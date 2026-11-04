package app

import (
	"ai-pm-system/internal/attachment"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
)

func (s *Service) SaveAttachment(c context.Context, a domain.Actor, project, name string, content []byte) (domain.Attachment, error) {
	if err := s.RequirePages(c, a, project, "tasks", "reviews", "chat"); err != nil {
		return domain.Attachment{}, err
	}
	if _, err := s.Store.GetProject(c, project); err != nil {
		return domain.Attachment{}, err
	}
	v, err := attachment.Prepare(project, name, content)
	if err != nil {
		return v, err
	}
	v.UploaderID, v.UploaderKind = attachmentUploader(a)
	if err = s.Store.PutAttachment(c, v); err != nil {
		return v, err
	}
	v.Content = nil
	v.Text = ""
	return v, nil
}

// Load metadata from storage instead of trusting client-supplied names or paths.
func (s *Service) taskAttachments(c context.Context, actor domain.Actor, project string, ids []string) ([]domain.Attachment, error) {
	if len(ids) > attachment.MaxCount {
		return nil, errors.New("每个需求最多添加 6 个附件")
	}
	var result []domain.Attachment
	seen, total := map[string]bool{}, 0
	for _, id := range ids {
		if seen[id] {
			return nil, errors.New("需求中存在重复附件")
		}
		seen[id] = true
		a, err := s.AttachmentAs(c, actor, project, id)
		if err != nil {
			return nil, err
		}
		if a.ProjectID != project {
			return nil, domain.ErrForbidden
		}
		total += a.Size
		if total > attachment.MaxTotalSize {
			return nil, errors.New("需求附件总大小不能超过 20 MB")
		}
		a.Content, a.Text = nil, ""
		result = append(result, a)
	}
	return result, nil
}

func attachmentUploader(a domain.Actor) (string, string) {
	if a.Kind == "developer" && a.UserID != "" {
		return a.UserID, "human"
	}
	return a.ID, a.Kind
}

// AttachmentAs applies the referenced object's current access before returning
// bytes or allowing an ID to be attached elsewhere. Knowing a URL is no grant.
func (s *Service) AttachmentAs(c context.Context, a domain.Actor, project, id string) (domain.Attachment, error) {
	var empty domain.Attachment
	if e := s.RequirePages(c, a, project, "tasks", "reviews", "versions", "chat"); e != nil {
		return empty, e
	}
	f, e := s.Store.GetAttachment(c, id)
	if e != nil {
		return empty, e
	}
	if f.ProjectID != project {
		return empty, domain.ErrForbidden
	}
	if a.Kind == "platform" && a.ProjectID == project {
		return f, nil
	}
	p, e := s.Store.GetProject(c, project)
	if e != nil {
		return empty, e
	}
	refs, e := s.Store.AttachmentReferences(c, project, id)
	if e != nil {
		return empty, e
	}
	for _, ref := range refs {
		switch ref.Kind {
		case "task":
			if CanAccessAnyPage(a, p, "tasks", "reviews", "versions") {
				return f, nil
			}
		case "version":
			if CanAccessPage(a, p, "versions") {
				return f, nil
			}
		case "run":
			r, err := s.Store.GetRun(c, ref.ID)
			if err == nil && CanReadRun(a, p, r) {
				return f, nil
			}
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return empty, err
			}
		case "intake":
			v, err := s.Store.GetRequirementIntake(c, ref.ID)
			if err != nil {
				return empty, err
			}
			r, err := s.Store.GetRun(c, v.OriginRunID)
			if err == nil && CanReadRun(a, p, r) {
				return f, nil
			}
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return empty, err
			}
		}
	}
	if len(refs) == 0 {
		user, kind := attachmentUploader(a)
		if f.UploaderID != "" && f.UploaderID == user && f.UploaderKind == kind {
			return f, nil
		}
		// Older personal-MCP uploads already recorded a trustworthy uploader.
		if f.UploaderID == "" && f.Authorization != nil && f.Authorization.Source == "mcp" && f.Authorization.UserID == user && kind == "human" {
			return f, nil
		}
	}
	return empty, domain.ErrForbidden
}
