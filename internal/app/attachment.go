package app

import (
	"ai-pm-system/internal/attachment"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
)

func (s *Service) SaveAttachment(c context.Context, a domain.Actor, project, name string, content []byte) (domain.Attachment, error) {
	if err := s.CheckProject(c, a, project, false); err != nil {
		return domain.Attachment{}, err
	}
	if _, err := s.Store.GetProject(c, project); err != nil {
		return domain.Attachment{}, err
	}
	v, err := attachment.Prepare(project, name, content)
	if err != nil {
		return v, err
	}
	if err = s.Store.PutAttachment(c, v); err != nil {
		return v, err
	}
	v.Content = nil
	v.Text = ""
	return v, nil
}

// Load metadata from storage instead of trusting client-supplied names or paths.
func (s *Service) taskAttachments(c context.Context, project string, ids []string) ([]domain.Attachment, error) {
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
		a, err := s.Store.GetAttachment(c, id)
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
