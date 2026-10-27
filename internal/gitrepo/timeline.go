package gitrepo

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var timelineSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// History is read from the already-synchronized mirror, never fetched on a GET.
// An anchor pins the same branch heads throughout pagination.
func (r *Repositories) Timeline(c context.Context, p string, from, until int64, offset, limit int, anchor string) (domain.GitTimeline, error) {
	out := domain.GitTimeline{Commits: []domain.GitCommit{}, NextOffset: offset}
	project, e := r.App.Store.GetProject(c, p)
	if e != nil {
		return out, e
	}
	out.SyncedAt = project.SyncedAt
	if project.Repository == "" {
		out.Warning = "尚未连接代码仓库"
		return out, nil
	}
	if project.SHA == "" {
		out.Warning = "仓库尚未同步，暂时没有提交记录"
		return out, nil
	}
	if from < 0 || until <= from || offset < 0 || limit < 1 || limit > 200 {
		return out, errors.New("时间线查询范围不合法")
	}
	c, cancel := context.WithTimeout(c, 20*time.Second)
	defer cancel()
	refs := []string{}
	if anchor == "" {
		raw, e := command(c, r.path(p), "for-each-ref", "--format=%(objectname)", "refs/heads")
		if e != nil {
			return out, e
		}
		seen := map[string]bool{}
		for _, sha := range strings.Fields(raw) {
			if !seen[sha] && timelineSHA.MatchString(sha) {
				refs = append(refs, sha)
				seen[sha] = true
			}
		}
		sort.Strings(refs)
		if len(refs) > 512 {
			refs = refs[:512]
			out.Warning = "分支较多，此窗口展示前512个分支的提交"
		}
	} else {
		refs = strings.Split(anchor, ",")
		if len(refs) > 512 {
			return out, errors.New("提交快照超出范围")
		}
		for _, sha := range refs {
			if !timelineSHA.MatchString(sha) {
				return out, errors.New("提交快照格式不合法")
			}
		}
	}
	if len(refs) == 0 {
		out.Warning = "已同步仓库没有可读取的分支"
		return out, nil
	}
	out.Anchor = strings.Join(refs, ",")
	args := []string{"log", "--date-order", "--no-show-signature", "--no-decorate", "--since-as-filter=@" + strconv.FormatInt(from/1000, 10), "--until=@" + strconv.FormatInt((until-1)/1000, 10), "--max-count=" + strconv.Itoa(limit+1), "--skip=" + strconv.Itoa(offset), "--format=%H%x00%ct%x00%an%x00%s"}
	args = append(args, refs...)
	args = append(args, "--")
	raw, e := command(c, r.path(p), args...)
	if e != nil {
		return out, e
	}
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 4)
		if len(parts) != 4 || !timelineSHA.MatchString(parts[0]) {
			return out, errors.New("提交记录读取不完整，请缩小范围")
		}
		seconds, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil {
			return out, e
		}
		out.Commits = append(out.Commits, domain.GitCommit{SHA: parts[0], At: seconds * 1000, Author: parts[2], Title: parts[3], TaskIDs: []string{}})
	}
	if len(out.Commits) > limit {
		out.Commits = out.Commits[:limit]
		out.HasMore = true
	}
	out.NextOffset = offset + len(out.Commits)
	return out, nil
}
