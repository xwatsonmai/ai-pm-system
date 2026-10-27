package app

import (
	"ai-pm-system/internal/attachment"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"fmt"
	"strings"
)

type ProductAuthorization struct {
	UserConfirmed   bool   `json:"user_confirmed"`
	UserInstruction string `json:"user_instruction"`
}

func productAuthorization(a domain.Actor, in ProductAuthorization) (*domain.TaskActionAuthorization, error) {
	if a.Kind != "developer" || a.UserID == "" || a.ID != a.UserID || !in.UserConfirmed || strings.TrimSpace(in.UserInstruction) == "" {
		return nil, fmt.Errorf("%w: 产品管理操作需绑定员工的个人OAuth、用户明确要求及原话", domain.ErrForbidden)
	}
	return &domain.TaskActionAuthorization{Source: "mcp", UserID: a.UserID, ActorID: a.ID, UserConfirmed: true, UserInstruction: strings.TrimSpace(in.UserInstruction)}, nil
}
func authorizedFingerprint(input any, auth *domain.TaskActionAuthorization) string {
	if auth == nil {
		return fingerprint(input)
	}
	return fingerprint(struct {
		Input         any
		Authorization *domain.TaskActionAuthorization
	}{input, auth})
}
func authorizedData(input any, auth *domain.TaskActionAuthorization) any {
	if auth == nil {
		return input
	}
	return struct {
		Input         any                             `json:"input"`
		Authorization *domain.TaskActionAuthorization `json:"authorization"`
	}{input, auth}
}
func (s *Service) CreateRequirementFromMCP(c context.Context, a domain.Actor, in TaskInput, grant ProductAuthorization) (domain.Mutation, error) {
	auth, e := productAuthorization(a, grant)
	if e != nil {
		return domain.Mutation{}, e
	}
	return s.createTask(c, a, a.ProjectID, in, auth)
}
func (s *Service) SaveProjectVersionFromMCP(c context.Context, a domain.Actor, id string, in ProjectVersionInput, grant ProductAuthorization) (ProjectVersionMutation, error) {
	auth, e := productAuthorization(a, grant)
	if e != nil {
		return ProjectVersionMutation{}, e
	}
	return s.saveProjectVersion(c, a, a.ProjectID, id, in, auth)
}
func (s *Service) ManageProjectVersionFromMCP(c context.Context, a domain.Actor, id string, in ProjectVersionAction, grant ProductAuthorization) (ProjectVersionMutation, error) {
	auth, e := productAuthorization(a, grant)
	if e != nil {
		return ProjectVersionMutation{}, e
	}
	if in.Action != "complete" && in.Action != "close" && in.Action != "reopen" && in.Action != "start" {
		return ProjectVersionMutation{}, domain.ErrForbidden
	}
	if strings.TrimSpace(in.Body) == "" {
		return ProjectVersionMutation{}, errors.New("请提供用户给出的结论或原因")
	}
	return s.actProjectVersion(c, a, id, in, auth)
}
func (s *Service) SaveReviewFromMCP(c context.Context, a domain.Actor, id string, in RequirementReviewInput, grant ProductAuthorization) (domain.RequirementReview, error) {
	auth, e := productAuthorization(a, grant)
	if e != nil {
		return domain.RequirementReview{}, e
	}
	if in.Action != "message" && in.Action != "retry" && in.Action != "confirm" {
		return domain.RequirementReview{}, domain.ErrForbidden
	}
	if in.ExpectedTaskVersion <= 0 {
		return domain.RequirementReview{}, errors.New("请先读取需求和评审最新version")
	}
	var v domain.RequirementReview
	for attempt := 0; attempt < 3; attempt++ {
		v, e = s.saveRequirementReview(c, a, id, in, auth)
		if !errors.Is(e, domain.ErrConflict) {
			break
		}
	}
	return v, e
}

type RequirementPatch struct {
	Title         *string   `json:"title,omitempty"`
	Kind          *string   `json:"kind,omitempty"`
	Description   *string   `json:"description,omitempty"`
	Acceptance    *[]string `json:"acceptance,omitempty"`
	Priority      *string   `json:"priority,omitempty"`
	Reviewer      *string   `json:"reviewer,omitempty"`
	AttachmentIDs *[]string `json:"attachment_ids,omitempty"`
	Version       int       `json:"version"`
	RequestID     string    `json:"-"`
}

func (s *Service) PatchRequirementFromMCP(c context.Context, a domain.Actor, id string, in RequirementPatch, grant ProductAuthorization) (domain.Mutation, error) {
	auth, e := productAuthorization(a, grant)
	if e != nil {
		return domain.Mutation{}, e
	}
	t, e := s.Store.GetTask(c, id)
	if e != nil {
		return domain.Mutation{}, e
	}
	if e = s.CheckProject(c, a, t.ProjectID, false); e != nil {
		return domain.Mutation{}, e
	}
	if in.RequestID == "" || in.Version <= 0 {
		return domain.Mutation{}, errors.New("必须提供request_id和最新需求version")
	}
	fp := authorizedFingerprint(struct {
		ID        string
		Patch     RequirementPatch
		RequestID string
	}{id, in, in.RequestID}, auth)
	if old, ok, e := s.prior(c, receiptKey(a, t.ProjectID, in.RequestID), fp); ok || e != nil {
		return old, e
	}
	input := TaskInput{Title: t.Title, Kind: t.Kind, Description: t.Description, Acceptance: t.Acceptance, Priority: t.Priority, Reviewer: t.Reviewer, Version: in.Version, RequestID: in.RequestID}
	if in.Title != nil {
		input.Title = *in.Title
	}
	if in.Kind != nil {
		input.Kind = *in.Kind
	}
	if in.Description != nil {
		input.Description = *in.Description
	}
	if in.Acceptance != nil {
		input.Acceptance = *in.Acceptance
	}
	if in.Priority != nil {
		input.Priority = *in.Priority
	}
	if in.Reviewer != nil {
		input.Reviewer = *in.Reviewer
	}
	if in.AttachmentIDs != nil {
		input.AttachmentIDs = *in.AttachmentIDs
	}
	return s.editTask(c, a, id, input, auth, fp)
}

func (s *Service) UploadProductAttachment(c context.Context, a domain.Actor, name, request string, content []byte, grant ProductAuthorization) (domain.Attachment, error) {
	auth, e := productAuthorization(a, grant)
	if e != nil {
		return domain.Attachment{}, e
	}
	if e = s.CheckProject(c, a, a.ProjectID, false); e != nil {
		return domain.Attachment{}, e
	}
	if request == "" {
		return domain.Attachment{}, errors.New("缺少request_id")
	}
	item, e := attachment.Prepare(a.ProjectID, name, content)
	if e != nil {
		return item, e
	}
	item.ID = "att_" + Hash("product-upload:" + receiptKey(a, a.ProjectID, request))[:24]
	item.Authorization = auth
	item.UploadFingerprint = authorizedFingerprint([]string{name, Hash(string(content))}, auth)
	existing, e := s.Store.GetAttachment(c, item.ID)
	if errors.Is(e, domain.ErrNotFound) {
		e = s.Store.PutAttachment(c, item)
		if e == nil {
			existing = item
		} else {
			existing, e = s.Store.GetAttachment(c, item.ID)
		}
	}
	if e != nil {
		return domain.Attachment{}, e
	}
	if existing.UploadFingerprint != item.UploadFingerprint {
		return domain.Attachment{}, domain.ErrConflict
	}
	existing.Content = nil
	existing.Text = ""
	return existing, nil
}
