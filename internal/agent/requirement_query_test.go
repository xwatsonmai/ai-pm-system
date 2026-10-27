package agent

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestQuestionContextAndToolsUseLiveScopedRequirements(t *testing.T) {
	m, db, r, _ := collaborationToolsFixture(t)
	c := context.Background()
	a := domain.Actor{ID: "platform", Kind: "platform", ProjectID: r.ProjectID}
	created, err := m.App.CreateTask(c, app.Human(), r.ProjectID, app.TaskInput{Title: "图片保存", Description: "DO_NOT_INJECT_FULL_DOCUMENT", RequestID: "task"})
	if err != nil {
		t.Fatal(err)
	}
	r.Prompt = "创建需求"
	if err = db.PutRun(c, r); err != nil {
		t.Fatal(err)
	}
	v, err := m.App.ProposeRequirement(c, r.ID, app.RequirementIntakeInput{Title: "保存需求", Kind: "requirement", Description: "PRIVATE_PROPOSAL_DOCUMENT", Reason: "业务目标", MissingDetails: []string{"保存天数"}})
	if err != nil {
		t.Fatal(err)
	}
	v, err = m.App.ResolveRequirementFromQuestion(c, r.ID, v.ID, "create", "创建需求")
	if err != nil {
		t.Fatal(err)
	}
	foreign := r
	foreign.ID = "other-conversation"
	foreign.ConversationID = "other"
	foreign.OwnerID = "bob"
	if err = db.PutRun(c, foreign); err != nil {
		t.Fatal(err)
	}
	if _, err = m.App.ProposeRequirement(c, foreign.ID, app.RequirementIntakeInput{Title: "DO_NOT_LEAK_PRIVATE_TITLE", Kind: "requirement", Description: "别人的私聊", Reason: "隐私"}); err != nil {
		t.Fatal(err)
	}
	initial, err := m.initialContext(c, a, r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(initial)
	for _, secret := range []string{"DO_NOT_INJECT_FULL_DOCUMENT", "PRIVATE_PROPOSAL_DOCUMENT", "DO_NOT_LEAK_PRIVATE_TITLE"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("bloated/private snapshot", string(b))
		}
	}
	if !strings.Contains(string(b), v.TaskID) || initial["events"] != nil || initial["tasks"] != nil || initial["artifacts"] != nil {
		t.Fatal(initial)
	}
	call := func(name, args string) any {
		t.Helper()
		out, e := m.callTool(c, a, r, "query", name, json.RawMessage(args))
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	pool := call("requirement_pool", `{"limit":1}`).(app.RequirementPoolResult)
	if pool.Counts.Total != 2 || len(pool.Items) != 1 || !pool.HasMore {
		t.Fatal(pool)
	}
	id := created.Task.ID
	review, err := m.App.SaveRequirementReview(c, app.Human(), id, app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.App.FinishRequirementReview(c, id, review.Generation, "", &domain.RequirementReviewResult{Body: "需要确认时间", Decision: "clarify", Questions: []domain.RequirementQuestion{{ID: "days", Label: "天数", Type: "text"}}}, ""); err != nil {
		t.Fatal(err)
	}
	args := `{"task_id":"` + id + `","review_limit":1}`
	before := call("requirement_detail", args).(app.RequirementDetailResult)
	if len(before.Review.PendingQuestions) != 1 {
		t.Fatal(before)
	}
	if _, err = m.App.SaveRequirementReview(c, app.Human(), id, app.RequirementReviewInput{Action: "message", Body: "90天", RequestID: "answer"}); err != nil {
		t.Fatal(err)
	}
	after := call("requirement_detail", args).(app.RequirementDetailResult)
	if after.Review.LatestSupplement.Body != "90天" || after.Review.State != "reviewing" || len(after.Review.PendingQuestions) != 0 {
		t.Fatal("tool used stale context", after)
	}
	project := call("project_context", `{}`).(map[string]any)
	if project["tasks"] != nil || project["events"] != nil {
		t.Fatal("legacy tool bypassed lightweight context", project)
	}
	if _, err = m.callTool(c, a, r, "invalid", "requirement_pool", json.RawMessage(`{"limit":"5"}`)); err == nil {
		t.Fatal("numeric argument silently ignored")
	}
	other, _ := m.App.SaveProject(c, domain.Project{Name: "other"})
	otherTask, _ := m.App.CreateTask(c, app.Human(), other.ID, app.TaskInput{Title: "foreign", RequestID: "foreign"})
	if _, err = m.callTool(c, a, r, "foreign", "requirement_detail", json.RawMessage(`{"task_id":"`+otherTask.Task.ID+`"}`)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("cross project tool access", err)
	}
}

func TestReviewContextDoesNotInjectHistoricalProgressOrDevelopmentReceipts(t *testing.T) {
	m, db, r, _ := collaborationToolsFixture(t)
	c := context.Background()
	a := domain.Actor{ID: "platform", Kind: "platform", ProjectID: r.ProjectID}
	created, err := m.App.CreateTask(c, app.Human(), r.ProjectID, app.TaskInput{Title: "显示素材尺寸", Description: "显示宽高", RequestID: "review-source"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 95; i++ {
		if err = db.PutArtifact(c, domain.Artifact{ID: domain.ID("analysis_"), ProjectID: r.ProjectID, Kind: "analysis", Body: strings.Repeat("HISTORICAL_PROGRESS_DO_NOT_INJECT", 200)}); err != nil {
			t.Fatal(err)
		}
	}
	r.Kind, r.TaskID = "requirement_review", created.Task.ID
	initial, err := m.initialContext(c, a, r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(initial)
	if strings.Contains(string(b), "HISTORICAL_PROGRESS_DO_NOT_INJECT") || initial["artifacts"] != nil || initial["tasks"] != nil || len(b) > 15000 {
		t.Fatal("review context grows with historical analyses", len(b))
	}
	pool, ok := initial["requirement_pool"].(app.RequirementPoolResult)
	if !ok || pool.Counts.Total < 1 || len(pool.Items) == 0 {
		t.Fatal("review cannot discover related requirements", initial)
	}
}
