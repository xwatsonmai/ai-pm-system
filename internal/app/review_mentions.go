package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"unicode/utf16"
)

func (s *Service) validateReviewMentions(c context.Context, project string, in RequirementReviewInput) error {
	if len(in.Mentions) == 0 {
		return nil
	}
	if (in.Action != "message" && in.Action != "request_change") || len(in.Mentions) > 100 {
		return errors.New("只能在评审消息中提及成员，单条最多 100 处")
	}
	p, err := s.Store.GetProject(c, project)
	if err != nil {
		return err
	}
	users, err := s.Store.ListUsers(c)
	if err != nil {
		return err
	}
	names := map[string]string{"local-owner": "本地管理员"}
	for _, u := range users {
		names[u.ID] = u.Name
	}
	members := map[string]bool{}
	for _, m := range domain.ProjectMembers(p) {
		members[m.UserID] = true
	}
	body, end := utf16.Encode([]rune(in.Body)), 0
	for _, m := range in.Mentions {
		if !members[m.UserID] || m.Name == "" || names[m.UserID] != m.Name {
			return errors.New("被 @ 的成员已变更，请重新选择当前项目成员；草稿已保留")
		}
		if m.Start < end || m.End <= m.Start || m.End > len(body) || string(utf16.Decode(body[m.Start:m.End])) != "@"+m.Name {
			return errors.New("@ 提及内容已变化，请重新选择成员；草稿已保留")
		}
		end = m.End
	}
	return nil
}
