package gitrepo_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/credential"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSSHRealConnectionCloneAndHostVerification(t *testing.T) {
	sshd := "/usr/sbin/sshd"
	if _, e := os.Stat(sshd); e != nil {
		t.Skip("sshd unavailable")
	}
	current, e := user.Current()
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	os.Mkdir(source, 0700)
	git(t, source, "init", "-b", "main")
	os.WriteFile(filepath.Join(source, "README.md"), []byte("SSH private repository"), 0600)
	git(t, source, "add", ".")
	git(t, source, "commit", "-m", "fixture")
	keygen := func(name string) {
		t.Helper()
		if output, e := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, name)).CombinedOutput(); e != nil {
			t.Fatal(string(output), e)
		}
	}
	keygen("host")
	keygen("client")
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	config := fmt.Sprintf("ListenAddress 127.0.0.1\nPort %d\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nStrictModes no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nAllowUsers %s\nLogLevel ERROR\n", port, filepath.Join(dir, "host"), filepath.Join(dir, "pid"), filepath.Join(dir, "client.pub"), current.Username)
	os.WriteFile(filepath.Join(dir, "sshd_config"), []byte(config), 0600)
	log, e := os.Create(filepath.Join(dir, "sshd.log"))
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	cmd := exec.Command(sshd, "-D", "-e", "-f", filepath.Join(dir, "sshd_config"))
	cmd.Stdout = log
	cmd.Stderr = log
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	ready := false
	for i := 0; i < 30; i++ {
		conn, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
		if e == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		b, _ := os.ReadFile(filepath.Join(dir, "sshd.log"))
		t.Fatalf("sshd did not start: %s", b)
	}
	private, _ := os.ReadFile(filepath.Join(dir, "client"))
	host, _ := os.ReadFile(filepath.Join(dir, "host.pub"))
	secret := credential.Secret{Mode: "ssh", PrivateKey: string(private), KnownHosts: fmt.Sprintf("[127.0.0.1]:%d %s", port, host)}
	db, e := sqlite.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	vault, e := credential.Open(filepath.Join(dir, "key"), db)
	if e != nil {
		t.Fatal(e)
	}
	repos := &gitrepo.Repositories{App: svc, Vault: vault, Root: filepath.Join(dir, "repos")}
	ctx := context.Background()
	repository := fmt.Sprintf("ssh://%s@127.0.0.1:%d%s", current.Username, port, source)
	result, e := repos.Test(ctx, repository, secret)
	if e != nil {
		b, _ := os.ReadFile(filepath.Join(dir, "sshd.log"))
		t.Fatalf("ssh connection: %v, server: %s", e, b)
	}
	if result.DefaultBranch != "main" {
		t.Fatal(result)
	}
	id, e := vault.Save(ctx, "owner", secret)
	if e != nil {
		t.Fatal(e)
	}
	p, e := svc.SaveProject(ctx, domain.Project{Name: "SSH", Repository: repository, Branch: "main", AuthMode: "ssh", CredentialID: id})
	if e != nil {
		t.Fatal(e)
	}
	if e = repos.Sync(ctx, p.ID); e != nil {
		t.Fatal(e)
	}
	wrong := secret
	wrong.KnownHosts = ""
	if _, e = repos.Test(ctx, repository, wrong); e == nil || !strings.Contains(e.Error(), "主机公钥") {
		t.Fatalf("untrusted SSH host accepted: %v", e)
	}
}
