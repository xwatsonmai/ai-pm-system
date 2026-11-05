package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"sort"
	"strconv"
	"strings"
)

// Project committed events into the existing room, including historical events.
// Reading the room never manufactures workflow writes.
func (s *Service) requirementTimeline(c context.Context, t domain.Task, messages []domain.RequirementReviewMessage) ([]domain.RequirementReviewMessage, error) {
	verbs := map[string]string{
		"claim": "认领了开发", "release": "交接了开发", "submit": "提交了开发结果",
		"progress": "更新了开发进展", "decision": "记录了决定", "blocked": "记录了阻塞", "unblocked": "解除了阻塞",
		"test_pass": "确认测试通过", "start_retest": "开始复测", "verify_bug": "确认 Bug 修复有效", "return_bug": "退回 Bug 修复",
		"mark_deployed": "登记了实际上线", "accept": "确认业务验收通过", "reject": "退回了业务验收",
		"rework": "退回开发", "withdraw_submission": "撤回了提测", "bug_progress": "关联 Bug 进展",
	}
	out := append([]domain.RequirementReviewMessage{}, messages...)
	seen := map[string]bool{}
	for _, m := range out {
		seen[m.ID] = true
	}
	var tasks []domain.Task
	var after int64
	for {
		events, err := s.Store.Events(c, t.ProjectID, t.ID, after)
		if err != nil {
			return nil, err
		}
		for _, e := range events {
			if m, ok := domain.CreatedRequirementProductMention(e); ok && !seen[m.ID] {
				out = append(out, m)
				seen[m.ID] = true
			}
			verb, ok := verbs[e.Kind]
			id := "event_" + strconv.FormatInt(e.Seq, 10)
			if !ok || seen[id] {
				continue
			}
			body := e.Body
			actor := e.Actor
			if actor == "system" {
				actor = "系统"
			}
			if e.Kind == "claim" && body == "领取任务" {
				body = ""
			}
			if e.Kind == "submit" {
				body = strings.TrimPrefix(body, "开发已完成，等待测试：")
			}
			if e.Kind == "bug_progress" {
				if tasks == nil {
					tasks, err = s.Store.ListTasks(c, t.ProjectID)
					if err != nil {
						return nil, err
					}
				}
				for _, task := range tasks {
					body = strings.ReplaceAll(body, task.ID, "「"+task.Title+"」")
				}
				for status, label := range map[string]string{"clarify": "待评审", "ready": "待认领", "doing": "处理中", "verifying": "待验证", "resolved": "已解决", "closed": "已关闭", "rejected": "已拒绝", "canceled": "已取消"} {
					body = strings.ReplaceAll(body, "状态 "+status, "状态为"+label)
				}
			}
			text := actor + " " + verb
			if body != "" {
				text += "：" + body
			}
			out = append(out, domain.RequirementReviewMessage{ID: id, Role: "system", Action: "task_" + e.Kind, Actor: e.Actor, Body: text, At: e.At})
			seen[id] = true
		}
		if len(events) < 500 {
			break
		}
		after = events[len(events)-1].Seq
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out, nil
}
