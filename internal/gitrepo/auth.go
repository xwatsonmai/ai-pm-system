package gitrepo

import (
	"ai-pm-system/internal/credential"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type optionsKey struct{}
type commandOptions struct{ env, config []string }

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func ValidateRepository(repository, mode string, allowLocal bool) error {
	if repository == "" {
		return nil
	}
	if strings.HasPrefix(repository, "-") || strings.ContainsAny(repository, "\n\r\x00") || (!strings.Contains(repository, "://") && strings.Contains(repository, "::")) {
		return errors.New("Git 地址不合法")
	}
	remoteSSH := false
	if strings.Contains(repository, "://") {
		u, e := url.Parse(repository)
		if e != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("Git 地址不合法，请勿在地址中填写凭据")
		}
		if u.Scheme != "file" && u.Hostname() == "" {
			return errors.New("Git 地址缺少主机")
		}
		if u.User != nil {
			if _, ok := u.User.Password(); ok {
				return errors.New("请在认证配置中填写凭据，不要放在 Git 地址里")
			}
			if u.Scheme != "ssh" {
				return errors.New("HTTPS 地址不要包含用户名或 Token")
			}
		}
		switch u.Scheme {
		case "http", "https":
			if mode == "ssh" {
				return errors.New("SSH 密钥需要 SSH 仓库地址")
			}
			return nil
		case "ssh":
			remoteSSH = true
		case "file":
			if !allowLocal {
				return errors.New("登录模式下新项目不支持服务器本地路径")
			}
		default:
			return errors.New("只支持 HTTPS、SSH 或本地仓库路径")
		}
	} else if !strings.HasPrefix(repository, "/") && strings.Contains(repository, ":") {
		host, _, _ := strings.Cut(repository, ":")
		if strings.ContainsAny(host, " /\\") || strings.HasPrefix(host, "-") {
			return errors.New("SSH 仓库地址不合法")
		}
		remoteSSH = true
	} else if !allowLocal {
		return errors.New("请填写 HTTPS 或 SSH 仓库地址")
	}
	if mode == "token" {
		return errors.New("Token 认证需要 HTTP 或 HTTPS 仓库地址")
	}
	if mode == "ssh" && !remoteSSH {
		return errors.New("SSH 密钥需要 SSH 仓库地址")
	}
	return nil
}
func NormalizeSecret(secret credential.Secret) (credential.Secret, error) {
	switch secret.Mode {
	case "none", "server":
		return credential.Secret{Mode: secret.Mode}, nil
	case "token":
		if strings.TrimSpace(secret.Username) == "" || secret.Token == "" {
			return secret, errors.New("请填写 Git 用户名和访问令牌")
		}
		if strings.ContainsAny(secret.Username+secret.Token, "\r\n\x00") {
			return secret, errors.New("用户名和令牌不能包含换行")
		}
		return credential.Secret{Mode: "token", Username: secret.Username, Token: secret.Token}, nil
	case "ssh":
		if secret.PrivateKey == "" {
			return secret, errors.New("请粘贴专用 SSH 私钥，或生成密钥")
		}
		dir, e := os.MkdirTemp("", "ai-pm-key-check-")
		if e != nil {
			return secret, e
		}
		defer os.RemoveAll(dir)
		key := filepath.Join(dir, "key")
		if e = os.WriteFile(key, []byte(strings.TrimSpace(secret.PrivateKey)+"\n"), 0600); e != nil {
			return secret, e
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		output, e := exec.CommandContext(ctx, "ssh-keygen", "-y", "-P", "", "-f", key).Output()
		if e != nil {
			return secret, errors.New("SSH 私钥格式无效或带有口令；请使用仓库专用的无口令密钥")
		}
		if strings.TrimSpace(secret.KnownHosts) == "" {
			return secret, errors.New("请填写经仓库管理员确认的 SSH 主机公钥（known_hosts）")
		}
		hosts := filepath.Join(dir, "known_hosts")
		if e = os.WriteFile(hosts, []byte(secret.KnownHosts+"\n"), 0600); e != nil {
			return secret, e
		}
		if _, e = exec.CommandContext(ctx, "ssh-keygen", "-l", "-f", hosts).Output(); e != nil {
			return secret, errors.New("SSH 主机公钥格式无效，需要 known_hosts 格式")
		}
		return credential.Secret{Mode: "ssh", PrivateKey: strings.TrimSpace(secret.PrivateKey) + "\n", KnownHosts: secret.KnownHosts, PublicKey: strings.TrimSpace(string(output))}, nil
	default:
		return secret, errors.New("不支持的仓库认证方式")
	}
}
func (r *Repositories) GenerateKey(ctx context.Context, owner string) (map[string]string, error) {
	if r.Vault == nil {
		return nil, errors.New("仓库凭据存储未初始化")
	}
	dir, e := os.MkdirTemp("", "ai-pm-keygen-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	path := filepath.Join(dir, "key")
	if _, e = exec.CommandContext(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "ai-pm-readonly", "-f", path).Output(); e != nil {
		return nil, errors.New("无法生成 SSH 密钥，请检查 ssh-keygen")
	}
	private, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	public, e := os.ReadFile(path + ".pub")
	if e != nil {
		return nil, e
	}
	id, e := r.Vault.Save(ctx, owner, credential.Secret{Mode: "ssh", PrivateKey: string(private), PublicKey: strings.TrimSpace(string(public))})
	if e != nil {
		return nil, e
	}
	return map[string]string{"credential_id": id, "public_key": strings.TrimSpace(string(public))}, nil
}
func withAuth(ctx context.Context, secret credential.Secret) (context.Context, func(), error) {
	opts := commandOptions{env: []string{"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_LFS_SKIP_SMUDGE=1", "LC_ALL=C"}, config: []string{"-c", "protocol.ext.allow=never", "-c", "core.hooksPath=/dev/null"}}
	if secret.Mode == "" || secret.Mode == "server" {
		opts.env = append(opts.env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=10 -o ServerAliveInterval=10 -o ServerAliveCountMax=2 -o StrictHostKeyChecking=yes")
		return context.WithValue(ctx, optionsKey{}, opts), func() {}, nil
	}
	dir, e := os.MkdirTemp("", "ai-pm-git-auth-")
	if e != nil {
		return ctx, nil, e
	}
	cleanup := func() { os.RemoveAll(dir) }
	write := func(name, body string, mode os.FileMode) error {
		return os.WriteFile(filepath.Join(dir, name), []byte(body), mode)
	}
	// Explicit modes ignore machine credentials, HTTP extra headers and URL rewrites.
	opts.env = append(opts.env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_COUNT=0", "GIT_CONFIG_PARAMETERS=", "GIT_SSL_NO_VERIFY=false", "SSH_ASKPASS=/usr/bin/false", "GIT_ASKPASS=/usr/bin/false")
	opts.config = append(opts.config, "-c", "credential.helper=", "-c", "http.extraHeader=", "-c", "http.followRedirects=false")
	sshCommand := "ssh -F /dev/null -o BatchMode=yes -o ConnectTimeout=10 -o ServerAliveInterval=10 -o ServerAliveCountMax=2 -o IdentitiesOnly=yes -o IdentityAgent=none -o StrictHostKeyChecking=yes"
	if secret.Mode == "ssh" {
		if e = write("key", secret.PrivateKey, 0600); e == nil {
			e = write("known_hosts", secret.KnownHosts+"\n", 0600)
		}
		sshCommand += " -o GlobalKnownHostsFile=/dev/null -o UserKnownHostsFile=" + shellQuote(filepath.Join(dir, "known_hosts")) + " -i " + shellQuote(filepath.Join(dir, "key"))
	} else {
		sshCommand += " -i /dev/null"
	}
	opts.env = append(opts.env, "GIT_SSH_COMMAND="+sshCommand)
	if secret.Mode == "token" && e == nil {
		if e = write("username", secret.Username, 0600); e == nil {
			e = write("token", secret.Token, 0600)
		}
		script := "#!/bin/sh\ncase \"$1\" in\n get) printf 'username='; cat \"$AI_PM_GIT_AUTH/username\"; printf '\\npassword='; cat \"$AI_PM_GIT_AUTH/token\"; printf '\\n\\n';;\nesac\n"
		if e == nil {
			e = write("credential-helper", script, 0600)
		}
		// Git invokes ! credential helpers through a shell. The trusted shell
		// reads the private script, so credentials work on a noexec tmpfs too.
		opts.env = append(opts.env, "AI_PM_GIT_AUTH="+dir)
		opts.config = append(opts.config, "-c", "credential.helper=!/bin/sh \"$AI_PM_GIT_AUTH/credential-helper\"")
	}
	if e != nil {
		cleanup()
		return ctx, nil, e
	}
	return context.WithValue(ctx, optionsKey{}, opts), cleanup, nil
}
func (r *Repositories) projectAuth(ctx context.Context, p domain.Project) (context.Context, func(), error) {
	secret := credential.Secret{Mode: p.AuthMode}
	if p.CredentialID != "" {
		if r.Vault == nil {
			return ctx, nil, errors.New("仓库凭据存储未初始化")
		}
		var e error
		secret, _, e = r.Vault.Load(ctx, p.CredentialID)
		if e != nil {
			return ctx, nil, e
		}
	}
	return withAuth(ctx, secret)
}

type Connection struct {
	Branches      []string `json:"branches"`
	DefaultBranch string   `json:"default_branch"`
	Message       string   `json:"message"`
}

func (r *Repositories) Test(ctx context.Context, repository string, secret credential.Secret) (Connection, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	ctx, cleanup, e := withAuth(ctx, secret)
	if e != nil {
		return Connection{}, e
	}
	defer cleanup()
	out, e := command(ctx, "", "ls-remote", "--symref", "--", repository, "HEAD", "refs/heads/*")
	if e != nil {
		return Connection{}, e
	}
	result := Connection{Branches: []string{}, Message: "连接成功，已验证仓库读取权限"}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			continue
		}
		if parts[1] == "HEAD" && strings.HasPrefix(parts[0], "ref: refs/heads/") {
			result.DefaultBranch = strings.TrimPrefix(parts[0], "ref: refs/heads/")
		}
		if strings.HasPrefix(parts[1], "refs/heads/") {
			name := strings.TrimPrefix(parts[1], "refs/heads/")
			if !seen[name] {
				result.Branches = append(result.Branches, name)
				seen[name] = true
			}
		}
	}
	sort.Strings(result.Branches)
	if len(result.Branches) == 0 {
		result.Message = "连接成功，仓库还没有分支；请先推送代码"
	}
	return result, nil
}
func safeGitError(ctx context.Context, stderr string) error {
	if ctx.Err() != nil {
		return errors.New("仓库连接超时或已取消，请检查网络后重试")
	}
	text := strings.ToLower(stderr)
	switch {
	case strings.Contains(text, "does not exist in"), strings.Contains(text, "exists on disk, but not in"):
		return errors.New("PATH_NOT_FOUND：该路径在指定提交中不存在，请从仓库根目录核对路径")
	case strings.Contains(text, "host key verification failed"), strings.Contains(text, "host identification has changed"):
		return errors.New("SSH 主机公钥未受信任或已变化，请核对 known_hosts")
	case (strings.Contains(text, "cannot run") || strings.Contains(text, "cannot exec") || strings.Contains(text, "cannot open")) && (strings.Contains(text, "askpass") || strings.Contains(text, "credential-helper")):
		return errors.New("Git 认证辅助程序无法运行，请检查服务端临时目录和运行环境")
	case strings.Contains(text, "authentication failed"), strings.Contains(text, "permission denied"), strings.Contains(text, "could not read username"), strings.Contains(text, "403"), strings.Contains(text, "401"):
		return errors.New("仓库认证失败或缺少读取权限，请检查凭据和仓库授权")
	case strings.Contains(text, "not found"), strings.Contains(text, "does not appear to be a git repository"):
		return errors.New("仓库不存在或当前凭据无权查看，请检查地址和授权")
	case strings.Contains(text, "could not resolve"), strings.Contains(text, "failed to connect"), strings.Contains(text, "connection refused"), strings.Contains(text, "timed out"):
		return errors.New("无法连接仓库，请检查地址、网络和端口")
	case strings.Contains(text, "ssl certificate"):
		return errors.New("仓库 TLS 证书验证失败，请配置可信证书")
	case strings.Contains(text, "needed a single revision"), strings.Contains(text, "unknown revision"):
		return errors.New("所选分支或版本不存在，请重新测试连接并选择分支")
	default:
		return errors.New("Git 读取失败，请检查仓库地址、分支和访问权限")
	}
}
