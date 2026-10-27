package gitrepo

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/credential"
	"ai-pm-system/internal/domain"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Repositories struct {
	Vault         *credential.Vault
	Root          string
	App           *app.Service
	locks         sync.Map
	snapshotLocks sync.Map
	snapshotMu    sync.Mutex
	snapshotUsers map[string]int
}
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 2<<20 {
		left := (2 << 20) - b.Len()
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
func command(ctx context.Context, dir string, args ...string) (string, error) {
	opts, ok := ctx.Value(optionsKey{}).(commandOptions)
	if !ok {
		opts.config = []string{"-c", "protocol.ext.allow=never"}
	}
	cmd := exec.CommandContext(ctx, "git", append(opts.config, args...)...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	cmd.Env = append(cmd.Env, opts.env...)
	var out, stderr limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	e := cmd.Run()
	if e != nil {
		if len(args) > 0 && args[0] == "grep" && strings.TrimSpace(stderr.String()) == "" {
			return "", errors.New("Git:")
		}
		return "", safeGitError(ctx, stderr.String())
	}
	return out.String(), nil
}
func (r *Repositories) path(p string) string { return filepath.Join(r.Root, p) }
func (r *Repositories) Sync(ctx context.Context, id string) error {
	lock, _ := r.locks.LoadOrStore(id, &sync.Mutex{})
	m := lock.(*sync.Mutex)
	if !m.TryLock() {
		return errors.New("仓库正在同步")
	}
	defer m.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	p, err := r.App.Store.GetProject(ctx, id)
	if err != nil {
		return err
	}
	if p.Repository == "" {
		return errors.New("请先填写 Git 仓库地址")
	}
	err = r.sync(ctx, p)
	if err != nil {
		_ = r.App.UpdateSync(context.Background(), id, "", err.Error())
	}
	return err
}
func (r *Repositories) sync(c context.Context, p domain.Project) error {
	c, cleanup, e := r.projectAuth(c, p)
	if e != nil {
		return e
	}
	defer cleanup()
	dir := r.path(p.ID)
	if err := os.MkdirAll(r.Root, 0700); err != nil {
		return err
	}
	if _, e := os.Stat(filepath.Join(dir, "HEAD")); e != nil {
		if _, e = command(c, "", "clone", "--bare", "--no-hardlinks", "--", p.Repository, dir); e != nil {
			return e
		}
	} else {
		if _, e = command(c, dir, "fetch", "--prune", "origin", "+refs/heads/*:refs/heads/*"); e != nil {
			return e
		}
	}
	sha, e := r.resolve(c, p.ID, p.Branch)
	if e != nil {
		return e
	}
	tasks, e := r.App.Store.ListTasks(c, p.ID)
	if e != nil {
		return e
	}
	branches := []string{p.Branch}
	seen := map[string]bool{p.Branch: true}
	for _, t := range tasks {
		if t.Branch != "" && !seen[t.Branch] {
			branches = append(branches, t.Branch)
			seen[t.Branch] = true
		}
	}
	observations := map[string]string{}
	logs := []map[string]string{}
	for _, branch := range branches {
		ref, e := r.resolve(c, p.ID, branch)
		if e != nil {
			observations[branch] = "分支不可读取或已删除"
			continue
		}
		observations[branch] = ref
		log, e := command(c, dir, "log", "-15", "--format=%H%x09%s%x09%aI", ref, "--")
		if e != nil {
			return e
		}
		for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) == 3 {
				logs = append(logs, map[string]string{"sha": parts[0], "title": parts[1], "at": parts[2], "branch": branch})
			}
		}
	}
	data, _ := json.Marshal(map[string]any{"branches": observations, "commits": logs, "previous_sha": p.SHA})
	changed := p.SHA != sha
	if previous, err := r.App.Store.GetArtifact(c, "git_"+p.ID); err == nil {
		var snapshot struct {
			Branches map[string]string `json:"branches"`
		}
		_ = json.Unmarshal(previous.Data, &snapshot)
		before, _ := json.Marshal(snapshot.Branches)
		after, _ := json.Marshal(observations)
		changed = changed || string(before) != string(after)
	} else if errors.Is(err, domain.ErrNotFound) {
		changed = true
	} else {
		return err
	}
	a := domain.Artifact{ID: "git_" + p.ID, ProjectID: p.ID, Kind: "git", Title: "仓库同步记录", Body: fmt.Sprintf("已观察 %d 个分支，默认分支 %s", len(branches), p.Branch), SHA: sha, Source: "git", Data: data, At: domain.Now()}
	if e = r.App.Store.PutArtifact(c, a); e != nil {
		return e
	}
	if e = r.App.UpdateSync(c, p.ID, sha, ""); e != nil {
		return e
	}
	if changed {
		if e = r.App.Store.AppendEvent(c, domain.Event{ProjectID: p.ID, Kind: "git_sync", Actor: "Git", Body: "仓库观察更新，默认分支 " + sha[:12], Data: data, At: domain.Now()}); e != nil {
			return e
		}
		if r.App.OnChange != nil {
			r.App.OnChange(p.ID, "", "git_sync")
		}
	}
	return nil
}

var shaPattern = regexp.MustCompile(`^[a-fA-F0-9]{40,64}$`)

func (r *Repositories) resolve(c context.Context, p, ref string) (string, error) {
	if shaPattern.MatchString(ref) {
		s, e := command(c, r.path(p), "rev-parse", "--verify", ref+"^{commit}")
		return strings.TrimSpace(s), e
	}
	if ref == "" {
		project, e := r.App.Store.GetProject(c, p)
		if e != nil {
			return "", e
		}
		ref = project.Branch
	}
	if _, e := command(c, r.path(p), "check-ref-format", "refs/heads/"+ref); e != nil {
		return "", errors.New("分支名称不合法")
	}
	s, e := command(c, r.path(p), "rev-parse", "--verify", "refs/heads/"+ref+"^{commit}")
	return strings.TrimSpace(s), e
}

type Query struct {
	Action string `json:"action"`
	Ref    string `json:"ref"`
	Path   string `json:"path"`
	Query  string `json:"query"`
	Base   string `json:"base"`
}

func (r *Repositories) Read(c context.Context, p string, q Query) (map[string]any, error) {
	c, cancel := context.WithTimeout(c, 40*time.Second)
	defer cancel()
	project, e := r.App.Store.GetProject(c, p)
	if e != nil {
		return nil, e
	}
	c, cleanup, e := r.projectAuth(c, project)
	if e != nil {
		return nil, e
	}
	defer cleanup()
	ref, e := r.resolve(c, p, q.Ref)
	if e != nil {
		return nil, e
	}
	var out string
	switch q.Action {
	case "tree":
		args := []string{"ls-tree", "-r", "--name-only", ref, "--"}
		if q.Path != "" {
			args = append(args, q.Path)
		}
		out, e = command(c, r.path(p), args...)
	case "file":
		if q.Path == "" || strings.Contains(q.Path, "\x00") {
			return nil, errors.New("请指定文件路径")
		}
		out, e = command(c, r.path(p), "show", ref+":"+q.Path)
	case "search":
		if q.Query == "" {
			return nil, errors.New("请输入搜索文本")
		}
		args := []string{"grep", "-n", "-I", "-F", "-e", q.Query, ref, "--"}
		if q.Path != "" {
			args = append(args, q.Path)
		}
		out, e = command(c, r.path(p), args...)
		if e != nil && strings.TrimSpace(e.Error()) == "Git:" {
			e = nil
		}
	case "diff":
		base := q.Base
		if base == "" {
			out, e = command(c, r.path(p), "show", "--format=fuller", "--stat", "--patch", ref, "--")
		} else {
			var b string
			b, e = r.resolve(c, p, base)
			if e == nil {
				out, e = command(c, r.path(p), "diff", b, ref, "--")
			}
		}
	case "log":
		out, e = command(c, r.path(p), "log", "-20", "--format=%h %aI %s", ref, "--")
	default:
		return nil, errors.New("不支持的代码查询")
	}
	if e != nil {
		return nil, e
	}
	truncated := len(out) > 30000
	if truncated {
		out = out[:30000]
	}
	return map[string]any{"sha": ref, "path": q.Path, "text": out, "truncated": truncated, "source": "git"}, nil
}
