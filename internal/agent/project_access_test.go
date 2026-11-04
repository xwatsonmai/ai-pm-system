package agent

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/sandbox"
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

func TestManualAnalysisKindsCannotBorrowPlatformPrivileges(t *testing.T) {
	m, db, r, _ := collaborationToolsFixture(t)
	c := context.Background()
	p, _ := db.GetProject(c, r.ProjectID)
	p.Repository = "https://example.com/SECRET_REPO.git"
	p.ProjectRoles = append(p.ProjectRoles, domain.ProjectRole{ID: "tasks-only", Name: "Tasks", Pages: []string{"tasks"}, Version: 1})
	p.Memberships = []domain.ProjectMember{{UserID: r.OwnerID, Roles: []string{"tasks-only"}, Responsibilities: []string{}}}
	p.AccessVersion++
	if e := db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	if e := db.PutArtifact(c, domain.Artifact{ID: "secret-review", ProjectID: p.ID, Kind: "deployment", Body: "SECRET_REVIEW"}); e != nil {
		t.Fatal(e)
	}
	system := domain.Actor{ID: "platform", Kind: "platform", ProjectID: p.ID}
	for _, kind := range []string{"requirements", "triage", "knowledge", "progress"} {
		t.Run(kind, func(t *testing.T) {
			r.Kind = kind
			initial, e := m.initialContext(c, system, r)
			if e != nil {
				t.Fatal(e)
			}
			raw, _ := json.Marshal(initial)
			if strings.Contains(string(raw), "SECRET_") {
				t.Fatal("initial context used platform grants", string(raw))
			}
			for _, tool := range []string{"project_version_list", "project_timeline", "repository_read", "repository_info", "read"} {
				if _, e = m.callTool(c, system, r, "denied", tool, json.RawMessage(`{}`)); !errors.Is(e, domain.ErrForbidden) {
					t.Fatalf("%s used platform grants: %v", tool, e)
				}
			}
		})
	}
	// RPC authorization is reconstructed on every call, not captured at admission.
	p.Memberships[0].Roles = []string{}
	p.AccessVersion++
	if e := db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	if _, e := m.callTool(c, system, r, "revoked", "project_context", json.RawMessage(`{}`)); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("revoked manual analysis retained tool access", e)
	}
	// A genuine system run still has its internal processing capabilities.
	r.OwnerID, r.OwnerKind = "", ""
	if _, e := m.callTool(c, system, r, "internal", "project_version_list", json.RawMessage(`{}`)); e != nil {
		t.Fatal("background processing lost its system boundary", e)
	}
}

func TestManualSandboxRunStopsWhenSourcePageIsRevoked(t *testing.T) {
	m, db, origin, _ := collaborationToolsFixture(t)
	c := context.Background()
	p, _ := db.GetProject(c, origin.ProjectID)
	p.Name, p.AgentReadMode = "Sandbox permission", "sandbox"
	p.ProjectRoles = append(p.ProjectRoles, domain.ProjectRole{ID: "manual-source", Name: "Manual", Pages: []string{"tasks", "code"}, Version: 1})
	p.Memberships[0].Roles = []string{"manual-source"}
	p.AccessVersion++
	if e := db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	if e := db.PutProfile(c, domain.Profile{ID: "profile", Name: "Fixture", Model: "fixture", BaseURL: "http://unused"}); e != nil {
		t.Fatal(e)
	}
	if e := db.PutPlatformSettings(c, domain.PlatformSettings{ProfileID: "profile"}); e != nil {
		t.Fatal(e)
	}
	worker := filepath.Join(t.TempDir(), "unused-worker.mjs")
	if e := os.WriteFile(worker, nil, 0600); e != nil {
		t.Fatal(e)
	}
	m.Worker = worker
	m.Git = &gitrepo.Repositories{Root: filepath.Join(t.TempDir(), "repos"), App: m.App}
	socketDir, e := os.MkdirTemp("/tmp", "pm-access-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	m.SandboxSocket = filepath.Join(socketDir, "runner.sock")
	listener, e := net.Listen("unix", m.SandboxSocket)
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	started, stopped := make(chan struct{}), make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		decoder, encoder := json.NewDecoder(conn), json.NewEncoder(conn)
		var request sandbox.Request
		if err = decoder.Decode(&request); err != nil {
			errCh <- err
			return
		}
		if err = encoder.Encode(map[string]bool{"ready": true}); err != nil {
			errCh <- err
			return
		}
		var message map[string]any
		if err = decoder.Decode(&message); err != nil {
			errCh <- err
			return
		}
		close(started)
		if err = decoder.Decode(&message); err != nil {
			errCh <- err
			return
		}
		if message["type"] == "stop" {
			close(stopped)
		}
	}()
	r, e := m.StartAs(c, domain.Actor{ID: origin.OwnerID, Kind: "human"}, p.ID, "", "progress", "Read source", "")
	if e != nil {
		t.Fatal(e)
	}
	if r.OwnerID != origin.OwnerID || r.CodeSnapshot == nil {
		t.Fatal("manual run was not bound to owner/sandbox", r)
	}
	select {
	case <-started:
	case e := <-errCh:
		t.Fatal(e)
	case <-time.After(5 * time.Second):
		t.Fatal("sandbox did not start")
	}
	p, _ = db.GetProject(c, p.ID)
	p.ProjectRoles[len(p.ProjectRoles)-1].Pages = []string{"tasks"}
	p.AccessVersion++
	if e = db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	select {
	case <-stopped:
	case e := <-errCh:
		t.Fatal(e)
	case <-time.After(4 * time.Second):
		t.Fatal("native worker retained revoked source access")
	}
}
