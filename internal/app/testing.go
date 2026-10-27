package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
)

func (s *Service) applyTestingAction(c context.Context, t *domain.Task, in Action, a domain.Actor) error {
	if in.Version != t.Version {
		return domain.ErrConflict
	}
	if in.Action == "return_bug" {
		linked := t.ParentRequirementID != "" && (t.Status == "verifying" || t.Status == "resolved")
		standalone := t.ParentRequirementID == "" && t.Status == "testing"
		if t.Kind != "bug" || (!linked && !standalone) || !nonempty(in.Body) {
			return errors.New("仅测试中的平台 Bug、待验证或已解决的关联 Bug 可退回修复，请填写原因")
		}
		t.Status, t.TestVerification = "doing", nil
		return nil
	}
	v := in.TestVerification
	if v == nil || t.Submission == nil || v.Revision != t.Revision || !nonempty(v.SHA) || !nonempty(v.Environment) || !nonempty(v.Note) {
		return errors.New("请填写当前修订、实际测试版本、环境和结果说明")
	}
	if in.Action != "start_retest" && v.SHA != t.Submission.SHA {
		return domain.ErrConflict
	}
	switch in.Action {
	case "test_pass":
		if t.Status != "testing" || t.ParentRequirementID != "" {
			return errors.New("只有测试中的事项可以确认测试通过")
		}
		t.Status = "pending_release"
	case "verify_bug":
		if t.Status != "verifying" || t.ParentRequirementID == "" {
			return errors.New("只有待验证的关联 Bug 可以确认解决")
		}
		t.Status = "resolved"
	case "start_retest":
		if t.Status != "retest" || t.Kind != "requirement" {
			return errors.New("全部关联 Bug 解决或关闭后，才能开始复测")
		}
		tasks, err := s.Store.ListTasks(c, t.ProjectID)
		if err != nil {
			return err
		}
		required := []string{}
		for _, bug := range tasks {
			if bug.ParentRequirementID != t.ID {
				continue
			}
			if !domain.TaskFinished(bug.Status) {
				return errors.New("仍有未解决或未关闭的 Bug")
			}
			if bug.Status == "resolved" && bug.Submission != nil {
				required = append(required, bug.Submission.SHA)
			}
		}
		if err := s.validateTestingVersion(c, *t, v.SHA, required); err != nil {
			return err
		}
		t.Submission = &domain.Submission{Summary: "产品开始复测：" + v.Note, SHA: v.SHA, Branch: t.Branch, Revision: t.Revision, Environment: v.Environment, At: domain.Now(), Tests: "本版本尚待产品复测；先前版本的开发自测结果保留在历史记录中。"}
		t.Status = "testing"
		t.TestVerification = nil
		return nil
	default:
		return errors.New("不支持的测试操作")
	}
	verified := *v
	verified.Actor, verified.At = a.Name, domain.Now()
	t.TestVerification = &verified
	return nil
}

func (s *Service) validateTestingVersion(c context.Context, t domain.Task, sha string, required []string) error {
	if !nonempty(t.Branch) {
		return errors.New("需求未登记开发分支，请先由开发方补全")
	}
	if s.ValidateTestingCode == nil {
		return errors.New("代码版本校验服务不可用，请稍后重试")
	}
	return s.ValidateTestingCode(c, t.ProjectID, t.Branch, sha, required)
}
