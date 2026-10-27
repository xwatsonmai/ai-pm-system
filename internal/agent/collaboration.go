package agent

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"log"
	"os"
	"time"
)

// One platform process owns the worker, like the existing run manager. Jobs are
// durable; RunID is stable across crashes and explicit/bounded automatic retries.
func (m *Manager) StartReplyWorker() {
	m.replyOnce.Do(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.ctx.Err() != nil {
			return
		}
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				if err := m.dispatchReplies(); err != nil && m.ctx.Err() == nil {
					log.Printf("协同续答调度失败: %v", err)
				}
				select {
				case <-m.ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

func (m *Manager) replySource(ctx context.Context, id string) (domain.Collaboration, error) {
	v, err := m.App.Store.GetCollaboration(ctx, id)
	if err != nil {
		return v, err
	}
	if v.Status != "completed" || v.Result == nil {
		return v, domain.ErrForbidden
	}
	origin, err := m.App.Store.GetRun(ctx, v.OriginRunID)
	if err != nil {
		return v, err
	}
	p, err := m.App.Store.GetProject(ctx, v.ProjectID)
	if err != nil {
		return v, err
	}
	a, err := m.App.CollaborationOwner(ctx, v.OwnerID, v.OwnerKind, v.ProjectID)
	if err != nil {
		return v, err
	}
	if !app.CanReadRun(a, p, origin) {
		return v, domain.ErrForbidden
	}
	return v, nil
}

func (m *Manager) dispatchReplies() error {
	jobs, err := m.App.Store.ListReplyJobs(m.ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if (job.Status != "pending" && job.Status != "running") || job.NextAt > domain.Now() {
			continue
		}
		if err := m.admitReply(job); err != nil && !errors.Is(err, domain.ErrConflict) {
			return err
		}
	}
	return nil
}

func (m *Manager) admitReply(job domain.ReplyJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return m.ctx.Err()
	}
	if _, active := m.active[job.RunID]; active {
		return nil
	}
	v, err := m.replySource(m.ctx, job.CollaborationID)
	if err != nil {
		if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) {
			return m.updateReplyJob(job, "skipped", "协同已取消、会话已删除或访问权限已失效", nil)
		}
		return err
	}
	// A crash after saving the answer but before acknowledging the job must not
	// execute the model a second time.
	previous, err := m.App.Store.GetRun(m.ctx, job.RunID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if err == nil && previous.Status == "succeeded" {
		return m.updateReplyJob(job, "delivered", "", nil)
	}
	if job.Attempts >= 3 {
		return m.updateReplyJob(job, "failed", "补充回答重试达到上限，调查结果已保存，可手动重试", nil)
	}
	runs, err := m.App.Store.ListRuns(m.ctx, v.ProjectID)
	if err != nil {
		return err
	}
	for _, r := range runs {
		// Recover an orphaned run after a failed acknowledgement/save in this
		// process, as well as after a restart. No live execution owns it here.
		if r.ID == job.RunID {
			continue
		}
		conv := r.ConversationID
		if conv == "" {
			conv = r.ID
		}
		if conv == v.ConversationID && (r.Status == "running" || r.Status == "queued") {
			return nil
		}
	}
	settings, err := m.App.Store.GetPlatformSettings(m.ctx)
	if err != nil {
		return err
	}
	profile, err := m.App.Store.GetProfile(m.ctx, settings.ProfileID)
	if err == nil {
		_, err = os.Stat(m.Worker)
	}
	if err != nil {
		return m.updateReplyJob(job, "failed", "续答模型或 Pi worker 不可用，修复配置后可重试", nil)
	}
	r := domain.Run{ID: job.RunID, ProjectID: v.ProjectID, TaskID: v.TaskID, ConversationID: v.ConversationID, OwnerID: v.OwnerID, OwnerKind: v.OwnerKind, Kind: "question", TriggerType: "collaboration_result", SourceEventID: v.Result.ID, CollaborationID: v.ID, Status: "queued", Steps: []domain.Step{}, StartedAt: domain.Now()}
	project, err := m.App.Store.GetProject(m.ctx, v.ProjectID)
	if err != nil {
		return err
	}
	if project.AgentReadMode == "sandbox" {
		if m.SandboxSocket == "" {
			return m.updateReplyJob(job, "failed", "项目启用了隔离阅读，但 runner 未配置", nil)
		}
		r.CodeSnapshot = &domain.CodeSnapshot{Status: "preparing"}
	}
	if previous.StartedAt != 0 {
		r.StartedAt = previous.StartedAt
	}
	// A timed-out reply can now take the bounded failure retry path. Keep its
	// visible partial work and advance the version when reusing the same run ID.
	r.ProcessOutput = previous.ProcessOutput
	appendRunProcess(&r, previous.Output)
	r.OutputVersion = previous.OutputVersion + 1
	job.Attempts++
	if err = m.updateReplyJob(job, "running", "", &r); err != nil {
		return err
	}
	job.Version++
	job.Status = "running"
	ctx, cancel := m.runContext()
	m.active[r.ID] = activeRun{stop: cancel}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.execute(ctx, r, profile)
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.active, r.ID)
		if err := m.finishReply(job); err != nil {
			log.Printf("协同结果已保存，回执待恢复: %v", err)
		}
	}()
	return nil
}

func (m *Manager) updateReplyJob(job domain.ReplyJob, status, message string, r *domain.Run) error {
	old := job.Version
	job.Version++
	job.Status, job.Error = status, message
	return m.App.Store.CommitReplyJob(context.Background(), job, old, r)
}

func (m *Manager) finishReply(job domain.ReplyJob) error {
	if _, err := m.replySource(context.Background(), job.CollaborationID); err != nil {
		if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) {
			return m.updateReplyJob(job, "skipped", "协同已取消、会话已删除或访问权限已失效", nil)
		}
		return err
	}
	r, err := m.App.Store.GetRun(context.Background(), job.RunID)
	if err != nil {
		return err
	}
	if r.Status == "succeeded" {
		return m.updateReplyJob(job, "delivered", "", nil)
	}
	if m.ctx.Err() != nil {
		job.NextAt = domain.Now()
		return m.updateReplyJob(job, "pending", "服务中断，等待恢复", nil)
	}
	if r.Status == "stopped" || job.Attempts >= 3 {
		return m.updateReplyJob(job, "failed", "补充回答未完成，可手动重试；调查结果已保存", nil)
	}
	job.NextAt = domain.Now() + int64(job.Attempts*5)*1000
	return m.updateReplyJob(job, "pending", "补充回答暂未完成，等待自动重试", nil)
}

// Keep streaming updates from publishing to a canceled/deleted conversation.
func (m *Manager) saveRun(r domain.Run) error {
	if r.TriggerType == "collaboration_result" {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, err := m.replySource(context.Background(), r.CollaborationID); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil
			}
			if !errors.Is(err, domain.ErrForbidden) {
				return err
			}
			r.Status, r.Output, r.Error = "stopped", "", "协同已取消或访问权限已失效"
			r.ProcessOutput = ""
		}
	}
	if err := m.App.Store.PutRun(context.Background(), r); err != nil {
		return err
	}
	m.publishRun(r)
	return nil
}

func (m *Manager) CancelCollaboration(ctx context.Context, a domain.Actor, id, requestID string) (domain.CollaborationMutation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancelCollaboration(ctx, a, id, requestID)
}

// Caller holds m.mu so cancellation and automatic reply admission are serialized.
func (m *Manager) cancelCollaboration(ctx context.Context, a domain.Actor, id, requestID string) (domain.CollaborationMutation, error) {
	v, err := m.App.ActCollaboration(ctx, a, id, app.CollaborationAction{Action: "cancel", RequestID: requestID})
	if err == nil && v.Collaboration.Result != nil {
		job, e := m.App.Store.GetReplyJob(ctx, v.Collaboration.Result.ID)
		if e == nil {
			if active, ok := m.active[job.RunID]; ok {
				active.stop()
			}
		}
	}
	return v, err
}

func (m *Manager) RetryCollaboration(ctx context.Context, a domain.Actor, id string) (domain.ReplyJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.App.GetCollaboration(ctx, a, id)
	if err != nil {
		return domain.ReplyJob{}, err
	}
	if a.ID != v.OwnerID || a.Kind != v.OwnerKind || v.Status != "completed" {
		return domain.ReplyJob{}, domain.ErrForbidden
	}
	if v.Reply == nil {
		return domain.ReplyJob{}, domain.ErrNotFound
	}
	job := *v.Reply
	if job.Status == "pending" || job.Status == "running" || job.Status == "delivered" {
		return job, nil
	}
	if job.Status != "failed" {
		return job, domain.ErrConflict
	}
	job.Attempts, job.NextAt = 0, domain.Now()
	err = m.updateReplyJob(job, "pending", "", nil)
	if err != nil {
		return job, err
	}
	return m.App.Store.GetReplyJob(ctx, job.ID)
}
