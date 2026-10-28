// Real browser, HTTP and temporary SQLite; never connects to a live environment.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp,mkdir,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import net from 'node:net';
import {once} from 'node:events';
const load=createRequire(process.env.AI_PM_QA_NODE_MODULES?join(process.env.AI_PM_QA_NODE_MODULES,'package.json'):import.meta.url);
const {chromium}=load('playwright');
const output=resolve('test-results/release-ui');await mkdir(output,{recursive:true});
const data=await mkdtemp(join(tmpdir(),'ai-pm-release-'));
const reserve=net.createServer().listen(0,'127.0.0.1');await once(reserve,'listening');const port=reserve.address().port;await new Promise(done=>reserve.close(done));
const base=process.env.AI_PM_QA_BASE_URL || `http://127.0.0.1:${port}`;
if (new URL(base).hostname !== '127.0.0.1' || new URL(base).protocol !== 'http:') throw new Error('Only isolated loopback test services are allowed');
const server=process.env.AI_PM_QA_BASE_URL ? null : spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-requirements-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});const serverExit=server ? once(server,'exit') : null;server?.stderr.resume();
const browser=await chromium.launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),errors=[];
page.on('pageerror',e=>errors.push(e.message));
async function api(path,body,method){const r=await fetch(base+'/api'+path,{method:method||(body?'POST':'GET'),headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});const v=await r.json();assert(r.ok,JSON.stringify(v));return v;}
// Claiming is development work, so it only happens through the developer MCP.
let mcpSeq=0;
async function claim(token,args){const r=await fetch(base+'/mcp',{method:'POST',headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json',Accept:'application/json, text/event-stream','MCP-Protocol-Version':'2025-11-25'},body:JSON.stringify({jsonrpc:'2.0',id:++mcpSeq,method:'tools/call',params:{name:'task_claim',arguments:args}})});const v=await r.json();assert(r.ok&&!v.error&&!v.result.isError,JSON.stringify(v));return JSON.parse(v.result.content[0].text);}
const report={};
try {
 for(let n=0;n<100;n++){try{if((await fetch(base+'/api/health')).ok)break;}catch{}await new Promise(done=>setTimeout(done,50));}
 const project=await api('/projects',{name:'开发与上线分阶段验证',branch:'main'});
 let {task}=await api(`/projects/${project.id}/tasks`,{title:'订单退款状态查询',kind:'requirement',description:'提供可追溯的退款进度',acceptance:['退款状态及时更新'],request_id:'create'});
 const path=`/tasks/${task.id}`;
 await api(path+'/actions',{action:'ready',request_id:'ready'});
 const current=async()=> (await api(path)).task;
 await page.goto(base);await page.getByRole('button',{name:'需求池',exact:true}).click();
 await page.locator('.task-table tbody tr').click();await page.locator('.detail-title').waitFor();
 assert.equal(await page.getByRole('button',{name:'开始处理',exact:true}).count(),0,'web must not offer claiming');
 const developer=await api(`/projects/${project.id}/tokens`,{name:'开发客户端'});
 await claim(developer.secret,{task_id:task.id,request_id:'claim'});
 await page.goto(base);await page.getByRole('button',{name:'需求池',exact:true}).click();await page.locator('.task-table tbody tr').click();
 const submit=async version=>{
  await page.getByRole('button',{name:'提交开发结果',exact:true}).click();
  await page.locator('#submit-form [name=branch]').fill('feature/release');
  await page.locator('#submit-form [name=summary]').fill('退款状态查询已开发完成');
  await page.locator('#submit-form [name=sha]').fill(version);
  await page.locator('#submit-form [name=environment]').fill('local');
  await page.locator('#submit-form [name=tests]').fill('本地专项测试通过，尚未上线');
  await page.locator('#submit-form button[type=submit]').click();
  await page.getByRole('button',{name:'确认测试通过',exact:true}).click();
  await page.locator('#testing-form [name=note]').fill('产品验证通过');
  await page.locator('#testing-form button[type=submit]').click();
  await page.locator('#overlays').getByRole('button',{name:'确认已上线',exact:true}).waitFor();
 };
 await submit('release-v1');task=await current();assert.equal(task.status,'pending_release');assert(!task.deployment);
 assert.equal(await page.locator('#overlays').getByRole('button',{name:'开始验收',exact:true}).count(),0);
 await page.screenshot({path:join(output,'pending-desktop.png')});
 await page.getByRole('button',{name:'关闭',exact:true}).click();
 await page.locator('#task-filter').selectOption('pending_release');assert.equal(await page.locator('.task-table tbody tr').count(),1);
 await page.getByRole('button',{name:'验收与交付',exact:true}).click();
 assert.equal(await page.locator('#overlays').getByRole('button',{name:'开始验收',exact:true}).count(),0);
 await page.locator('#page').getByRole('button',{name:'确认已上线',exact:true}).click();
 await page.locator('#deployment-form [name=environment]').fill('国内生产');
 await page.locator('#deployment-form [name=body]').fill('2026-10-15 实际发布成功，发布记录 build-42');
 // Lose the response after the transaction commits; retry the same form once.
 let dropped=false;
 await page.route(`**/api${path}/actions`,async route=>{
  if(!dropped&&route.request().postDataJSON()?.action==='mark_deployed'){dropped=true;await route.fetch();await route.abort('failed');}
  else await route.continue();
 });
 await page.locator('#deployment-form button[type=submit]').click();
 await page.locator('#deployment-form .form-error').filter({hasText:/fetch|请求|网络|Failed/i}).waitFor();
 assert.equal(await page.locator('#deployment-form [name=body]').inputValue(),'2026-10-15 实际发布成功，发布记录 build-42');
 await page.locator('#deployment-form button[type=submit]').click();await page.locator('#overlays').getByRole('button',{name:'开始验收',exact:true}).waitFor();
 task=await current();assert.equal(task.status,'review');assert.equal(task.deployment.environment,'国内生产');
 let ctx=await api(path);assert.equal(ctx.events.filter(e=>e.kind==='mark_deployed').length,1);
 report.gate='开发提交经产品测试进入待上线、筛选与交付队列、未上线不可验收、上线丢失响应后幂等重试通过';
 await page.locator('#overlays').getByRole('button',{name:'开始验收',exact:true}).click();
 assert.equal(await page.locator('#review-form [name=environment]').inputValue(),'国内生产');
 assert(await page.locator('#review-form [name=environment]').getAttribute('readonly')!==null);
 await page.locator('#review-form [name=note]').fill('异常状态需要再修改');
 await page.getByRole('button',{name:'退回修改',exact:true}).click();await page.getByRole('button',{name:'提交开发结果',exact:true}).waitFor();
 await submit('release-v2');task=await current();assert.equal(task.status,'pending_release');assert(!task.deployment);
 await page.getByRole('button',{name:'退回开发',exact:true}).click();await page.locator('#rework-form [name=body]').fill('上线前补充回归');await page.locator('#rework-form button[type=submit]').click();
 await page.getByRole('button',{name:'提交开发结果',exact:true}).waitFor();await submit('release-v2');
 await page.locator('#overlays').getByRole('button',{name:'确认已上线',exact:true}).click();
 await page.setViewportSize({width:390,height:844});
 await page.waitForFunction(()=>{const el=document.querySelector('#deployment-form');if(!el)return false;const b=el.getBoundingClientRect();return b.left>=0&&b.right<=window.innerWidth;});
 await page.locator('#deployment-form [name=environment]').fill('国内生产');await page.locator('#deployment-form [name=body]').fill('版本 v2 实际发布成功，发布记录 build-43');
 await page.screenshot({path:join(output,'release-mobile.png')});
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 await page.locator('#deployment-form button[type=submit]').click();await page.locator('#overlays').getByRole('button',{name:'开始验收',exact:true}).waitFor();
 await page.locator('#overlays').getByRole('button',{name:'开始验收',exact:true}).click();await page.locator('#review-form [name=check_0]').check();await page.locator('#review-form [name=note]').fill('线上验收通过');await page.getByRole('button',{name:'确认验收通过',exact:true}).click();
 await page.locator('.detail-meta .badge.done').waitFor();task=await current();assert.equal(task.status,'done');assert.equal(task.deployment.sha,'release-v2');
 ctx=await api(path);assert.equal(ctx.events.filter(e=>e.kind==='mark_deployed').length,2);assert(ctx.events.some(e=>e.kind==='rework'));
 await page.reload();
 await page.getByRole('button',{name:'打开导航',exact:true}).click();await page.getByRole('button',{name:'需求池',exact:true}).click();await page.locator('#task-filter').selectOption('done');await page.locator('.task-table tbody tr').click();
 await page.getByRole('heading',{name:'最近一次上线 · R1',exact:true}).waitFor();
 assert.match(await page.locator('.drawer').innerText(),/build-43/);
 await page.getByRole('button',{name:'关闭',exact:true}).click();
 await page.getByRole('button',{name:'打开导航',exact:true}).click();await page.getByRole('button',{name:'项目概览',exact:true}).click();
 await page.locator('.stats').waitFor();
 assert.equal(await page.locator('.stat').count(),6);assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 report.lifecycle='验收退回、待上线退回开发、重新提交必须重新上线、线上环境验收与刷新持久化通过';
 report.mobile='390px 上线表单、需求详情及六项概览统计无横向溢出';
 assert.deepEqual(errors,[]);console.log(JSON.stringify(report,null,2));
} catch(error) {await page.screenshot({path:join(output,'failure.png'),fullPage:true}).catch(()=>{});throw error;}
finally {await browser.close();server?.kill('SIGTERM');if(serverExit)await serverExit;await rm(data,{recursive:true,force:true});}
