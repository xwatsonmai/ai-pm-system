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
)

// A submission that breaks the conversation rules is returned to the model as a
// tool error within the same round; only the corrected submission is saved.
func TestReviewSubmissionRejectedWithinRoundThenCorrected(t *testing.T) {
	dir := t.TempDir()
	errorsFile := filepath.Join(dir, "tool-errors.txt")
	encoded, _ := json.Marshal(errorsFile)
	script := `import fs from 'node:fs'; import readline from 'node:readline';
 const emit=x=>process.stdout.write(JSON.stringify(x)+'\n'); let attempt=0;
 const agenda=[{id:'mode',title:'改价方式',kind:'direction',status:'open'},{id:'items',title:'系统配置哪些项',kind:'detail',status:'open'}];
 const q=id=>({id,label:'点数要支持小数吗？',type:'radio',options:['支持两位小数','只支持整数'],recommended:[],reason:'',required:false});
 const submit=args=>emit({type:'tool_call',id:'review'+(++attempt),name:'submit_requirement_review',args});
 readline.createInterface({input:process.stdin}).on('line',line=>{const m=JSON.parse(line);
  if(m.type==='start'){fs.writeFileSync(ERRORS,JSON.stringify(m.review_context.agenda||null)+'\n');
   submit({reply:'整理了 4 个细节问题。',decision:'clarify',agenda,ask:{topic_id:'items',kind:'form',questions:[q('a'),q('b'),q('c'),q('d')]}});return;}
  if(m.type==='tool_result'&&m.error){fs.appendFileSync(ERRORS,m.error+'\n');
   submit({reply:'先确认改价方式。',decision:'clarify',agenda,ask:{topic_id:'mode',kind:'direction',question:'保存后是一次性改价，还是长期锁定？',quick_replies:['一次性改价','长期锁定']}});return;}
  if(m.type==='tool_result'){emit({type:'done',text:'评审已提交'});process.exit(0);}
  if(m.type==='stop'){emit({type:'done',stopped:true});process.exit(0);}
 });`
	script = strings.ReplaceAll(script, "ERRORS", string(encoded))
	worker := filepath.Join(dir, "worker.mjs")
	if err := os.WriteFile(worker, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(filepath.Join(dir, "pm.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := context.Background()
	s := &app.Service{Store: db}
	profile, err := s.SaveProfile(c, domain.Profile{Name: "fixture", Provider: "fixture", Model: "fixture", BaseURL: "http://127.0.0.1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutPlatformSettings(c, domain.PlatformSettings{ProfileID: profile.ID}); err != nil {
		t.Fatal(err)
	}
	m := agent.New(c, s, nil, worker, dir)
	t.Cleanup(func() { m.Close(); db.Close() })
	p, _ := s.SaveProject(c, domain.Project{Name: "模板价格"})
	task, _ := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "模板设置价格", Description: "系统设置增加模板价格配置", RequestID: "create"})
	if _, err = s.SaveRequirementReview(c, app.Human(), task.Task.ID, app.RequirementReviewInput{Action: "start", RequestID: "start"}); err != nil {
		t.Fatal(err)
	}
	m.KickRequirementReview(task.Task.ID)
	var v domain.RequirementReview
	queueWait(t, func() bool {
		v, err = s.RequirementReview(c, app.Human(), task.Task.ID)
		return err == nil && v.Status != "queued" && v.Status != "running"
	})
	log, _ := os.ReadFile(errorsFile)
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(lines) != 2 || lines[0] != "[]" || !strings.Contains(lines[1], "方向性话题") {
		t.Fatalf("model did not receive the agenda and the rule it broke: %q", lines)
	}
	last := v.Messages[len(v.Messages)-1]
	if v.Status != "needs_input" || len(v.Agenda) != 2 || last.Body != "先确认改价方式。" || last.Result.Ask == nil || last.Result.Ask.Kind != "direction" || len(last.Result.Questions) != 0 {
		t.Fatalf("corrected submission not saved: %+v", v)
	}
	for _, message := range v.Messages {
		if strings.Contains(message.Body, "4 个细节问题") {
			t.Fatal("rejected submission leaked into the conversation")
		}
	}
	// Each tool call keeps a readable label for the progress shown while a round runs.
	run, err := s.Store.GetRun(c, v.RunID)
	if err != nil || len(run.Steps) != 2 || run.Steps[0].Label != "整理回复" || run.Steps[0].Status != "failed" || run.Steps[1].Label != "整理回复" || run.Steps[1].Status != "done" {
		t.Fatalf("step labels not recorded: %+v %v", run.Steps, err)
	}
}
