package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"sync"
	"testing"
)

func versionDetail(t *testing.T, s *app.Service, id string) app.ProjectVersionDetail {
	t.Helper()
	v, e := s.ProjectVersionContext(context.Background(), app.Human(), id)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func publishVersion(t *testing.T, s *app.Service, id string, tasks ...domain.Task) app.ProjectVersionMutation {
	t.Helper()
	v := versionDetail(t, s, id).ProjectVersion
	refs := []app.VersionTaskRef{}
	for _, task := range tasks {
		refs = append(refs, app.VersionTaskRef{ID: task.ID, Version: task.Version, Revision: task.Revision, SHA: task.Submission.SHA})
	}
	m, e := s.ActProjectVersion(context.Background(), app.Human(), id, app.ProjectVersionAction{Action: "publish", Version: v.Version, Environment: "生产", Body: "实际发布成功，见发布记录", Items: refs, RequestID: domain.ID("publish_")})
	if e != nil {
		t.Fatal(e)
	}
	return m
}

func TestMilestoneAutomaticCreationAndConcurrentAppend(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	var wg sync.WaitGroup
	results := make(chan domain.Mutation, 16)
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, e := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "并发新需求", Description: "完整文档", RequestID: domain.ID("create_")})
			results <- m
			failures <- e
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	versions, e := s.ProjectVersions(c, app.Human(), p.ID)
	if e != nil || len(versions) != 1 {
		t.Fatal("orphan/parallel milestones", versions, e)
	}
	for m := range results {
		if m.Task.ProjectVersionID != versions[0].ID || m.Task.Version != 1 || m.Task.Revision != 1 {
			t.Fatal(m)
		}
	}
	v := versionDetail(t, s, versions[0].ID)
	if len(v.ProjectVersion.Scope) != 16 || v.Summary.Counts.Included != 16 {
		t.Fatal("lost scope", v)
	}
	in := app.TaskInput{Title: "重试", RequestID: "repeat"}
	first, e := s.CreateTask(c, app.Human(), p.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.CreateTask(c, app.Human(), p.ID, in)
	if e != nil || first.Task.ID != second.Task.ID {
		t.Fatal("creation duplicated", e)
	}
	state, e := s.Store.GetCurrentMilestone(c, p.ID)
	if e != nil || state.VersionID != versions[0].ID {
		t.Fatal(state, e)
	}
	if _, e = s.SaveProjectVersion(c, app.Human(), p.ID, "", app.ProjectVersionInput{Name: "第二个并行", RequestID: "parallel"}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("parallel opened", e)
	}
}
func TestMilestoneAppendAfterReleaseAndImmutableHistory(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a, _, _ := taskAtStage(t, s, p.ID, "pending_release")
	v := versionDetail(t, s, a.ProjectVersionID).ProjectVersion
	published := publishVersion(t, s, v.ID, a)
	a = published.Tasks[0]
	added := create(t, s, p.ID)
	if added.ProjectVersionID != v.ID {
		t.Fatal("published milestone rejected new requirement")
	}
	current := versionDetail(t, s, v.ID)
	if current.ProjectVersion.ScopeFrozen || len(current.ProjectVersion.Scope) != 2 {
		t.Fatal(current)
	}
	renamed, e := s.SaveProjectVersion(c, app.Human(), p.ID, v.ID, app.ProjectVersionInput{Name: "质检里程碑", Goal: "持续追加", Version: current.ProjectVersion.Version, RequestID: "rename"})
	if e != nil {
		t.Fatal(e)
	}
	edited, e := s.EditTask(c, app.Human(), a.ID, app.TaskInput{Title: a.Title, Kind: a.Kind, Description: "同一里程碑内的新修订", Acceptance: a.Acceptance, Version: a.Version, RequestID: "revise"})
	if e != nil {
		t.Fatal(e)
	}
	if edited.Task.ProjectVersionID != v.ID || edited.Task.Revision != 2 {
		t.Fatal(edited)
	}
	view := versionDetail(t, s, v.ID)
	if view.ProjectVersion.Releases[0].Tasks[0].Description != "原始需求与业务范围" || view.ProjectVersion.Releases[0].Tasks[0].Revision != 1 || renamed.ProjectVersion.Name != "质检里程碑" {
		t.Fatal("release snapshot rewritten")
	}
	current = view
	_, e = s.ActProjectVersion(c, app.Human(), v.ID, app.ProjectVersionAction{Action: "publish", Version: current.ProjectVersion.Version, Environment: "生产", Body: "不可提前上线", RequestID: "bad-publish", Items: []app.VersionTaskRef{{ID: edited.Task.ID, Version: edited.Task.Version, Revision: 2, SHA: "v1"}}})
	if !errors.Is(e, domain.ErrConflict) {
		t.Fatal("unready work published", e)
	}
}
func TestMilestoneEndCloseCarryAndReopen(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	doing, _, _ := taskAtStage(t, s, p.ID, "doing")
	pending, _, _ := taskAtStage(t, s, p.ID, "pending_release")
	published := publishVersion(t, s, doing.ProjectVersionID, pending).Tasks[0]
	v := versionDetail(t, s, doing.ProjectVersionID).ProjectVersion
	if _, e := s.ActProjectVersion(c, app.Human(), v.ID, app.ProjectVersionAction{Action: "complete", Version: v.Version, Body: "完成", RequestID: "unfinished"}); e == nil {
		t.Fatal("unfinished milestone completed")
	}
	closed, e := s.ActProjectVersion(c, app.Human(), v.ID, app.ProjectVersionAction{Action: "close", Version: v.Version, Body: "后续工作转下期", RequestID: "close"})
	if e != nil {
		t.Fatal(e)
	}
	if closed.ProjectVersion.Status != "closed" || closed.ProjectVersion.EndedAt == 0 || len(closed.Tasks) != 1 || closed.Tasks[0].ProjectVersionID == v.ID || closed.Tasks[0].ProjectVersionID == "" {
		t.Fatal(closed)
	}
	next := closed.Tasks[0].ProjectVersionID
	actual, _ := s.Store.GetTask(c, published.ID)
	if actual.ProjectVersionID != v.ID || actual.Status != "review" {
		t.Fatal("pending acceptance moved", actual)
	}
	again, e := s.ActProjectVersion(c, app.Human(), v.ID, app.ProjectVersionAction{Action: "close", Version: v.Version, Body: "后续工作转下期", RequestID: "close"})
	if e != nil || again.Tasks[0].ProjectVersionID != next {
		t.Fatal("close retry created successor twice", e)
	}
	if _, e = s.ActProjectVersion(c, app.Human(), v.ID, app.ProjectVersionAction{Action: "reopen", Version: closed.ProjectVersion.Version, Body: "恢复", RequestID: "unsafe-reopen"}); !errors.Is(e, domain.ErrConflict) && e == nil {
		t.Fatal("two active milestones")
	}
	ended, e := s.Act(c, app.Human(), doing.ID, app.Action{Action: "close", Body: "本次不做", RequestID: "end-task"})
	if e != nil {
		t.Fatal(e)
	}
	state := versionDetail(t, s, next)
	done, e := s.ActProjectVersion(c, app.Human(), next, app.ProjectVersionAction{Action: "complete", Version: state.ProjectVersion.Version, Body: "已处理全部需求", RequestID: "complete"})
	if e != nil || done.ProjectVersion.Status != "completed" {
		t.Fatal(done, e)
	}
	statePointer, _ := s.Store.GetCurrentMilestone(c, p.ID)
	if statePointer.VersionID != "" {
		t.Fatal("completion left current pointer")
	}
	reopened, e := s.Act(c, app.Human(), ended.Task.ID, app.Action{Action: "reopen", Body: "新的业务范围", RequestID: "reopen-task"})
	if e != nil {
		t.Fatal(e)
	}
	if reopened.Task.ProjectVersionID == "" || reopened.Task.ProjectVersionID == next || reopened.Task.Revision != 2 {
		t.Fatal("new work left without current milestone", reopened)
	}
	old := versionDetail(t, s, next)
	if old.ProjectVersion.Scope[0].Task.Status != "closed" || old.ProjectVersion.Scope[0].Task.Revision != 1 {
		t.Fatal("historic resolution overwritten")
	}
}
func TestMilestoneCASAndProductAuthority(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := domain.Actor{ID: "local-owner", UserID: "local-owner", Kind: "developer", ProjectID: p.ID, Name: "产品 · MCP"}
	grant := app.ProductAuthorization{UserConfirmed: true, UserInstruction: "创建质检优化需求"}
	in := app.TaskInput{Title: "质检优化", Kind: "bug", Description: "完整图文文档", Acceptance: []string{"业务正确"}, RequestID: "create"}
	if _, e := s.CreateTask(c, a, p.ID, in); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("ordinary developer gained product writes", e)
	}
	if _, e := s.CreateRequirementFromMCP(c, a, in, app.ProductAuthorization{}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal(e)
	}
	m, e := s.CreateRequirementFromMCP(c, a, in, grant)
	if e != nil {
		t.Fatal(e)
	}
	if m.Task.Status != "clarify" || m.Task.ProjectVersionID == "" {
		t.Fatal(m)
	}
	title := "仅更新标题"
	patch := app.RequirementPatch{Title: &title, Version: m.Task.Version, RequestID: "patch"}
	updated, e := s.PatchRequirementFromMCP(c, a, m.Task.ID, patch, grant)
	if e != nil {
		t.Fatal(e)
	}
	if updated.Task.Description != in.Description || updated.Task.Kind != "bug" || len(updated.Task.Acceptance) != 1 {
		t.Fatal("partial edit dropped content", updated)
	}
	replay, e := s.PatchRequirementFromMCP(c, a, m.Task.ID, patch, grant)
	if e != nil || replay.Task.Version != updated.Task.Version {
		t.Fatal("patch replay", e)
	}
	if _, e = s.PatchRequirementFromMCP(c, a, m.Task.ID, app.RequirementPatch{Title: &title, Version: m.Task.Version, RequestID: "stale"}, grant); !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	version := versionDetail(t, s, m.Task.ProjectVersionID)
	if _, e = s.AssignProjectVersion(c, app.Human(), p.ID, app.VersionAssignmentInput{Tasks: []app.VersionTaskRef{{ID: m.Task.ID, Version: updated.Task.Version}}, Body: "移回未排期", RequestID: "unplanned"}); e == nil {
		t.Fatal("mandatory association bypassed")
	}
	if _, e = s.ManageProjectVersionFromMCP(c, a, version.ProjectVersion.ID, app.ProjectVersionAction{Action: "publish", Version: version.ProjectVersion.Version, RequestID: "no-publish"}, grant); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("product tool gained deployment", e)
	}
	foreign, _ := s.SaveProject(c, domain.Project{Name: "foreign"})
	if _, e = s.ProjectVersions(c, a, foreign.ID); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal(e)
	}
	changes, _ := s.Store.Events(c, p.ID, m.Task.ID, 0)
	if changes[0].Actor != a.Name {
		t.Fatal("product actor replaced")
	}
}
func TestMilestoneReviewConfirmationAndConcurrency(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	task, _, _ := taskAtStage(t, s, p.ID, "clarify")
	v, e := s.SaveRequirementReview(c, app.Human(), task.ID, app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.BeginRequirementReview(c, task.ID, v.Generation, "run"); e != nil {
		t.Fatal(e)
	}
	result := &domain.RequirementReviewResult{Body: "已明确", Decision: "ready", Proposal: &domain.RequirementProposal{Description: "正式需求稿", Acceptance: []string{"可以验证"}}}
	if e = s.FinishRequirementReview(c, task.ID, v.Generation, "run", result, ""); e != nil {
		t.Fatal(e)
	}
	a := domain.Actor{ID: "local-owner", UserID: "local-owner", Kind: "developer", ProjectID: p.ID, Name: "产品 · MCP"}
	grant := app.ProductAuthorization{UserConfirmed: true, UserInstruction: "确认这份需求稿"}
	confirmed, e := s.SaveReviewFromMCP(c, a, task.ID, app.RequirementReviewInput{Action: "confirm", ExpectedTaskVersion: task.Version, Generation: v.Generation, RequestID: "confirm"}, grant)
	if e != nil || confirmed.Status != "confirmed" {
		t.Fatal(e, confirmed)
	}
	current, _ := s.Store.GetTask(c, task.ID)
	view := versionDetail(t, s, task.ProjectVersionID)
	if current.ProjectVersionID != task.ProjectVersionID || view.Summary.Counts.ByStatus["ready"] != 1 || view.ProjectVersion.Scope[0].Task.Description != "正式需求稿" {
		t.Fatal("scope stale after confirmation")
	}
	if confirmed.Messages[len(confirmed.Messages)-1].Authorization.UserID != a.UserID {
		t.Fatal("confirmation audit missing")
	}
}

func TestMilestonePublicationIsSelectedAndAtomic(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a, _, _ := taskAtStage(t, s, p.ID, "pending_release")
	b, _, _ := taskAtStage(t, s, p.ID, "pending_release")
	unfinished, _, _ := taskAtStage(t, s, p.ID, "doing")
	v := versionDetail(t, s, a.ProjectVersionID).ProjectVersion
	_, e := s.ActProjectVersion(c, app.Human(), v.ID, app.ProjectVersionAction{Action: "publish", Version: v.Version, Body: "无效清单", Environment: "生产", RequestID: "invalid-batch", Items: []app.VersionTaskRef{{ID: a.ID, Version: a.Version, Revision: a.Revision, SHA: a.Submission.SHA}, {ID: unfinished.ID, Version: unfinished.Version, Revision: unfinished.Revision, SHA: "not-ready"}}})
	if e == nil {
		t.Fatal("unfinished work published")
	}
	actual, _ := s.Store.GetTask(c, a.ID)
	if actual.Status != "pending_release" || len(versionDetail(t, s, v.ID).ProjectVersion.Releases) != 0 {
		t.Fatal("partial publication saved")
	}
	published := publishVersion(t, s, v.ID, a).Tasks[0]
	actual, _ = s.Store.GetTask(c, b.ID)
	if published.Status != "review" || actual.Status != "pending_release" || len(versionDetail(t, s, v.ID).ProjectVersion.Releases[0].Tasks) != 1 {
		t.Fatal("unselected task advanced")
	}
}

func TestMilestoneSingleTaskDeploymentPreservesSnapshot(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	task, claim, _ := taskAtStage(t, s, p.ID, "pending_release")
	m, e := s.Act(c, developer(p.ID, "dev"), task.ID, app.Action{Action: "mark_deployed", RequestID: "deploy", ClaimID: claim.ClaimID, Deployment: &domain.Deployment{Revision: task.Revision, SHA: task.Submission.SHA, Environment: "生产", Body: "实际发布", ProjectVersionID: "forged", ReleaseID: "forged"}})
	if e != nil {
		t.Fatal(e)
	}
	info := versionDetail(t, s, task.ProjectVersionID)
	if info.ProjectVersion.ScopeFrozen || len(info.ProjectVersion.Releases) != 1 || m.Task.Deployment.ProjectVersionID != task.ProjectVersionID || m.Task.Deployment.ReleaseID == "forged" {
		t.Fatal("single task deployment bypassed milestone snapshots", m, info)
	}
}

func TestMilestonePublicationRacesRequirementRevision(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	task, _, _ := taskAtStage(t, s, p.ID, "pending_release")
	v := versionDetail(t, s, task.ProjectVersionID).ProjectVersion
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, e := s.ActProjectVersion(c, app.Human(), v.ID, app.ProjectVersionAction{Action: "publish", Version: v.Version, Environment: "生产", Body: "实际上线", Items: []app.VersionTaskRef{{ID: task.ID, Version: task.Version, Revision: task.Revision, SHA: task.Submission.SHA}}, RequestID: "publish-race"})
		results <- e
	}()
	go func() {
		<-start
		_, e := s.EditTask(c, app.Human(), task.ID, app.TaskInput{Title: task.Title, Kind: task.Kind, Description: "新业务范围", Acceptance: task.Acceptance, Version: task.Version, RequestID: "edit-race"})
		results <- e
	}()
	close(start)
	successes := 0
	for n := 0; n < 2; n++ {
		e := <-results
		if e == nil {
			successes++
		} else if !errors.Is(e, domain.ErrConflict) {
			t.Fatal(e)
		}
	}
	fresh, _ := s.Store.GetTask(c, task.ID)
	view := versionDetail(t, s, v.ID)
	if successes != 1 {
		t.Fatal("stale checklist and new revision both committed", successes)
	}
	if fresh.Status == "review" {
		if fresh.Revision != 1 || len(view.ProjectVersion.Releases) != 1 {
			t.Fatal("inconsistent publication")
		}
	} else if fresh.Revision != 2 || len(view.ProjectVersion.Releases) != 0 {
		t.Fatal("failed publication left history", fresh, view)
	}
}

func TestMilestoneClosedAcceptanceKeepsDecisionsAndContinuesWork(t *testing.T) {
	for _, operation := range []string{"close", "reject", "accept"} {
		t.Run(operation, func(t *testing.T) {
			s, p := setup(t)
			c := context.Background()
			task, _, _ := taskAtStage(t, s, p.ID, "pending_release")
			task = publishVersion(t, s, task.ProjectVersionID, task).Tasks[0]
			v := versionDetail(t, s, task.ProjectVersionID).ProjectVersion
			if _, e := s.ActProjectVersion(c, app.Human(), v.ID, app.ProjectVersionAction{Action: "close", Version: v.Version, Body: "本期结束", RequestID: "close-version"}); e != nil {
				t.Fatal(e)
			}
			in := app.Action{Action: operation, RequestID: "decision", Body: "业务决定"}
			if operation != "close" {
				in.Review = &domain.Review{Revision: task.Revision, SHA: task.Submission.SHA, Environment: "生产", Note: "验收结论", Checks: []string{"passed"}}
			}
			result, e := s.Act(c, app.Human(), task.ID, in)
			if e != nil {
				t.Fatal(e)
			}
			view := versionDetail(t, s, v.ID)
			if operation == "reject" {
				old := view.ProjectVersion.Scope[0]
				if result.Task.ProjectVersionID == "" || result.Task.ProjectVersionID == v.ID || old.Task.Review == nil || old.Task.Review.Accepted || old.Reason == "" {
					t.Fatal("returned acceptance was lost or left unplanned", result, view)
				}
			} else if result.Task.ProjectVersionID != v.ID {
				t.Fatal("final decision left historical milestone")
			}
			if len(view.ProjectVersion.Releases) != 1 || view.ProjectVersion.Releases[0].Tasks[0].Status != "review" {
				t.Fatal("final decision rewrote release snapshot")
			}
		})
	}
}
