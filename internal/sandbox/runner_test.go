package sandbox

import (
	"ai-pm-system/internal/domain"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runnerFixture(t *testing.T) (*Runner, Request) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	r, e := New(Config{DataRoot: root, Image: "worker:fixed"})
	if e != nil {
		t.Fatal(e)
	}
	q := Request{ProjectID: "prj_a", ConversationID: "conv_a", RunID: "run_a", SnapshotKey: strings.Repeat("a", 40)}
	dir := filepath.Join(root, "snapshots", q.ProjectID, q.SnapshotKey)
	if e = os.MkdirAll(filepath.Join(dir, "source"), 0755); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(domain.CodeSnapshot{ProjectID: q.ProjectID, Key: q.SnapshotKey, SHA: q.SnapshotKey, Status: "ready"})
	if e = os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0444); e != nil {
		t.Fatal(e)
	}
	return r, q
}

func TestRunnerConstrainsMountsAndContainer(t *testing.T) {
	r, q := runnerFixture(t)
	source, state, e := r.mounts(q)
	if e != nil {
		t.Fatal(e)
	}
	args := strings.Join(r.createArgs(q, source, state), "\n")
	for _, want := range []string{"--read-only", "--cap-drop\nALL", "--user\n1000:1000", "--memory\n1g", "--pids-limit\n128", "dst=/workspace,readonly", "dst=/state/sessions", "--pull=never"} {
		if !strings.Contains(args, want) {
			t.Fatal("missing constraint", want)
		}
	}
	for _, bad := range []string{"docker.sock", "--privileged", "--network\nhost", "/app/data"} {
		if strings.Contains(args, bad) {
			t.Fatal("unexpected authority", bad)
		}
	}
	q.ProjectID = "../prj_a"
	if _, _, e = r.mounts(q); e == nil {
		t.Fatal("path traversal accepted")
	}
	q.ProjectID = "prj_b"
	if _, _, e = r.mounts(q); e == nil {
		t.Fatal("other project snapshot accepted")
	}
	q.ProjectID = "prj_a"
	q.SnapshotKey = "main"
	if _, _, e = r.mounts(q); e == nil {
		t.Fatal("mutable ref accepted")
	}
}

func TestRunnerRejectsSymlinkedState(t *testing.T) {
	r, q := runnerFixture(t)
	if _, _, e := r.mounts(q); e != nil {
		t.Fatal(e)
	}
	state := filepath.Join(r.Config.DataRoot, "pi", q.ProjectID, "sessions", q.ConversationID)
	if e := os.Remove(state); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(t.TempDir(), state); e != nil {
		t.Fatal(e)
	}
	if _, _, e := r.mounts(q); e == nil {
		t.Fatal("linked private state accepted")
	}
}
