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
	missingFromSelection := false
	for _, fix := range fixes {
		fixed, err := r.resolve(c, project, fix)
		if err != nil {
			return errors.New("Bug 修复版本不可读取，请同步仓库")
		}
		contained, err := r.testingContains(c, project, fixed, commit)
		if err != nil {
			return err
		}
		if !contained {
			merged, err := r.testingContains(c, project, fixed, head)
			if err != nil {
				return err
			}
			if !merged {
				return errors.New("需求分支尚未包含全部已验证的 Bug 修复提交，请先合入修复并同步仓库")
			}
			missingFromSelection = true
		}
	}
	if missingFromSelection {
		return fmt.Errorf("当前选择的复测版本未包含全部已验证修复；修复已在需求分支中，请更新复测 SHA（当前分支最新提交：%s）", head)
	}
	return nil
}

// TestingCandidate reads the synchronized requirement branch, never the project
// default branch or the previous submission. It does not attest deployment.
func (r *Repositories) TestingCandidate(c context.Context, project, branch string, fixes []string) (string, error) {
	if strings.TrimSpace(branch) == "" {
		return "", errors.New("需求未登记开发分支，请先由开发方补全")
	}
	head, err := r.resolve(c, project, branch)
	if err != nil {
		return "", fmt.Errorf("开发分支不可读取，请先推送并同步仓库：%w", err)
	}
	if err := r.ValidateTestingCode(c, project, branch, head, fixes); err != nil {
		return "", err
	}
	return head, nil
}

func (r *Repositories) testingContains(c context.Context, project, ancestor, descendant string) (bool, error) {
	// A bounded traversal distinguishes a missing ancestor from a Git failure,
	// including unrelated histories, without treating every command error as an
	// unmerged fix.
	out, err := command(c, r.path(project), "rev-list", "--max-count=1", ancestor, "--not", descendant, "--")
	if err != nil {
		return false, fmt.Errorf("无法校验修复版本包含关系：%w", err)
	}
	return strings.TrimSpace(out) == "", nil
}
