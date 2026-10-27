package gitrepo_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/credential"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/storage/sqlite"
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTokenAuthenticationRealCloneFetchAndSecretIsolation(t *testing.T) {
	dir := t.TempDir()
	// The auth script must work without execute permission, including when
	// its path needs shell quoting, as on the deployed noexec /tmp mount.
	authTemp := filepath.Join(dir, "auth directory 'with spaces'")
	if e := os.Mkdir(authTemp, 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("TMPDIR", authTemp)
	const fixtureUsername = "gitlab+deploy-token-1"
	source := filepath.Join(dir, "source")
	os.Mkdir(source, 0700)
	git(t, source, "init", "-b", "main")
	os.WriteFile(filepath.Join(source, "README.md"), []byte("private repository"), 0600)
	git(t, source, "add", ".")
	git(t, source, "commit", "-m", "initial")
	git(t, source, "branch", "feature/test")
	git(t, dir, "clone", "--bare", source, filepath.Join(dir, "repo.git"))
	var authenticated atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, token, ok := r.BasicAuth()
		if !ok || user != fixtureUsername || token != "test-token-SECRET" {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(401)
			return
		}
		helperFiles, e := filepath.Glob(filepath.Join(authTemp, "ai-pm-git-auth-*", "credential-helper"))
		if e != nil || len(helperFiles) != 1 {
			t.Errorf("expected one active auth script: %v %v", helperFiles, e)
			w.WriteHeader(500)
			return
		}
		info, e := os.Stat(helperFiles[0])
		if e != nil || info.Mode().Perm()&0111 != 0 {
			t.Errorf("auth script must work without execute permission: %v", e)
			w.WriteHeader(500)
			return
		}
		authenticated.Add(1)
		cmd := exec.Command("git", "http-backend")
		cmd.Env = append(os.Environ(), "GIT_PROJECT_ROOT="+dir, "GIT_HTTP_EXPORT_ALL=1", "PATH_INFO="+r.URL.Path, "QUERY_STRING="+r.URL.RawQuery, "REQUEST_METHOD="+r.Method, "CONTENT_TYPE="+r.Header.Get("Content-Type"), "REMOTE_USER=test-user")
		cmd.Stdin = r.Body
		out, e := cmd.Output()
		if e != nil {
			t.Error(e)
			w.WriteHeader(500)
			return
		}
		reader := bufio.NewReader(bytes.NewReader(out))
		headers, e := textproto.NewReader(reader).ReadMIMEHeader()
		if e != nil {
			t.Error(e)
			w.WriteHeader(500)
			return
		}
		for k, vs := range headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		io.Copy(w, reader)
	}))
	defer provider.Close()
	db, e := sqlite.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	svc := &app.Service{Store: db}
	vault, e := credential.Open(filepath.Join(dir, "key"), db)
	if e != nil {
		t.Fatal(e)
	}
	repos := &gitrepo.Repositories{Root: filepath.Join(dir, "clones"), App: svc, Vault: vault}
	secret := credential.Secret{Mode: "token", Username: fixtureUsername, Token: "test-token-SECRET"}
	repository := provider.URL + "/repo.git"
	conn, e := repos.Test(ctx, repository, secret)
	if e != nil || conn.DefaultBranch != "main" || len(conn.Branches) != 2 {
		t.Fatalf("test connection: %+v %v", conn, e)
	}
	wrong := secret
	wrong.Token = "wrong-SECRET"
	if _, e = repos.Test(ctx, repository, wrong); e == nil || strings.Contains(e.Error(), "SECRET") {
		t.Fatalf("credential failure not safe: %v", e)
	}
	if _, e = repos.Test(ctx, repository, credential.Secret{Mode: "none"}); e == nil {
		t.Fatal("public mode borrowed a credential")
	}
	cred, e := vault.Save(ctx, "owner", secret)
	if e != nil {
		t.Fatal(e)
	}
	p, e := svc.SaveProject(ctx, domain.Project{Name: "private", Repository: repository, Branch: "main", AuthMode: "token", CredentialID: cred})
	if e != nil {
		t.Fatal(e)
	}
	if e = repos.Sync(ctx, p.ID); e != nil {
		t.Fatal(e)
	}
	if e = repos.Sync(ctx, p.ID); e != nil {
		t.Fatal("fetch", e)
	}
	file, e := repos.Read(ctx, p.ID, gitrepo.Query{Action: "file", Path: "README.md"})
	if e != nil || file["text"] != "private repository" {
		t.Fatal(file, e)
	}
	config, _ := os.ReadFile(filepath.Join(repos.Root, p.ID, "config"))
	if strings.Contains(string(config), secret.Token) || strings.Contains(string(config), secret.Username) {
		t.Fatal("credential persisted in git config")
	}
	if authenticated.Load() < 3 {
		t.Fatal("git operations did not authenticate")
	}
}
func TestSSHKeyGenerationValidationAndAddressRules(t *testing.T) {
	dir := t.TempDir()
	db, e := sqlite.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	vault, e := credential.Open(filepath.Join(dir, "key"), db)
	if e != nil {
		t.Fatal(e)
	}
	repos := &gitrepo.Repositories{Vault: vault}
	generated, e := repos.GenerateKey(ctx, "alice")
	if e != nil {
		t.Fatal(e)
	}
	if generated["private_key"] != "" || !strings.HasPrefix(generated["public_key"], "ssh-ed25519 ") {
		t.Fatal("bad public response")
	}
	secret, owner, e := vault.Load(ctx, generated["credential_id"])
	if e != nil || owner != "alice" {
		t.Fatal(e)
	}
	secret.KnownHosts = "git.example.test " + generated["public_key"]
	checked, e := gitrepo.NormalizeSecret(secret)
	if e != nil || checked.PublicKey == "" {
		t.Fatal(e)
	}
	checked.PrivateKey = "not a key"
	if _, e = gitrepo.NormalizeSecret(checked); e == nil {
		t.Fatal("invalid key accepted")
	}
	for _, repository := range []string{"-evil", "ext::command", "https://user:password@example.test/repo", "https://TOKEN@example.test/repo", "/private/server/repo", "file:///private/repo", "https://example.test/repo?token=SECRET"} {
		if e = gitrepo.ValidateRepository(repository, "none", false); e == nil {
			t.Fatalf("unsafe address accepted: %s", repository)
		}
	}
	if e = gitrepo.ValidateRepository("git@example.test:team/repo.git", "ssh", false); e != nil {
		t.Fatal(e)
	}
}
