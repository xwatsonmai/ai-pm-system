package agent_test

import (
	"ai-pm-system/internal/agent"
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A controllable worker exercises admission, cancellation and persistence without
// involving a paid model or conflating provider latency with queue behaviour.
func queueFixture(t *testing.T) (*app.Service, *agent.Manager, string, string) {
	t.Helper()
	dir := t.TempDir()
	control := filepath.Join(dir, "control")
	_ = os.Mkdir(control, 0700)
	encoded, _ := json.Marshal(control)
	script := `import fs from 'node:fs'; import path from 'node:path'; import readline from 'node:readline';
 const control=CONTROL; const input=readline.createInterface({input:process.stdin});let timer;
 const emit=x=>process.stdout.write(JSON.stringify(x)+'\n');
 const finish=()=>{clearInterval(timer);input.close();process.stdin.unref()};
 input.on('line',line=>{const c=JSON.parse(line);
 if(c.type==='start') {const task=c.review_context.requirement;fs.writeFileSync(path.join(control,task.id+'.started'),JSON.stringify(c));
 timer=setInterval(()=>{if(!fs.existsSync(path.join(control,task.id+'.release')))return;clearInterval(timer);
 if(task.title==='fail'){emit({type:'error',error:'fixture failure'});emit({type:'done'});finish();return;}
 emit({type:'tool_call',id:'result',name:'submit_requirement_review',args:{reply:'请补充业务范围',decision:'clarify',agenda:[{id:'scope',title:'业务范围',kind:'direction',status:'open'}],ask:{topic_id:'scope',kind:'direction',question:'这次要覆盖哪些业务？'}}});},10);
 } else if(c.type==='tool_result'){emit({type:'done',text:'本轮已保存'});finish();}
 else if(c.type==='stop'){emit({type:'done',stopped:true});finish();}
 });`
	script = strings.Replace(script, "CONTROL", string(encoded), 1)
	worker := filepath.Join(dir, "worker.mjs")
	if e := os.WriteFile(worker, []byte(script), 0600); e != nil {
		t.Fatal(e)
	}
	db, e := sqlite.Open(filepath.Join(dir, "pm.db"))
	if e != nil {
		t.Fatal(e)
	}
	s := &app.Service{Store: db}
	profile, e := s.SaveProfile(context.Background(), domain.Profile{Name: "fixture", Provider: "fixture", Model: "fixture", BaseURL: "http://127.0.0.1"}, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.PutPlatformSettings(context.Background(), domain.PlatformSettings{ProfileID: profile.ID}); e != nil {
		t.Fatal(e)
	}
	m := agent.New(context.Background(), s, nil, worker, dir)
	t.Cleanup(func() { m.Close(); db.Close() })
	return s, m, control, worker
}

func queueWait(t *testing.T, check func() bool) {
	t.Helper()
	for end := time.Now().Add(12 * time.Second); time.Now().Before(end); {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("queue condition timed out")
}

func TestProjectReviewQueueSerializesRoundsAndContinuesAfterFailure(t *testing.T) {
	s, m, control, _ := queueFixture(t)
	c := context.Background()
	a := app.Human()
	p, _ := s.SaveProject(c, domain.Project{Name: "same"})
	other, _ := s.SaveProject(c, domain.Project{Name: "different"})
	create := func(project, title string) domain.Task {
		t.Helper()
		v, e := s.CreateTask(c, a, project, app.TaskInput{Title: title, Description: title, RequestID: title})
		if e != nil {
			t.Fatal(e)
		}
		return v.Task
	}
	first, second, third, foreign := create(p.ID, "first"), create(p.ID, "fail"), create(p.ID, "third"), create(other.ID, "foreign")
	start := func(task domain.Task) {
		t.Helper()
		if _, e := s.SaveRequirementReview(c, a, task.ID, app.RequirementReviewInput{Action: "start", RequestID: "start-" + task.ID}); e != nil {
			t.Fatal(e)
		}
		m.KickRequirementReview(task.ID)
	}
	started := func(id string) bool { _, e := os.Stat(filepath.Join(control, id+".started")); return e == nil }
	release := func(id string) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(control, id+".release"), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	state := func(id, want string) bool { v, e := s.RequirementReview(c, a, id); return e == nil && v.Status == want }
	start(first)
	queueWait(t, func() bool { return started(first.ID) })
	start(second)
	start(third)
	for _, text := range []string{"第一条补充", "最新补充"} {
		if _, e := s.SaveRequirementReview(c, a, second.ID, app.RequirementReviewInput{Action: "message", Body: text, RequestID: text}); e != nil {
			t.Fatal(e)
		}
		m.KickRequirementReview(second.ID)
	}
	start(foreign)
	queueWait(t, func() bool { return started(foreign.ID) })
	if started(second.ID) || started(third.ID) || !state(second.ID, "queued") {
		t.Fatal("same project ran concurrently")
	}
	release(foreign.ID)
	queueWait(t, func() bool { return state(foreign.ID, "needs_input") })
	release(first.ID)
	queueWait(t, func() bool { return started(second.ID) })
	if !state(first.ID, "needs_input") || started(third.ID) {
		t.Fatal("human wait retained project slot or FIFO was lost")
	}
	b, _ := os.ReadFile(filepath.Join(control, second.ID+".started"))
	if !strings.Contains(string(b), "最新补充") {
		t.Fatal("queued supplements were lost")
	}
	release(second.ID)
	queueWait(t, func() bool { return started(third.ID) })
	if !state(second.ID, "failed") {
		t.Fatal("failure blocked queue or vanished")
	}
	release(third.ID)
	queueWait(t, func() bool { return state(third.ID, "needs_input") })
}

func TestQueuedReviewSurvivesDatabaseReopenAndRecovery(t *testing.T) {
	s, m, control, worker := queueFixture(t)
	c := context.Background()
	a := app.Human()
	p, _ := s.SaveProject(c, domain.Project{Name: "restart"})
	task, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "resume", RequestID: "create"})
	saved, e := s.SaveRequirementReview(c, a, task.Task.ID, app.RequirementReviewInput{Action: "message", RequestID: "message", Body: "停机前已保存"})
	if e != nil {
		t.Fatal(e)
	}
	m.Close()
	// Re-open a separate DB handle to prove the queue is not reconstructed from
	// manager memory. It must preserve the stable conversation and queued order.
	dir := filepath.Dir(worker)
	db, e := sqlite.Open(filepath.Join(dir, "pm.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	recovered := agent.New(c, &app.Service{Store: db}, nil, worker, dir)
	defer recovered.Close()
	if e = recovered.Recover(c); e != nil {
		t.Fatal(e)
	}
	queueWait(t, func() bool { _, e := os.Stat(filepath.Join(control, task.Task.ID+".started")); return e == nil })
	loaded, _ := db.GetRequirementReview(c, task.Task.ID)
	if loaded.ConversationID != saved.ConversationID || loaded.QueuedAt != saved.QueuedAt || loaded.Messages[len(loaded.Messages)-1].Body != "停机前已保存" {
		t.Fatal(loaded)
	}
	_ = os.WriteFile(filepath.Join(control, task.Task.ID+".release"), nil, 0600)
	queueWait(t, func() bool { v, _ := db.GetRequirementReview(c, task.Task.ID); return v.Status == "needs_input" })
}
