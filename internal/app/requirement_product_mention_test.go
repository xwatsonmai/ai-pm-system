package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"slices"
	"testing"
	"unicode/utf16"
)

func productMentionFixture(t *testing.T) (*app.Service, domain.Project, domain.Actor) {
	t.Helper()
	s, p, _, member := accessFixture(t)
	p.ProjectRoles = append(p.ProjectRoles,
		domain.ProjectRole{ID: "operations", Name: "运营", Pages: []string{"chat", "tasks"}, Actions: []string{domain.ActionAskAgent, domain.ActionCreateRequirement, domain.ActionCreateBug}},
		domain.ProjectRole{ID: "business", Name: "业务负责人", Pages: []string{"tasks"}, Actions: []string{}},
		domain.ProjectRole{ID: "hidden-product", Name: "产品", Pages: []string{"overview"}, Actions: []string{}},
	)
	p.Memberships = append(p.Memberships,
		domain.ProjectMember{UserID: member.ID, Roles: []string{"operations"}, Responsibilities: []string{"developer"}},
		domain.ProjectMember{UserID: "product-a", Roles: []string{"business"}, Responsibilities: []string{"product"}},
		domain.ProjectMember{UserID: "product-b", Roles: []string{"business", "operations"}, Responsibilities: []string{"product", "tester"}},
		domain.ProjectMember{UserID: "unreadable", Roles: []string{"hidden-product"}, Responsibilities: []string{"product"}},
		domain.ProjectMember{UserID: "named-product", Roles: []string{"product"}, Responsibilities: []string{}},
	)
	for _, u := range []domain.User{{ID: "product-a", Name: "产品😀甲"}, {ID: "product-b", Name: "产品乙"}, {ID: "unreadable", Name: "无权限"}, {ID: "named-product", Name: "仅角色同名"}} {
		if err := s.Store.PutUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Store.PutProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return s, p, member
}

func assertProductMention(t *testing.T, s *app.Service, task domain.Task, expected bool) domain.RequirementReviewMessage {
	t.Helper()
	c := context.Background()
	events, err := s.Store.Events(c, task.ProjectID, task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	messages := []domain.RequirementReviewMessage{}
	for _, event := range events {
		if m, ok := domain.CreatedRequirementProductMention(event); ok {
			messages = append(messages, m)
		}
	}
	if !expected {
		if len(messages) != 0 {
			t.Fatal("unexpected product reminder", messages)
		}
		return domain.RequirementReviewMessage{}
	}
	if len(messages) != 1 {
		t.Fatalf("creation did not save exactly one product reminder: %+v", messages)
	}
	m := messages[0]
	ids := []string{}
	encoded := utf16.Encode([]rune(m.Body))
	for _, mention := range m.Mentions {
		ids = append(ids, mention.UserID)
		if string(utf16.Decode(encoded[mention.Start:mention.End])) != "@"+mention.Name {
			t.Fatal("automatic mention offsets do not match UTF-16 text", mention, m.Body)
		}
	}
	if !slices.Equal(ids, []string{"product-a", "product-b"}) {
		t.Fatal("custom duties, multi-role dedup or page filtering failed", ids)
	}
	for i := 0; i < 2; i++ {
		view, err := s.RequirementReview(c, app.Human(), task.ID)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, item := range view.Messages {
			if item.ID == m.ID {
				count++
			}
		}
		if count != 1 {
			t.Fatal("room read duplicated or omitted the reminder", view.Messages)
		}
	}
	return m
}

func TestNonProductRequirementCreationMentionsProductsAcrossEntryPoints(t *testing.T) {
	for _, entry := range []string{"web", "personal-mcp", "question", "iteration"} {
		t.Run(entry, func(t *testing.T) {
			s, p, member := productMentionFixture(t)
			c := context.Background()
			in := app.TaskInput{Title: "新需求", Description: "由非产品成员发起", Acceptance: []string{"行为符合预期"}, RequestID: "create"}
			var task domain.Task
			switch entry {
			case "web", "personal-mcp":
				for i := 0; i < 2; i++ {
					var mutation domain.Mutation
					var err error
					if entry == "web" {
						mutation, err = s.CreateTask(c, member, p.ID, in)
					} else {
						actor := domain.Actor{ID: member.ID, UserID: member.ID, Kind: "developer", Name: member.Name + " · MCP", ProjectID: p.ID}
						mutation, err = s.CreateRequirementFromMCP(c, actor, in, app.ProductAuthorization{UserConfirmed: true, UserInstruction: "请创建该需求"})
					}
					if err != nil || (i > 0 && mutation.Task.ID != task.ID) {
						t.Fatal("creation or idempotent retry failed", err)
					}
					task = mutation.Task
				}
			case "question":
				r := domain.Run{ID: "question", ProjectID: p.ID, Kind: "question", OwnerID: member.ID, OwnerKind: "human", Prompt: "请创建需求", ConversationID: "conversation"}
				if err := s.Store.PutRun(c, r); err != nil {
					t.Fatal(err)
				}
				proposal, err := s.ProposeRequirement(c, r.ID, app.RequirementIntakeInput{Title: in.Title, Kind: "requirement", Description: in.Description, Acceptance: in.Acceptance, Reason: "明确的新增需求"})
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					result, err := s.ResolveRequirementFromQuestion(c, r.ID, proposal.ID, "create", "请创建需求")
					if err != nil {
						t.Fatal(err)
					}
					task, err = s.Store.GetTask(c, result.TaskID)
					if err != nil {
						t.Fatal(err)
					}
				}
			case "iteration":
				original, err := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "原需求", Description: "已确认的原需求内容", Acceptance: []string{"完成"}, RequestID: "original"})
				if err != nil {
					t.Fatal(err)
				}
				source := acceptIterationTask(t, s, original.Task)
				for i := 0; i < 2; i++ {
					result, err := s.CreateRequirementIteration(c, member, source.ID, iterationInput(source, "iteration"))
					if err != nil {
						t.Fatal(err)
					}
					task = result.Task
				}
			}
			if task.Creator == nil || task.Creator.ID != member.ID || task.Status != "clarify" {
				t.Fatal("reminder changed attribution or bypassed review", task)
			}
			assertProductMention(t, s, task, true)
		})
	}
}

func TestProductCreationAndBugsDoNotAutoMentionProducts(t *testing.T) {
	s, p, member := productMentionFixture(t)
	c := context.Background()
	for _, tc := range []struct{ user, kind string }{{"product-b", "requirement"}, {member.ID, "bug"}} {
		actor := domain.Actor{ID: tc.user, Kind: "human", Name: "发起人"}
		result, err := s.CreateTask(c, actor, p.ID, app.TaskInput{Title: "正常创建", Kind: tc.kind, RequestID: tc.user})
		if err != nil {
			t.Fatal(err)
		}
		assertProductMention(t, s, result.Task, false)
	}
	// A role called 产品 does not suppress the reminder when duties are empty.
	result, err := s.CreateTask(c, domain.Actor{ID: "named-product", Kind: "human"}, p.ID, app.TaskInput{Title: "角色名不代表职责", RequestID: "named"})
	if err != nil {
		t.Fatal(err)
	}
	assertProductMention(t, s, result.Task, true)
	for i := range p.Memberships {
		p.Memberships[i].Responsibilities = []string{}
	}
	if err := s.Store.PutProject(c, p); err != nil {
		t.Fatal(err)
	}
	result, err = s.CreateTask(c, member, p.ID, app.TaskInput{Title: "未配置产品也可保存", RequestID: "no-product"})
	if err != nil {
		t.Fatal(err)
	}
	assertProductMention(t, s, result.Task, false)
	if _, err = s.RequirementReview(c, member, result.Task.ID); err != nil {
		t.Fatal("reminder changed the existing page grant", err)
	}
}
