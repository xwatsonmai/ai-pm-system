package gitrepo

import (
	"ai-pm-system/internal/domain"
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
)

// linePage counts all rows while retaining only the requested page. Unlike the
// legacy command buffer, a large tree/search cannot silently lose later pages.
type linePage struct {
	page      domain.PageQuery
	pending   []byte
	items     []string
	total     int
	truncated bool
}

func (w *linePage) row() {
	if w.total >= w.page.Offset() && w.total < w.page.Offset()+w.page.PageSize {
		w.items = append(w.items, string(w.pending))
	}
	w.total++
	w.pending = w.pending[:0]
}
func (w *linePage) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		at := bytes.IndexByte(p, '\n')
		part := p
		if at >= 0 {
			part = p[:at]
		}
		remaining := 65536 - len(w.pending)
		if remaining < len(part) {
			part = part[:max(remaining, 0)]
			w.truncated = true
		}
		w.pending = append(w.pending, part...)
		if at < 0 {
			break
		}
		w.row()
		p = p[at+1:]
	}
	return n, nil
}
func (w *linePage) finish() {
	if len(w.pending) > 0 {
		w.row()
	}
}
func pagedCommand(c context.Context, dir string, page domain.PageQuery, args ...string) (*linePage, error) {
	opts, ok := c.Value(optionsKey{}).(commandOptions)
	if !ok {
		opts.config = []string{"-c", "protocol.ext.allow=never"}
	}
	cmd := exec.CommandContext(c, "git", append(opts.config, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	cmd.Env = append(cmd.Env, opts.env...)
	out := &linePage{page: page, items: []string{}}
	var stderr limitedBuffer
	cmd.Stdout = out
	cmd.Stderr = &stderr
	if e := cmd.Run(); e != nil {
		if len(args) > 0 && args[0] == "grep" && strings.TrimSpace(stderr.String()) == "" {
			return out, nil
		}
		return nil, safeGitError(c, stderr.String())
	}
	out.finish()
	return out, nil
}
func (r *Repositories) readListPage(c context.Context, p, ref string, q Query) (map[string]any, error) {
	page, e := (domain.PageQuery{Page: q.Page, PageSize: q.PageSize}).Normalized()
	if e != nil {
		return nil, e
	}
	args := []string{}
	switch q.Action {
	case "tree":
		args = []string{"ls-tree", "-r", "--name-only", ref, "--"}
		if q.Path != "" {
			args = append(args, q.Path)
		}
	case "search":
		if q.Query == "" {
			return nil, domain.ErrNotFound
		}
		args = []string{"grep", "-n", "-I", "-F", "-e", q.Query, ref, "--"}
		if q.Path != "" {
			args = append(args, q.Path)
		}
	case "log":
		args = []string{"log", "--format=%h %aI %s", ref, "--"}
	}
	out, e := pagedCommand(c, r.path(p), page, args...)
	if e != nil {
		return nil, e
	}
	if out.total > 0 && page.Offset() >= out.total {
		page.Page = (out.total-1)/page.PageSize + 1
		out, e = pagedCommand(c, r.path(p), page, args...)
		if e != nil {
			return nil, e
		}
	}
	if out.total == 0 {
		page.Page = 1
	}
	return map[string]any{"sha": ref, "path": q.Path, "items": out.items, "total": out.total, "page": page.Page, "page_size": page.PageSize, "text": strings.Join(out.items, "\n"), "truncated": out.truncated, "source": "git", "has_more": page.Offset()+len(out.items) < out.total, "next_offset": page.Offset() + len(out.items)}, nil
}
