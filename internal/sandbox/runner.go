// Package sandbox implements a deliberately narrow local runner protocol.
// The caller supplies identities only, never commands, images or mount paths.
package sandbox

import (
	"ai-pm-system/internal/domain"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Request struct {
	ProjectID      string `json:"project_id"`
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
	SnapshotKey    string `json:"snapshot_key"`
}

// Leave cleanup headroom above the platform's default 30-minute run limit.
const DefaultTimeout = 35 * time.Minute

type Config struct {
	UID           int
	GID           int
	DataRoot      string
	Image         string
	Network       string
	Docker        string
	MaxConcurrent int
	Timeout       time.Duration
}
type Runner struct {
	Config Config
	slots  chan struct{}
}

var identity = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
var snapshotKey = regexp.MustCompile(`^(none|[a-f0-9]{40}|[a-f0-9]{64})$`)

func New(c Config) (*Runner, error) {
	if c.Image == "" || strings.HasPrefix(c.Image, "-") || strings.ContainsAny(c.Image, " \n\r") {
		return nil, errors.New("必须指定固定的 Worker 镜像")
	}
	if c.Docker == "" {
		c.Docker = "docker"
	}
	if c.Network == "" {
		c.Network = "bridge"
	}
	if c.Network == "host" || c.Network == "container" || strings.HasPrefix(c.Network, "container:") {
		return nil, errors.New("沙盒不能共享宿主或其他容器的网络命名空间")
	}
	root, err := filepath.EvalSymlinks(c.DataRoot)
	if err != nil {
		return nil, err
	}
	c.DataRoot, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(c.DataRoot, ",\n\r") {
		return nil, errors.New("沙盒数据目录不能包含逗号或换行")
	}
	if c.UID <= 0 {
		c.UID = 1000
	}
	if c.GID <= 0 {
		c.GID = 1000
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 4
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	return &Runner{Config: c, slots: make(chan struct{}, c.MaxConcurrent)}, nil
}

func (r *Runner) mounts(q Request) (string, string, error) {
	if !identity.MatchString(q.ProjectID) || !identity.MatchString(q.ConversationID) || !identity.MatchString(q.RunID) || !snapshotKey.MatchString(q.SnapshotKey) {
		return "", "", errors.New("无效沙盒运行标识")
	}
	dir := filepath.Join(r.Config.DataRoot, "snapshots", q.ProjectID, q.SnapshotKey)
	if err := exactDirectory(dir); err != nil {
		return "", "", err
	}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", "", errors.New("源码快照尚未准备完成")
	}
	var s domain.CodeSnapshot
	if json.Unmarshal(b, &s) != nil || s.ProjectID != q.ProjectID || s.Key != q.SnapshotKey || (s.Status != "ready" && s.Status != "no_repository") || (s.Key != "none" && s.SHA != s.Key) {
		return "", "", errors.New("源码快照标识不一致")
	}
	source := filepath.Join(dir, "source")
	if err = exactDirectory(source); err != nil {
		return "", "", err
	}
	// Reuse the existing per-conversation Pi session directory, without mounting
	// another user's conversation or project-level credentials/model cache.
	state := filepath.Join(r.Config.DataRoot, "pi", q.ProjectID, "sessions", q.ConversationID)
	if err = mkdirExact(r.Config.DataRoot, state); err != nil {
		return "", "", err
	}
	// Runner normally runs on the host; the worker is UID 1000. Do not chown any
	// project source, only the newly created/current conversation directory.
	if os.Geteuid() == 0 {
		if err = os.Chown(state, r.Config.UID, r.Config.GID); err != nil {
			return "", "", err
		}
	}
	return source, state, nil
}

func exactDirectory(p string) error {
	real, err := filepath.EvalSymlinks(p)
	if err != nil || filepath.Clean(real) != filepath.Clean(p) {
		return errors.New("沙盒目录不存在或包含符号链接")
	}
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return errors.New("沙盒目录无效")
	}
	return nil
}
func mkdirExact(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil || !filepath.IsLocal(rel) {
		return errors.New("沙盒目录越界")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if err = os.Mkdir(current, 0750); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err = exactDirectory(current); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) createArgs(q Request, source, state string) []string {
	return []string{"create", "--pull=never", "--name", "ai-pm-run-" + q.RunID, "--label", "ai-pm.sandbox=true", "--label", "ai-pm.runner=" + r.label(), "--label", "ai-pm.run=" + q.RunID,
		"--interactive", "--init", "--user", fmt.Sprintf("%d:%d", r.Config.UID, r.Config.GID), "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
		"--cpus", "1", "--memory", "1g", "--memory-swap", "1g", "--pids-limit", "128", "--network", r.Config.Network,
		"--workdir", "/workspace", "--env", "HOME=/state", "--env", "NODE_ENV=production",
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=64m,mode=1777", "--tmpfs", fmt.Sprintf("/state:rw,noexec,nosuid,size=64m,uid=%d,gid=%d,mode=0700", r.Config.UID, r.Config.GID),
		"--mount", "type=bind,src=" + source + ",dst=/workspace,readonly,bind-propagation=rprivate",
		"--mount", "type=bind,src=" + state + ",dst=/state/sessions,bind-propagation=rprivate",
		"--entrypoint", "node", r.Config.Image, "/app/runtime/worker.mjs"}
}

func (r *Runner) Serve(ctx context.Context, listener net.Listener) error {
	go func() { <-ctx.Done(); _ = listener.Close() }()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() { defer wg.Done(); r.serveConn(ctx, conn) }()
	}
}
func (r *Runner) serveConn(parent context.Context, conn net.Conn) {
	defer conn.Close()
	ctx, cancel := context.WithTimeout(parent, r.Config.Timeout)
	defer cancel()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReaderSize(conn, 4096)
	line, prefix, err := reader.ReadLine()
	var q Request
	fail := func(message string) { _ = json.NewEncoder(conn).Encode(map[string]string{"error": message}) }
	if err != nil || prefix || json.Unmarshal(line, &q) != nil {
		fail("无效沙盒请求")
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	default:
		fail("沙盒并发已满，请稍后重试")
		return
	}
	source, state, err := r.mounts(q)
	if err != nil {
		fail(err.Error())
		return
	}
	name := "ai-pm-run-" + q.RunID
	create := exec.CommandContext(ctx, r.Config.Docker, r.createArgs(q, source, state)...)
	if err = create.Run(); err != nil {
		fail("沙盒创建失败，请检查 runner 镜像、挂载和 Docker 状态")
		return
	}
	defer func() {
		cleanup, c := context.WithTimeout(context.Background(), 15*time.Second)
		defer c()
		_ = exec.CommandContext(cleanup, r.Config.Docker, "rm", "--force", name).Run()
	}()
	if err = json.NewEncoder(conn).Encode(map[string]bool{"ready": true}); err != nil {
		return
	}
	cmd := exec.CommandContext(ctx, r.Config.Docker, "start", "--attach", "--interactive", name)
	// A closed client cancels the container promptly. A pipe keeps exec.Wait
	// independent of a blocked socket-copy goroutine after normal worker exit.
	input, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	cmd.Stdout = conn
	if err = cmd.Start(); err != nil {
		return
	}
	go func() { _, _ = io.Copy(input, reader); _ = input.Close(); cancel() }()
	if err = cmd.Wait(); err != nil && parent.Err() == nil {
		_ = json.NewEncoder(conn).Encode(map[string]string{"type": "error", "error": "沙盒 Worker 异常退出或超过运行限制"})
	}
}

// Connect returns a bidirectional JSON-lines stream after the runner's handshake.
func Connect(ctx context.Context, socket string, q Request) (net.Conn, *bufio.Reader, error) {
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, nil, errors.New("无法连接沙盒 runner；不会回退到非隔离执行")
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if err = json.NewEncoder(conn).Encode(q); err != nil {
		conn.Close()
		return nil, nil, err
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	var ack struct {
		Ready bool   `json:"ready"`
		Error string `json:"error"`
	}
	if err != nil || json.Unmarshal([]byte(line), &ack) != nil || !ack.Ready {
		conn.Close()
		if ack.Error != "" {
			return nil, nil, errors.New(ack.Error)
		}
		return nil, nil, fmt.Errorf("沙盒启动握手失败")
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, reader, nil
}

func (r *Runner) label() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(r.Config.DataRoot)))[:24]
}

// Reap is called once after acquiring the runner socket at startup. It only
// removes abandoned containers belonging to this configured data root.
func (r *Runner) Reap(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, r.Config.Docker, "ps", "-aq", "--filter", "label=ai-pm.runner="+r.label()).Output()
	if err != nil {
		return errors.New("无法核验 runner 的遗留容器")
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil
	}
	return exec.CommandContext(ctx, r.Config.Docker, append([]string{"rm", "--force"}, ids...)...).Run()
}
