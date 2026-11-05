package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf16"
)

// The caller holds Service.mu, so creation and changes to project access use
// the same boundary. Only the trusted creator identity decides product status.
func (s *Service) addCreationProductMention(c context.Context, t domain.Task, event *domain.Event) error {
	if t.Kind != "requirement" || t.Creator == nil || t.Creator.ID == "" {
		return nil
	}
	p, err := s.Store.GetProject(c, t.ProjectID)
	if err != nil {
		return err
	}
	p = domain.NormalizeProjectAccess(p)
	for _, member := range p.Memberships {
		if member.UserID == t.Creator.ID && member.ResponsibleFor("product") {
			return nil
		}
	}
	ids, seen := []string{}, map[string]bool{}
	for _, member := range p.Memberships {
		if member.UserID != t.Creator.ID && !seen[member.UserID] && member.ResponsibleFor("product") && domain.MemberHasProjectPage(p, member.UserID, "tasks") {
			seen[member.UserID] = true
			ids = append(ids, member.UserID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	users, err := s.Store.ListUsers(c)
	if err != nil {
		return err
	}
	names := map[string]string{"local-owner": "本地管理员"}
	for _, user := range users {
		names[user.ID] = user.Name
	}
	creator := strings.TrimSpace(t.Creator.Name)
	if creator == "" {
		creator = "非产品成员"
	}
	m := domain.RequirementReviewMessage{ID: domain.RequirementProductMentionID(t.ID), Role: "system", Actor: "系统", ActorID: "system", ActorKind: "platform", Action: domain.RequirementProductMentionAction, At: event.At,
		Body: creator + " 提交了需求，请产品同事协助评审与确认："}
	for _, id := range ids {
		account, err := s.Store.GetAccount(c, id)
		if err == nil && account.Disabled {
			continue
		}
		// External-login users have no built-in account row.
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		name := strings.TrimSpace(names[id])
		if name == "" {
			name = id
		}
		m.Body += " "
		start := len(utf16.Encode([]rune(m.Body)))
		m.Body += "@" + name
		m.Mentions = append(m.Mentions, domain.ReviewMention{UserID: id, Name: name, Start: start, End: len(utf16.Encode([]rune(m.Body)))})
	}
	if len(m.Mentions) == 0 {
		return nil
	}
	data := map[string]json.RawMessage{}
	if len(event.Data) != 0 && string(event.Data) != "null" {
		if err = json.Unmarshal(event.Data, &data); err != nil {
			return err
		}
	}
	if data == nil {
		data = map[string]json.RawMessage{}
	}
	data["product_mention"], err = json.Marshal(m)
	if err == nil {
		event.Data, err = json.Marshal(data)
	}
	return err
}
