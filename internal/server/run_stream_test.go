package server

import (
	"ai-pm-system/internal/agent"
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunStreamDeliversDeltasReconnectsAndPersists(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, err := range []error{
		db.PutProject(ctx, domain.Project{ID: "p", Name: "Project", OwnerID: "local-owner"}),
		db.PutProfile(ctx, domain.Profile{ID: "model", Model: "fixture", APIKey: "fixture-only"}),
		db.PutPlatformSettings(ctx, domain.PlatformSettings{ProfileID: "model"}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	worker := filepath.Join(dir, "worker.mjs")
	if err = os.WriteFile(worker, []byte(`
import readline from "node:readline";
const input = readline.createInterface({input: process.stdin});
input.on("line", async (line) => {
  if (JSON.parse(line).type !== "start") return;
  let text = "";
  for (let n = 0; n < 12; n++) {
    await new Promise((resolve) => setTimeout(resolve, 60));
    text += "文字🙂";
    process.stdout.write(JSON.stringify({type:"delta", text:"文字🙂"}) + "\n");
  }
  process.stdout.write(JSON.stringify({type:"done", text}) + "\n");
  process.exit(0);
});`), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &app.Service{Store: db}
	m := agent.New(ctx, svc, nil, worker, dir)
	defer m.Close()
	s := &Server{App: svc, Agent: m}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	run, err := m.StartAs(ctx, domain.Actor{ID: "local-owner", Kind: "human"}, "p", "", "question", "输出测试", "")
	if err != nil {
		t.Fatal(err)
	}
	connect := func() (*http.Response, *bufio.Scanner) {
		t.Helper()
		request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/runs/"+run.ID+"/events", nil)
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || response.Header.Get("X-Accel-Buffering") != "no" || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			t.Fatalf("stream: %d %s", response.StatusCode, body)
		}
		return response, bufio.NewScanner(response.Body)
	}
	next := func(scanner *bufio.Scanner) runStreamUpdate {
		t.Helper()
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "data: ") {
				var update runStreamUpdate
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &update); err != nil {
					t.Fatal(err)
				}
				return update
			}
		}
		t.Fatalf("stream ended early: %v", scanner.Err())
		return runStreamUpdate{}
	}
	response, scanner := connect()
	first := next(scanner)
	if first.Append {
		t.Fatal("first event was not a snapshot")
	}
	text := first.Run.Output
	for text == "" {
		update := next(scanner)
		if update.Append {
			text += update.Run.Output
		} else {
			text = update.Run.Output
		}
	}
	response.Body.Close()
	response, scanner = connect()
	defer response.Body.Close()
	first = next(scanner)
	if first.Append || !strings.HasPrefix(first.Run.Output, text) {
		t.Fatal("reconnect lost received text")
	}
	text = first.Run.Output
	count := 1
	for first.Run.Status == "running" || first.Run.Status == "queued" {
		first = next(scanner)
		if first.Append {
			text += first.Run.Output
		} else {
			text = first.Run.Output
		}
		count++
	}
	if count < 5 || first.Run.Status != "succeeded" || text != strings.Repeat("文字🙂", 12) {
		t.Fatalf("updates=%d status=%s text=%q", count, first.Run.Status, text)
	}
	stored, err := db.GetRun(ctx, run.ID)
	if err != nil || stored.Output != text || stored.Status != "succeeded" {
		t.Fatalf("not persisted: %+v %v", stored, err)
	}
}
