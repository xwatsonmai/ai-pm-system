package agent

import (
	"ai-pm-system/internal/domain"
	"encoding/json"
	"testing"
)

func TestSandboxToolsAndSnapshotBinding(t *testing.T) {
	snapshot := &domain.CodeSnapshot{Status: "ready", SHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	for _, kind := range []string{"question", "triage", "requirement_review"} {
		r := domain.Run{Kind: kind, CodeSnapshot: snapshot}
		names := map[string]bool{}
		for _, tool := range toolsFor(r) {
			names[tool.Name] = true
		}
		if names["repository_read"] || !names["repository_info"] || !names["repository_history"] {
			t.Fatal(kind, names)
		}
		if names["create_bug"] != (kind == "triage") {
			t.Fatal("bug tool leaked", kind, names)
		}
		if kind == "requirement_review" && (names["save_analysis"] || !names["submit_requirement_review"]) {
			t.Fatal(names)
		}
	}
	r := domain.Run{Kind: "question", TriggerType: "collaboration_result", CodeSnapshot: snapshot}
	for _, tool := range toolsFor(r) {
		if !readOnlyTool(tool.Name) {
			t.Fatal("continuation write tool", tool.Name)
		}
	}
	// Ensure the new audit shape round-trips through the existing JSON storage.
	r.Steps = []domain.Step{{ID: "call_1", Name: "read", Path: "server/services/photo-compositor/service.py", SHA: snapshot.SHA, Arguments: json.RawMessage(`{"offset":316,"limit":12}`), Details: json.RawMessage(`{"truncation":{"truncated":false}}`)}}
	b, _ := json.Marshal(r)
	var restored domain.Run
	if e := json.Unmarshal(b, &restored); e != nil || restored.CodeSnapshot.SHA != snapshot.SHA || restored.Steps[0].ID != "call_1" {
		t.Fatal(e, string(b))
	}
}
