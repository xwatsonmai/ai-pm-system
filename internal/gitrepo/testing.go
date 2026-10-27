package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ValidateTestingCode requires an exact, reachable commit on the requirement's
// development branch. Every verified fix must be contained in the retest build.
func (r *Repositories) ValidateTestingCode(c context.Context, project, branch, sha string, fixes []string) error {
	if strings.TrimSpace(branch) == "" || (len(sha) != 40 && len(sha) != 64) {
		return errors.New("请登记开发分支和完整提交 SHA")
	}
	for _, ch := range sha {
		if !strings.ContainsRune("0123456789abcdefABCDEF", ch) {
			return errors.New("代码版本必须是完整提交 SHA")
		}
	}
	head, err := r.resolve(c, project, branch)
	if err != nil {
		return fmt.Errorf("开发分支不可读取，请先推送并同步仓库：%w", err)
	}
	commit, err := r.resolve(c, project, sha)
	if err != nil {
		return fmt.Errorf("提测版本不可读取，请先推送并同步仓库：%w", err)
	}
	if _, err = command(c, r.path(project), "merge-base", "--is-ancestor", commit, head); err != nil {
		return errors.New("提交不属于需求开发分支，请核对并同步仓库")
	}
	for _, fix := range fixes {
		fixed, err := r.resolve(c, project, fix)
		if err != nil {
			return errors.New("Bug 修复版本不可读取，请同步仓库")
		}
		if _, err = command(c, r.path(project), "merge-base", "--is-ancestor", fixed, commit); err != nil {
			return errors.New("复测版本尚未包含所有已验证的 Bug 修复提交，请先合入需求分支")
		}
	}
	return nil
}
