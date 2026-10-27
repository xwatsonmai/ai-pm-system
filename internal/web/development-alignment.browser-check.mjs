// Real browser + local HTTP/MCP + SQLite. No external model or service.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp,mkdir,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import net from 'node:net';
import {once} from 'node:events';
const load=createRequire(process.env.AI_PM_QA_NODE_MODULES?join(process.env.AI_PM_QA_NODE_MODULES,'package.json'):import.meta.url);
const {chromium}=load('playwright');
const output=resolve('test-results/development-alignment-ui');await mkdir(output,{recursive:true});
const data=await mkdtemp(join(tmpdir(),'ai-pm-alignment-'));
const reserve=net.createServer().listen(0,'127.0.0.1');await once(reserve,'listening');const port=reserve.address().port;await new Promise(done=>reserve.close(done));
const base=`http://127.0.0.1:${port}`;
const server=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm'),['-addr',`127.0.0.1:${port}`,'-data',data,'-pi-worker',join(data,'missing-worker'),'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});
const serverExit=once(server,'exit');server.stderr.resume();
const browser=await chromium.launch({headless:true});
const page=await browser.newPage({viewport:{width:1440,height:1000},reducedMotion:'reduce'});
const errors=[];page.on('pageerror',e=>errors.push(e.message));
async function api(path,body,method){const r=await fetch(base+'/api'+path,{method:method||(body?'POST':'GET'),headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});const result=await r.json();assert(r.ok,JSON.stringify(result));return result;}
let sequence=0;
async function mcp(token,name,args={},wantError=false){
  const r=await fetch(base+'/mcp',{method:'POST',headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json',Accept:'application/json, text/event-stream','MCP-Protocol-Version':'2025-11-25'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method:'tools/call',params:{name,arguments:args}})});
  const response=await r.json();assert(r.ok,JSON.stringify(response));assert(!response.error,JSON.stringify(response.error));
  assert.equal(Boolean(response.result.isError),wantError,JSON.stringify(response.result));
  return wantError?response.result:JSON.parse(response.result.content[0].text);
}
const report={};
try {
  for(let i=0;i<100;i++){try{if((await fetch(base+'/api/health')).ok)break;}catch{}await new Promise(done=>setTimeout(done,50));}
  const project=await api('/projects',{name:'开发对齐验证',branch:'main'});
  const original=await api(`/projects/${project.id}/tokens`,{name:'原开发客户端'});
  const replacement=await api(`/projects/${project.id}/tokens`,{name:'其他开发客户端'});
  let made=await api(`/projects/${project.id}/tasks`,{title:'售后记录展示',description:'按订单展示售后记录，保留历史。',acceptance:['可按订单查询售后记录'],request_id:'create'});
  made=await api(`/tasks/${made.task.id}/actions`,{action:'ready',request_id:'ready'});
  let mutation=await mcp(original.secret,'task_claim',{task_id:made.task.id,request_id:'claim',branch:'codex/alignment-check'});
  let credential=mutation.claim_id;
  const questions=[
    {id:'scope',label:'覆盖哪些订单？',type:'radio',options:['全部订单','线上订单'],required:true},
    {id:'roles',label:'哪些角色可看？',type:'multiselect',options:['客服','财务'],required:true},
    {id:'channel',label:'展示位置？',type:'select',options:['订单详情','单独页面'],required:true},
    {id:'name',label:'入口名称？',type:'text',required:true},
    {id:'edge',label:'特殊情况？',type:'textarea',required:true},
  ];
  async function ask(){const {task}=await api(`/tasks/${made.task.id}`);return mcp(original.secret,'task_request_alignment',{task_id:task.id,request_id:`ask-${++sequence}`,claim_id:credential,version:String(task.version),revision:String(task.revision),body:'开发时发现需要确认订单范围和展示入口。',questions:JSON.stringify(questions)});}
  await ask();
  await page.goto(base);await page.getByRole('button',{name:'需求池',exact:true}).click();
  await page.locator('.task-table .review-attention').filter({hasText:'待对齐'}).click();
  let form=page.locator('.review-question-form').last();await form.waitFor();
  assert.equal(await form.locator('fieldset').count(),5);assert.equal(await form.locator('input:checked').count(),0);
  await form.getByRole('radio',{name:'线上订单'}).check();
  await page.locator('#review-input').fill('你理解有偏差：只需要展示售后记录，暂不修改退款。');
  await page.getByRole('button',{name:'关闭',exact:true}).click();
  await page.locator('.task-table .review-attention').filter({hasText:'待对齐'}).click();
  await page.locator('.review-question-form').waitFor();
  assert(await page.getByRole('radio',{name:'线上订单'}).isChecked());
  assert.match(await page.locator('#review-input').inputValue(),/理解有偏差/);
  await page.screenshot({path:join(output,'alignment-partial-and-correction.png')});
  // One answered field is enough despite the other four being marked key questions.
  await page.locator('.review-send').click();
  await page.getByText('回复已提交，等待开发确认',{exact:true}).waitFor();
  assert.equal((await api(`/tasks/${made.task.id}`)).task.status,'ready');
  assert.equal((await mcp(replacement.secret,'work_list')).tasks.length,0);
  report.partial='五类表单、部分回答加自由纠正、关闭重开保留草稿、提交后待原开发确认通过';
  await ask();
  await page.locator('.review-question-card[data-status=active]').waitFor();
  assert.equal(await page.locator('.review-question-form input:checked').count(),0);
  await page.locator('#review-input').fill('这一轮不用表单，我的意思是按订单号查看所有售后记录。');
  await page.locator('.review-send').click();
  await page.getByText('回复已提交，等待开发确认',{exact:true}).waitFor();
  let context=await mcp(original.secret,'task_get_context',{task_id:made.task.id});
  assert(context.requirement_review.messages.some(m=>m.body.includes('这一轮不用表单')));
  const resumed=await mcp(original.secret,'task_resume_development',{task_id:made.task.id,request_id:'resume',claim_id:credential,version:String(context.task.version),revision:String(context.task.revision),body:'已理解为按订单号展示售后记录，可以继续开发。'});
  credential=resumed.claim_id;
  await page.locator('.review-composer-note').filter({hasText:'开发已确认对齐完成'}).waitFor();
  await page.locator('.rc-event').filter({hasText:'开发代理确认对齐完成'}).waitFor();
  assert.equal(await page.locator('#review-input').getAttribute('readonly'),'');
  report.resume='完全跳过表单、多轮追问、MCP读取完整回复并确认继续开发通过';
  await ask();
  await page.locator('.review-question-card[data-status=active]').waitFor();
  await page.setViewportSize({width:390,height:844});
  await page.locator('#review-input').fill('范围保持不变，按上轮结论继续。');
  await page.screenshot({path:join(output,'alignment-mobile.png')});
  assert(await page.evaluate(()=>document.querySelector('dialog').scrollWidth<=innerWidth));
  await page.locator('.review-send').click();await page.getByText('回复已提交，等待开发确认',{exact:true}).waitFor();
  await page.setViewportSize({width:1440,height:1000});
  await page.locator('[data-review-action=details]').first().click();
  await page.getByRole('button',{name:'重新开放领取',exact:true}).click();
  await page.getByRole('button',{name:'开始处理',exact:true}).waitFor();
  context=await api(`/tasks/${made.task.id}`);
  assert.equal(context.task.status,'ready');assert.equal(context.task.alignment,undefined);
  assert.match(context.task.description,/需求评审对齐补充/);assert.match(context.task.description,/这一轮不用表单/);
  assert.match(context.task.description,/线上订单/);
  assert.equal((context.task.description.match(/## 需求评审对齐补充/g)||[]).length,1);
  await page.screenshot({path:join(output,'alignment-reopened-with-history.png')});
  const claimed=await mcp(replacement.secret,'task_claim',{task_id:made.task.id,request_id:'replacement-claim'});
  assert.equal(claimed.task.status,'doing');assert.match(claimed.task.description,/暂不修改退款/);
  await mcp(original.secret,'task_report_work',{task_id:made.task.id,request_id:'old-write',claim_id:credential,body:'旧客户端晚到写入'},true);
  report.reopen='产品一键重新开放领取、历轮问题及回复追加正文、其他客户端领取、原凭据撤销通过';
  assert.deepEqual(errors,[]);await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report,null,2));
} catch(e) {await page.screenshot({path:join(output,'failure.png'),fullPage:true});console.error('Browser errors:',errors);throw e;}
finally {await browser.close();server.kill('SIGTERM');await serverExit;await rm(data,{recursive:true,force:true});}
