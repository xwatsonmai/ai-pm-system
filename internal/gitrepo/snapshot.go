package gitrepo

import (
	"ai-pm-system/internal/domain"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const snapshotMaxBytes = 1 << 30
const snapshotMaxFiles = 100000

// AcquireSnapshot pins one immutable tree for a run. Only the source directory is
// mounted into the worker; the manifest is owned by the platform, not the repo.
func (r *Repositories) AcquireSnapshot(ctx context.Context, projectID, requestedRef string) (domain.CodeSnapshot, func(), error) {
	gate := r.snapshotGate(projectID)
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return domain.CodeSnapshot{}, nil, ctx.Err()
	}
	p, err := r.App.Store.GetProject(ctx, projectID)
	if err != nil {
		return domain.CodeSnapshot{}, nil, err
	}
	if !safeComponent(projectID) {
		return domain.CodeSnapshot{}, nil, errors.New("无效项目 ID")
	}
	ref := requestedRef
	if ref == "" {
		ref = p.AgentRef
	}
	if ref == "" {
		ref = p.Branch
	}
	s := domain.CodeSnapshot{ProjectID: projectID, Ref: ref, SyncedAt: p.SyncedAt, Status: "ready", Key: "none"}
	if p.Repository != "" {
		s.SHA, err = r.resolve(ctx, projectID, ref)
		if err != nil {
			return s, nil, fmt.Errorf("无法准备指定代码版本，请先同步仓库：%w", err)
		}
		s.Key = s.SHA
	} else {
		if requestedRef != "" || p.AgentRef != "" {
			return s, nil, errors.New("项目未配置仓库，不能指定代码版本")
		}
		s.Status, s.Ref = "no_repository", ""
	}
	dir := filepath.Join(r.snapshotRoot(), projectID, s.Key)
	if b, e := os.ReadFile(filepath.Join(dir, "manifest.json")); e == nil {
		var cached domain.CodeSnapshot
		if json.Unmarshal(b, &cached) != nil || cached.ProjectID != projectID || cached.Key != s.Key || cached.SHA != s.SHA {
			return s, nil, errors.New("源码快照清单不一致")
		}
		cached.Ref, cached.SyncedAt = s.Ref, s.SyncedAt
		s = cached
	} else {
		if !errors.Is(e, os.ErrNotExist) {
			return s, nil, e
		}
		if err = os.MkdirAll(filepath.Dir(dir), 0750); err != nil {
			return s, nil, err
		}
		tmp, e := os.MkdirTemp(filepath.Dir(dir), ".building-")
		if e != nil {
			return s, nil, e
		}
		defer os.RemoveAll(tmp)
		if err = os.Mkdir(filepath.Join(tmp, "source"), 0755); err != nil {
			return s, nil, err
		}
		if s.SHA != "" {
			if err = r.exportTree(ctx, projectID, s.SHA, filepath.Join(tmp, "source"), &s); err != nil {
				return s, nil, err
			}
		}
		s.CreatedAt = domain.Now()
		b, _ := json.Marshal(s)
		if err = os.WriteFile(filepath.Join(tmp, "manifest.json"), b, 0444); err != nil {
			return s, nil, err
		}
		if err = os.Chmod(tmp, 0755); err != nil {
			return s, nil, err
		}
		if err = os.Rename(tmp, dir); err != nil {
			return s, nil, err
		}
	}
	// A writable marker lives outside source; touching it cannot change code.
	if err = os.WriteFile(filepath.Join(dir, ".last-used"), nil, 0600); err != nil {
		return s, nil, err
	}
	r.snapshotMu.Lock()
	if r.snapshotUsers == nil {
		r.snapshotUsers = map[string]int{}
	}
	r.snapshotUsers[dir]++
	r.snapshotMu.Unlock()
	released := false
	release := func() {
		r.snapshotMu.Lock()
		defer r.snapshotMu.Unlock()
		if !released {
			r.snapshotUsers[dir]--
			released = true
		}
	}
	return s, release, nil
}

func safeComponent(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func (r *Repositories) snapshotRoot() string { return filepath.Join(filepath.Dir(r.Root), "snapshots") }

func (r *Repositories) snapshotGate(projectID string) chan struct{} {
	gate, _ := r.snapshotLocks.LoadOrStore(projectID, make(chan struct{}, 1))
	return gate.(chan struct{})
}

// ExpireSnapshots is called by maintenance, never removes an in-use tree, and
// leaves recent snapshots available for concurrent workers and explicit rollback.
func (r *Repositories) ExpireSnapshots(before time.Time) error {
	projects, err := os.ReadDir(r.snapshotRoot())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, p := range projects {
		if !p.IsDir() || !safeComponent(p.Name()) {
			continue
		}
		gate := r.snapshotGate(p.Name())
		select {
		case gate <- struct{}{}:
		default:
			continue // Preparation in progress; do not delay another project.
		}
		e := r.expireProjectSnapshots(p.Name(), before)
		<-gate
		if e != nil {
			return e
		}
	}
	return nil
}

func (r *Repositories) expireProjectSnapshots(projectID string, before time.Time) error {
	entries, e := os.ReadDir(filepath.Join(r.snapshotRoot(), projectID))
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if !entry.IsDir() || !(shaPattern.MatchString(entry.Name()) || entry.Name() == "none") {
			continue
		}
		dir := filepath.Join(r.snapshotRoot(), projectID, entry.Name())
		info, e := os.Stat(filepath.Join(dir, ".last-used"))
		r.snapshotMu.Lock()
		inUse := r.snapshotUsers[dir] > 0
		r.snapshotMu.Unlock()
		if e == nil && info.ModTime().Before(before) && !inUse {
			if e = os.RemoveAll(dir); e != nil {
				return e
			}
		}
	}
	return nil
}

type treeEntry struct{ mode, oid, name string }

func (r *Repositories) exportTree(ctx context.Context, projectID, sha, dest string, s *domain.CodeSnapshot) error {
	// Read raw Git objects: checkout filters, hooks, export-ignore/export-subst and
	// repository executables must never run or silently change the source snapshot.
	list := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "-C", r.path(projectID), "ls-tree", "-rz", "--full-tree", sha)
	pipe, err := list.StdoutPipe()
	if err != nil {
		return err
	}
	if err = list.Start(); err != nil {
		return err
	}
	var entries []treeEntry
	limited := &io.LimitedReader{R: pipe, N: 64 << 20}
	reader := bufio.NewReader(limited)
	for {
		record, e := reader.ReadString(0)
		if e == io.EOF && record == "" {
			if limited.N == 0 {
				_ = list.Process.Kill()
				_ = list.Wait()
				return errors.New("Git 文件清单超过 64 MiB 限制")
			}
			break
		}
		if e != nil {
			_ = list.Process.Kill()
			_ = list.Wait()
			return errors.New("Git 文件清单不完整或超出限制")
		}
		header, name, ok := strings.Cut(strings.TrimSuffix(record, "\x00"), "\t")
		parts := strings.Fields(header)
		if !ok || len(parts) != 3 || !validTreePath(name) || len(entries) >= snapshotMaxFiles {
			_ = list.Process.Kill()
			_ = list.Wait()
			return errors.New("Git 文件路径或数量不符合快照限制")
		}
		entries = append(entries, treeEntry{parts[0], parts[2], name})
	}
	if err = list.Wait(); err != nil {
		return errors.New("无法读取完整 Git 文件清单")
	}
	batch := exec.CommandContext(ctx, "git", "-C", r.path(projectID), "cat-file", "--batch")
	in, err := batch.StdinPipe()
	if err != nil {
		return err
	}
	out, err := batch.StdoutPipe()
	if err != nil {
		return err
	}
	if err = batch.Start(); err != nil {
		return err
	}
	defer func() { _ = in.Close(); _ = batch.Process.Kill(); _ = batch.Wait() }()
	br := bufio.NewReader(out)
	type link struct{ name, target string }
	var links []link
	for _, entry := range entries {
		if entry.mode == "160000" {
			s.Submodules = append(s.Submodules, domain.CodeSubmodule{Path: entry.name, SHA: entry.oid, Status: "not_expanded"})
			continue
		}
		if entry.mode != "100644" && entry.mode != "100755" && entry.mode != "120000" {
			return errors.New("不支持的 Git 对象类型")
		}
		if _, err = fmt.Fprintln(in, entry.oid); err != nil {
			return err
		}
		header, e := br.ReadString('\n')
		if e != nil {
			return e
		}
		parts := strings.Fields(header)
		if len(parts) != 3 || parts[0] != entry.oid || parts[1] != "blob" {
			return errors.New("Git blob 缺失")
		}
		n, e := strconv.ParseInt(parts[2], 10, 64)
		if e != nil || n < 0 || n > snapshotMaxBytes-s.Bytes {
			return errors.New("源码快照超过 1 GiB 限制")
		}
		s.Bytes += n
		s.Files++
		file := filepath.Join(dest, filepath.FromSlash(entry.name))
		if entry.mode == "120000" {
			if n > 4096 {
				return errors.New("符号链接长度超限")
			}
			b := make([]byte, n)
			if _, err = io.ReadFull(br, b); err != nil {
				return err
			}
			target := string(b)
			resolved := path.Clean(path.Join(path.Dir(entry.name), target))
			if path.IsAbs(target) || !validTreePath(resolved) || strings.ContainsRune(target, '\x00') {
				s.Warnings = append(s.Warnings, "未展开越界符号链接："+entry.name)
			} else {
				links = append(links, link{file, target})
			}
		} else {
			if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
				return err
			}
			f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0444)
			if e != nil {
				return e
			}
			// A short prefix detects an LFS pointer without loading large blobs.
			prefix := make([]byte, min(n, 200))
			_, e = io.ReadFull(br, prefix)
			if e == nil {
				_, e = f.Write(prefix)
			}
			if e == nil {
				_, e = io.CopyN(f, br, n-int64(len(prefix)))
			}
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
			if strings.HasPrefix(string(prefix), "version https://git-lfs.github.com/spec/v1\n") {
				s.Warnings = append(s.Warnings, "Git LFS 内容未展开："+entry.name)
			}
		}
		if b, e := br.ReadByte(); e != nil || b != '\n' {
			return errors.New("Git blob 数据不完整")
		}
	}
	// Create links last so exporting a later file can never follow repository links.
	for _, l := range links {
		if err = os.MkdirAll(filepath.Dir(l.name), 0755); err != nil {
			return err
		}
		if err = os.Symlink(l.target, l.name); err != nil {
			return err
		}
	}
	// Lexically safe links can still escape when a target traverses another
	// symlink before "..". Validate the complete chain after creating every link.
	canonicalDest, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return err
	}
	for _, l := range links {
		resolved, e := filepath.EvalSymlinks(l.name)
		rel, relErr := filepath.Rel(canonicalDest, resolved)
		if e != nil || relErr != nil || !filepath.IsLocal(rel) {
			if err = os.Remove(l.name); err != nil {
				return err
			}
			name, _ := filepath.Rel(dest, l.name)
			s.Warnings = append(s.Warnings, "未展开无法安全解析的符号链接："+filepath.ToSlash(name))
		}
	}
	return nil
}

func validTreePath(name string) bool {
	if !filepath.IsLocal(name) || strings.Contains(name, "\\") || path.Clean(name) != name {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.EqualFold(part, ".git") || part == ".." {
			return false
		}
	}
	return true
}
