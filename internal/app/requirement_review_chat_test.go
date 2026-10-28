package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func reviewTopic(id, kind, status string) domain.RequirementTopic {
	t := domain.RequirementTopic{ID: id, Title: "话题" + id, Kind: kind, Status: status}
	if status == "resolved" {
		t.Conclusion = "已确认"
	}
	return t
}

func choiceQuestion(id string) domain.RequirementQuestion {
	return domain.RequirementQuestion{ID: id, Label: "点数要支持小数吗？", Type: "radio", Options: []string{"支持两位小数", "只支持整数"}, Recommended: []string{}}
}

func TestReviewSubmissionRules(t *testing.T) {
	base := func() app.RequirementReviewSubmission {
		return app.RequirementReviewSubmission{
			Reply: "先确认改价方式。", Decision: "clarify",
			Agenda: []domain.RequirementTopic{reviewTopic("mode", "direction", "open"), reviewTopic("items", "detail", "open")},
			Ask:    &app.RequirementAskInput{TopicID: "mode", Kind: "direction", Question: "保存后是一次性改价，还是长期锁定？", QuickReplies: []string{"一次性改价", "长期锁定"}},
		}
	}
	directionDone := func(v *app.RequirementReviewSubmission) { v.Agenda[0] = reviewTopic("mode", "direction", "resolved") }
	form := func(n int) func(*app.RequirementReviewSubmission) {
		return func(v *app.RequirementReviewSubmission) {
			directionDone(v)
			v.Ask = &app.RequirementAskInput{TopicID: "items", Kind: "form"}
			for i := 0; i < n; i++ {
				v.Ask.Questions = append(v.Ask.Questions, choiceQuestion(string(rune('a'+i))))
			}
		}
	}
	ready := func(v *app.RequirementReviewSubmission) {
		v.Decision, v.Ask = "ready", nil
		v.Agenda = []domain.RequirementTopic{reviewTopic("mode", "direction", "resolved"), reviewTopic("items", "detail", "dropped")}
		v.Proposal = &domain.RequirementProposal{Description: "需求稿", Acceptance: []string{"保存后价格生效"}}
	}
	cases := []struct {
		name     string
		change   func(*app.RequirementReviewSubmission)
		previous []domain.RequirementTopic
		want     string
	}{
		{name: "direction question in plain text"},
		{name: "detail form after the direction is settled", change: form(3)},
		{name: "ready once every topic is closed", change: ready},
		{name: "empty reply", change: func(v *app.RequirementReviewSubmission) { v.Reply = " " }, want: "reply 不能为空"},
		{name: "long reply", change: func(v *app.RequirementReviewSubmission) { v.Reply = strings.Repeat("字", 401) }, want: "不能超过 400 字"},
		{name: "clarify needs one ask", change: func(v *app.RequirementReviewSubmission) { v.Ask = nil }, want: "必须带 ask"},
		{name: "details wait for the direction", change: func(v *app.RequirementReviewSubmission) {
			v.Ask = &app.RequirementAskInput{TopicID: "items", Kind: "form", Questions: []domain.RequirementQuestion{choiceQuestion("a")}}
		}, want: "方向性话题"},
		{name: "direction is never a form", change: func(v *app.RequirementReviewSubmission) {
			v.Ask = &app.RequirementAskInput{TopicID: "mode", Kind: "form", Questions: []domain.RequirementQuestion{choiceQuestion("a")}}
		}, want: "直接用文字问"},
		{name: "direction carries no form questions", change: func(v *app.RequirementReviewSubmission) {
			v.Ask.Questions = []domain.RequirementQuestion{choiceQuestion("a")}
		}, want: "不能附表单"},
		{name: "at most three quick replies", change: func(v *app.RequirementReviewSubmission) { v.Ask.QuickReplies = []string{"甲", "乙", "丙", "丁"} }, want: "最多 3 条"},
		{name: "at most three form questions", change: form(4), want: "1 至 3 题"},
		{name: "ask targets an open topic", change: func(v *app.RequirementReviewSubmission) { directionDone(v) }, want: "status=open"},
		{name: "resolved topic needs a conclusion", change: func(v *app.RequirementReviewSubmission) {
			v.Agenda = append(v.Agenda, domain.RequirementTopic{ID: "x", Title: "范围", Kind: "detail", Status: "resolved"})
		}, want: "conclusion"},
		{name: "long topic title", change: func(v *app.RequirementReviewSubmission) { v.Agenda[1].Title = strings.Repeat("长", 21) }, want: "不超过 20 字"},
		{name: "too many open topics", change: func(v *app.RequirementReviewSubmission) {
			for _, id := range []string{"c", "d", "e", "f", "g"} {
				v.Agenda = append(v.Agenda, reviewTopic(id, "detail", "open"))
			}
		}, want: "最多 6 个"},
		{name: "topics never silently disappear", previous: []domain.RequirementTopic{reviewTopic("scope", "detail", "open")}, want: "不能直接删除"},
		{name: "ready with an open topic", change: func(v *app.RequirementReviewSubmission) {
			ready(v)
			v.Agenda[1].Status = "open"
		}, want: "resolved 或 dropped"},
		{name: "internal id in reply", change: func(v *app.RequirementReviewSubmission) {
			v.Reply = "该需求已由产品确认（rm_627f0902447c6f9868664846）"
		}, want: "内部编号"},
		{name: "internal id in an option", change: func(v *app.RequirementReviewSubmission) {
			form(1)(v)
			v.Ask.Questions[0].Options[0] = "沿用 tsk_0123456789abcdef01234567 的规则"
		}, want: "内部编号"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := base()
			if tc.change != nil {
				tc.change(&v)
			}
			err := app.ValidateRequirementReviewSubmission(v, tc.previous)
			if tc.want == "" && err != nil {
				t.Fatalf("unexpected rejection: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}

	// Storage keeps the reply in body and form questions in questions, so the
	// existing answer links and form expiry apply to every round.
	direction := base().Result()
	if direction.Body != "先确认改价方式。" || direction.Ask.Question == "" || len(direction.Ask.QuickReplies) != 2 || len(direction.Questions) != 0 {
		t.Fatalf("direction mapping: %+v", direction)
	}
	v := base()
	form(2)(&v)
	mapped := v.Result()
	if mapped.Ask.Kind != "form" || mapped.Ask.Question != "" || len(mapped.Questions) != 2 || len(mapped.Agenda) != 2 {
		t.Fatalf("form mapping: %+v", mapped)
	}
}

func TestReviewConversationRecordsWhoDidWhat(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	p.Memberships = []domain.ProjectMember{{UserID: "u_lin", Roles: []string{"product"}}, {UserID: "u_zhou", Roles: []string{"product"}}}
	if err := s.Store.PutProject(c, p); err != nil {
		t.Fatal(err)
	}
	lin := domain.Actor{ID: "u_lin", Name: "林悦", Kind: "human"}
	zhou := domain.Actor{ID: "u_zhou", Name: "周航", Kind: "human"}
	created, err := s.CreateTask(c, lin, p.ID, app.TaskInput{Title: "模板设置价格", Description: "系统设置增加模板价格配置", RequestID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Task.ID
	if created.Task.Creator == nil || created.Task.Creator.ID != "u_lin" || created.Task.Creator.Kind != "human" {
		t.Fatalf("creator not recorded: %+v", created.Task.Creator)
	}
	v, err := s.SaveRequirementReview(c, zhou, id, app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginRequirementReview(c, id, v.Generation, "r1"); err != nil {
		t.Fatal(err)
	}
	agenda := []domain.RequirementTopic{reviewTopic("mode", "direction", "open"), reviewTopic("items", "detail", "open")}
	round := app.RequirementReviewSubmission{Reply: "先确认改价方式。", Decision: "clarify", Agenda: agenda, Ask: &app.RequirementAskInput{TopicID: "mode", Kind: "direction", Question: "一次性改价还是长期锁定？"}}
	result := round.Result()
	if err = s.FinishRequirementReview(c, id, v.Generation, "r1", &result, ""); err != nil {
		t.Fatal(err)
	}
	v, _ = s.RequirementReview(c, lin, id)
	last := v.Messages[len(v.Messages)-1]
	if len(v.Agenda) != 2 || last.ActorKind != "platform" || last.Result.Ask.Kind != "direction" || v.Creator == nil || v.Creator.Name != "林悦" {
		t.Fatalf("agenda or Agent attribution missing: %+v", v)
	}
	detail, err := s.RequirementDetail(c, lin, p.ID, app.RequirementDetailQuery{TaskID: id})
	if err != nil || detail.Review.PendingAsk == nil || detail.Review.PendingAsk.Question != "一次性改价还是长期锁定？" || len(detail.Review.Agenda) != 2 {
		t.Fatalf("query hides the pending direction question: %+v %v", detail.Review, err)
	}
	var prompt struct {
		Agenda       []domain.RequirementTopic         `json:"agenda"`
		Conversation []domain.RequirementReviewMessage `json:"conversation"`
	}
	if err = json.Unmarshal([]byte(app.ReviewPrompt(v)), &prompt); err != nil || len(prompt.Agenda) != 2 {
		t.Fatalf("model input lacks the agenda: %v", err)
	}

	// Another product member answers in plain text: the message names its author.
	v, err = s.SaveRequirementReview(c, zhou, id, app.RequirementReviewInput{Action: "message", Body: "一次性改价，之后各级还能改", RequestID: "answer"})
	if err != nil {
		t.Fatal(err)
	}
	answer := v.Messages[len(v.Messages)-1]
	if answer.Actor != "周航" || answer.ActorID != "u_zhou" || answer.ActorKind != "human" {
		t.Fatalf("answer not attributed: %+v", answer)
	}
	if err = json.Unmarshal([]byte(app.ReviewPrompt(v)), &prompt); err != nil || strings.Contains(app.ReviewPrompt(v), "u_zhou") || prompt.Conversation[len(prompt.Conversation)-1].Actor != "周航" {
		t.Fatalf("model input should name people without account IDs: %v", err)
	}

	// A failed round and its retry are both visible, with who retried.
	if err = s.FinishRequirementReview(c, id, v.Generation, "", nil, "模型响应超时"); err != nil {
		t.Fatal(err)
	}
	v, err = s.SaveRequirementReview(c, lin, id, app.RequirementReviewInput{Action: "retry", RequestID: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	failed, retried := v.Messages[len(v.Messages)-2], v.Messages[len(v.Messages)-1]
	if failed.Action != "review_failed" || failed.Event.Reason != "模型响应超时" || retried.Action != "review_retried" || retried.ActorID != "u_lin" || retried.Body != "林悦 重新发起了评审" {
		t.Fatalf("failure or retry not recorded: %+v %+v", failed, retried)
	}

	// A form is open when 周航 edits the requirement: the edit is recorded at
	// once, with the committed snapshot, and the form expires.
	if err = s.BeginRequirementReview(c, id, v.Generation, "r2"); err != nil {
		t.Fatal(err)
	}
	agenda[0] = reviewTopic("mode", "direction", "resolved")
	round = app.RequirementReviewSubmission{Reply: "改价方式已确认。", Decision: "clarify", Agenda: agenda, Ask: &app.RequirementAskInput{TopicID: "items", Kind: "form", Questions: []domain.RequirementQuestion{choiceQuestion("decimal")}}}
	result = round.Result()
	if err = s.FinishRequirementReview(c, id, v.Generation, "r2", &result, ""); err != nil {
		t.Fatal(err)
	}
	task, _ := s.Store.GetTask(c, id)
	if _, err = s.EditTask(c, zhou, id, app.TaskInput{Title: task.Title, Description: "系统设置增加模板价格配置，币值数改叫点数", Version: task.Version, RequestID: "edit"}); err != nil {
		t.Fatal(err)
	}
	edited, _ := s.Store.GetTask(c, id)
	v, _ = s.RequirementReview(c, lin, id)
	edit := v.Messages[len(v.Messages)-1]
	form := v.Messages[len(v.Messages)-2]
	if edit.Action != "task_edited" || edit.ActorID != "u_zhou" || edit.Event.FromRevision != 1 || edit.Event.ToRevision != 2 || edit.Source == nil || edit.Source.Version != edited.Version || form.FormExpiredAt == 0 {
		t.Fatalf("edit not recorded with snapshot: %+v / form %+v", edit, form)
	}
	count := len(v.Messages)
	if v, err = s.SaveRequirementReview(c, lin, id, app.RequirementReviewInput{Action: "start", RequestID: "after-edit"}); err != nil || v.Status != "queued" || len(v.Messages) != count {
		t.Fatalf("edit snapshot duplicated on the next round: %d -> %d %v", count, len(v.Messages), err)
	}

	// Closing and reopening are part of the same conversation.
	task, _ = s.Store.GetTask(c, id)
	if _, err = s.Act(c, zhou, id, app.Action{Action: "close", Body: "并入「模板积分」一起做", Version: task.Version, RequestID: "close"}); err != nil {
		t.Fatal(err)
	}
	v, _ = s.RequirementReview(c, lin, id)
	closed := v.Messages[len(v.Messages)-1]
	if v.Status != "closed" || closed.Action != "task_closed" || closed.Body != "周航 关闭了需求：并入「模板积分」一起做" || closed.Event.Reason != "并入「模板积分」一起做" {
		t.Fatalf("close not recorded: %+v", closed)
	}
	task, _ = s.Store.GetTask(c, id)
	if _, err = s.Act(c, lin, id, app.Action{Action: "reopen", Body: "恢复独立排期", Version: task.Version, RequestID: "reopen"}); err != nil {
		t.Fatal(err)
	}
	reopenedTask, _ := s.Store.GetTask(c, id)
	v, _ = s.RequirementReview(c, lin, id)
	reopened := v.Messages[len(v.Messages)-1]
	if reopened.Action != "task_reopened" || reopened.ActorID != "u_lin" || reopened.Source == nil || reopened.Source.Revision != reopenedTask.Revision {
		t.Fatalf("reopen not recorded: %+v", reopened)
	}
	count = len(v.Messages)
	if v, err = s.SaveRequirementReview(c, lin, id, app.RequirementReviewInput{Action: "start", RequestID: "after-reopen"}); err != nil || v.Status != "queued" || len(v.Messages) != count {
		t.Fatalf("reopen did not restart review cleanly: %d -> %d %v", count, len(v.Messages), err)
	}
}

func TestReviewRetryNamesRecheck(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := app.Human()
	created, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "复核", RequestID: "create"})
	id := created.Task.ID
	v, _ := s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if err := s.BeginRequirementReview(c, id, v.Generation, "r1"); err != nil {
		t.Fatal(err)
	}
	result := app.RequirementReviewSubmission{Reply: "请确认范围。", Decision: "clarify", Agenda: []domain.RequirementTopic{reviewTopic("scope", "direction", "open")}, Ask: &app.RequirementAskInput{TopicID: "scope", Kind: "direction", Question: "覆盖哪些商家？"}}.Result()
	// A requirement this round read changed afterwards, so confirmation needs a recheck.
	read, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "参考", RequestID: "read"})
	stale := domain.RequirementReviewReference{TaskID: read.Task.ID, Revision: read.Task.Revision - 1}
	if err := s.FinishRequirementReview(c, id, v.Generation, "r1", &result, "", &domain.RequirementReviewEvidence{References: []domain.RequirementReviewReference{stale}}); err != nil {
		t.Fatal(err)
	}
	v, err := s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "retry", RequestID: "recheck"})
	if err != nil {
		t.Fatal(err)
	}
	if last := v.Messages[len(v.Messages)-1]; last.Action != "recheck_requested" || last.Body != "项目负责人 请 Agent 重新核对关联需求" {
		t.Fatalf("recheck not named: %+v", last)
	}
}

func TestLegacyTaskCreatorFallsBackToCreationEvent(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	created, _ := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "旧需求", RequestID: "create"})
	legacy := created.Task
	legacy.Creator = nil
	legacy.Version++
	if err := s.Store.CommitTask(c, legacy, created.Task.Version, domain.Event{ProjectID: p.ID, TaskID: legacy.ID, Kind: "migrated", Actor: "系统", At: domain.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	v, err := s.RequirementReview(c, app.Human(), legacy.ID)
	if err != nil || v.Creator == nil || v.Creator.Name != "项目负责人" || v.Creator.ID != "" {
		t.Fatalf("creator fallback: %+v %v", v.Creator, err)
	}
}

func TestReviewProgressWhileRunning(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := app.Human()
	created, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "进度", RequestID: "create"})
	id := created.Task.ID
	v, _ := s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if err := s.BeginRequirementReview(c, id, v.Generation, "run-progress"); err != nil {
		t.Fatal(err)
	}
	steps := []domain.Step{
		{Name: "requirement_pool", Label: app.ReviewStepLabel("requirement_pool", nil), Status: "done"},
		{Name: "requirement_detail", Label: app.ReviewReadLabel("模板积分", false), Status: "done"},
		{Name: "repository_read", Label: app.ReviewStepLabel("repository_read", json.RawMessage(`{"action":"file","path":"web/src/template/List.vue"}`)), Status: "running"},
		{Name: "project_context", Status: "done"}, // older runs have no label
	}
	if err := s.Store.PutRun(c, domain.Run{ID: "run-progress", ProjectID: p.ID, TaskID: id, Kind: "requirement_review", Status: "running", Steps: steps}); err != nil {
		t.Fatal(err)
	}
	v, _ = s.RequirementReview(c, a, id)
	var labels []string
	for _, step := range v.Progress {
		labels = append(labels, step.Label+"/"+step.Status)
	}
	if strings.Join(labels, " ") != "查找相关需求/done 阅读「模板积分」/done 阅读代码（List.vue）/running 查看项目资料/done" {
		t.Fatalf("progress: %v", labels)
	}
	if err := s.FinishRequirementReview(c, id, v.Generation, "run-progress", nil, "超时"); err != nil {
		t.Fatal(err)
	}
	if v, _ = s.RequirementReview(c, a, id); v.Progress != nil {
		t.Fatal("progress belongs to a running round only", v.Progress)
	}
	if app.ReviewReadLabel("本需求", true) != "回看本需求的记录" || app.ReviewStepLabel("unknown_tool", nil) != "查阅资料" {
		t.Fatal("labels")
	}
}
