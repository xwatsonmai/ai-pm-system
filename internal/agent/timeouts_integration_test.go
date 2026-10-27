package agent

import (
	"ai-pm-system/internal/domain"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPiModelTimeoutCancelsUpstream(t *testing.T) {
	worker, _ := filepath.Abs("../../runtime/worker.mjs")
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(worker), "node_modules/@earendil-works/pi-coding-agent")); err != nil {
		t.Skip("install runtime dependencies for Pi integration")
	}
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%v", partial), func(t *testing.T) {
			aborted := make(chan struct{}, 1)
			release := make(chan struct{})
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n")
				if partial {
					fmt.Fprint(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"已收到部分回答\"},\"finish_reason\":null}]}\n\n")
				}
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					aborted <- struct{}{}
				case <-release:
				}
			}))
			defer func() { close(release); model.Close() }()
			m, db, v, job := replyFixture(t)
			m.Worker = worker
			m.Timeouts = RunTimeouts{FirstResponse: 600 * time.Millisecond, StreamIdle: 600 * time.Millisecond, Total: 15 * time.Second}
			r := domain.Run{ID: job.RunID, ProjectID: v.ProjectID, ConversationID: v.ConversationID, OwnerID: v.OwnerID, OwnerKind: v.OwnerKind, Kind: "question", TriggerType: "collaboration_result", SourceEventID: v.Result.ID, CollaborationID: v.ID, Status: "queued"}
			ctx, cancel := m.runContext()
			defer cancel()
			m.execute(ctx, r, domain.Profile{Provider: "fixture", Model: "fixture", BaseURL: model.URL + "/v1", API: "openai-completions", APIKey: "fixture-key"})
			got, err := db.GetRun(context.Background(), r.ID)
			want := "首次响应超时"
			if partial {
				want = "流式响应超时"
				if got.Output != "已收到部分回答" {
					t.Fatal("partial response lost", got)
				}
			}
			if err != nil || got.Status != "failed" || !strings.Contains(got.Error, want) {
				t.Fatalf("wrong timeout: %+v %v", got, err)
			}
			select {
			case <-aborted:
			case <-time.After(time.Second):
				t.Fatal("timed-out provider request was not canceled")
			}
		})
	}
}

func TestWorkerTimeoutsPersistPartialReply(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	for _, scenario := range []string{"first", "idle", "total", "streaming", "tool-pause"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, v, job := replyFixture(t)
			m.Timeouts = RunTimeouts{FirstResponse: 300 * time.Millisecond, StreamIdle: 300 * time.Millisecond, Total: 5 * time.Second}
			if scenario == "total" {
				m.Timeouts.Total = time.Second
			}
			worker := filepath.Join(t.TempDir(), "worker.mjs")
			js := `import readline from 'node:readline';
const scenario = '` + scenario + `';
const emit = value => process.stdout.write(JSON.stringify(value)+'\n');
const rl = readline.createInterface({input:process.stdin});
rl.on('line', line => {
  const m=JSON.parse(line);
  if(m.type==='stop') { emit({type:'done',stopped:true}); process.exit(0); }
  if(m.type!=='start') return;
  emit({type:'model_start',id:'one'});
  emit({type:'assistant_start'});
  if(scenario==='first') return;
  emit({type:'model_activity',id:'one'});
  emit({type:'delta',text:'已有说明'});
  emit({type:'assistant_start'});
  emit({type:'delta',text:'部分回答'});
  if(scenario==='idle') return;
  const complete=()=>{emit({type:'model_end',id:'one'});emit({type:'done'});process.exit(0);};
  if(scenario==='tool-pause') {
    emit({type:'model_end',id:'one'});
    setTimeout(()=>{emit({type:'model_start',id:'two'});emit({type:'model_activity',id:'two'});emit({type:'delta',text:'工具后续答'});emit({type:'model_end',id:'two'});complete();},750);
    return;
  }
  let count=0;
  setInterval(()=>{emit({type:'model_activity',id:'one'});emit({type:'delta',text:'继续'});if(++count===10 && scenario==='streaming') complete();},75);
});`
			if err := os.WriteFile(worker, []byte(js), 0600); err != nil {
				t.Fatal(err)
			}
			m.Worker = worker
			r := domain.Run{ID: job.RunID, ProjectID: v.ProjectID, ConversationID: v.ConversationID, OwnerID: v.OwnerID, OwnerKind: v.OwnerKind, Kind: "question", TriggerType: "collaboration_result", SourceEventID: v.Result.ID, CollaborationID: v.ID, Status: "queued"}
			ctx, cancel := m.runContext()
			defer cancel()
			m.execute(ctx, r, domain.Profile{ID: "fixture"})
			got, err := db.GetRun(context.Background(), r.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantError := map[string]string{"first": "首次响应超时", "idle": "流式响应超时", "total": "总时限"}[scenario]
			if wantError == "" {
				if got.Status != "succeeded" || got.Error != "" {
					t.Fatalf("active work timed out: %+v", got)
				}
			} else if got.Status != "failed" || !strings.Contains(got.Error, wantError) {
				t.Fatalf("wrong timeout: %+v", got)
			}
			if scenario != "first" && (got.ProcessOutput != "已有说明" || !strings.HasPrefix(got.Output, "部分回答")) {
				t.Fatalf("partial content lost: %+v", got)
			}
			// Timeout is a failure, so the existing bounded reply retry applies.
			// It must not take the manual-stop path that immediately ends the job.
			job.Attempts = 1
			if err := m.finishReply(job); err != nil {
				t.Fatal(err)
			}
			saved, err := db.GetReplyJob(context.Background(), job.ID)
			wantStatus := "delivered"
			if wantError != "" {
				wantStatus = "pending"
			}
			if err != nil || saved.Status != wantStatus {
				t.Fatalf("wrong retry status: %+v %v", saved, err)
			}
			if scenario == "idle" {
				m.Worker = filepath.Join(t.TempDir(), "retry.mjs")
				if err := os.WriteFile(m.Worker, []byte(`import readline from 'node:readline';
readline.createInterface({input:process.stdin}).once('line',()=>{process.stdout.write(JSON.stringify({type:'done',text:'重试完成'})+'\n');process.exit(0);});`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := db.PutProfile(context.Background(), domain.Profile{ID: "retry-profile"}); err != nil {
					t.Fatal(err)
				}
				if err := db.PutPlatformSettings(context.Background(), domain.PlatformSettings{ProfileID: "retry-profile"}); err != nil {
					t.Fatal(err)
				}
				if err := m.admitReply(saved); err != nil {
					t.Fatal(err)
				}
				m.wg.Wait()
				retried, err := db.GetRun(context.Background(), r.ID)
				if err != nil || retried.Status != "succeeded" || retried.Output != "重试完成" || retried.ProcessOutput != "已有说明\n\n部分回答" || retried.OutputVersion <= got.OutputVersion {
					t.Fatalf("retry discarded prior partial work: %+v %v", retried, err)
				}
			}
		})
	}
}
