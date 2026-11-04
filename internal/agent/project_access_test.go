package agent

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestQuestionWorkerCannotBorrowPlatformPrivileges(t *testing.T) {
	m, db, r, _ := collaborationToolsFixture(t)
	c := context.Background()
	p, _ := db.GetProject(c, r.ProjectID)
	p.Repository = "https://example.com/SECRET_REPO.git"
	p.ProjectRoles = append(p.ProjectRoles, domain.ProjectRole{ID: "chat-only", Name: "Chat", Pages: []string{"chat"}, Version: 1})
	p.Memberships = []domain.ProjectMember{{UserID: r.OwnerID, Roles: []string{"chat-only"}, Responsibilities: []string{}}}
	p.AccessVersion++
	if e := db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	system := domain.Actor{ID: "platform", Kind: "platform", ProjectID: p.ID}
	initial, e := m.initialContext(c, system, r)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(initial)
	if strings.Contains(string(raw), "SECRET_REPO") {
		t.Fatal("initial context borrowed worker permissions", string(raw))
	}
	for _, tool := range []string{"requirement_pool", "requirement_detail", "project_version_list", "project_timeline", "repository_read", "repository_info", "read"} {
		if _, e = m.callTool(c, system, r, "denied", tool, json.RawMessage(`{}`)); !errors.Is(e, domain.ErrForbidden) {
			t.Fatalf("%s borrowed platform privileges: %v", tool, e)
		}
	}
	if _, e = m.callTool(c, system, r, "allowed", "project_context", json.RawMessage(`{}`)); e != nil {
		t.Fatal("chat-only context unavailable", e)
	}
	p.Memberships[0].Roles = []string{}
	p.AccessVersion++
	if e = db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	if _, e = m.callTool(c, system, r, "revoked", "project_context", json.RawMessage(`{}`)); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("revoked chat continued platform calls", e)
	}
}
