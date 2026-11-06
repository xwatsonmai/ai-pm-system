// Temporary SQLite + loopback Go service + Chromium. No live data or model calls.
import assert from 'node:assert/strict';
import {spawn,execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp,mkdir,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {once} from 'node:events';
import net from 'node:net';
const load=createRequire(process.env.AI_PM_QA_NODE_MODULES?join(process.env.AI_PM_QA_NODE_MODULES,'package.json'):import.meta.url);
const {chromium}=load('playwright');
const output=resolve('test-results/task-hierarchy');await mkdir(output,{recursive:true});
const data=await mkdtemp(join(tmpdir(),'ai-pm-tree-'));
const reservation=net.createServer().listen(0,'127.0.0.1');await once(reservation,'listening');
const port=reservation.address().port;await new Promise(done=>reservation.close(done));const base=`http://127.0.0.1:${port}`;
const server=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-hierarchy-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});
let log='';server.stderr.on('data',chunk=>log+=chunk);const stopped=once(server,'exit');
const browser=await chromium.launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000}});
const errors=[],requests=[];page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>requests.push(r.url()));
async function api(path,body){const r=await fetch(base+'/api'+path,{method:body?'POST':'GET',headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const v=await r.json();assert(r.ok,JSON.stringify(v));return v;}
const sql=(code,...args)=>execFileSync('python3',['-c',`import sqlite3,json,sys,time\ndb=sqlite3.connect(sys.argv[1])\n${code}\ndb.commit()`,join(data,'ai-pm.db'),...args]);
const rows=()=>page.locator('[data-task-row][data-action=open-task]');
try{
 for(let i=0;i<100;i++){try{if((await fetch(base+'/api/health')).ok)break;}catch{}await new Promise(done=>setTimeout(done,50));}
 const project=await api('/projects',{name:'层级需求验证',branch:'main'});
 const created=await api(`/projects/${project.id}/tasks`,{title:'订单处理主需求',description:'主需求正文',request_id:'tree-root'}),root=created.task;
 sql(`
root=json.loads(sys.argv[2]);pid=root['project_id'];now=int(time.time()*1000)
root.update(status='done',updated_at=now-1000)
db.execute('UPDATE tasks SET payload=? WHERE id=?',(json.dumps(root),root['id']))
def add(id,title,**extra):
 t=dict(id=id,project_id=pid,title=title,description='PRIVATE_BODY'*100,kind='requirement',status='ready',priority='next',revision=1,version=1,project_version_id=root['project_version_id'],created_at=now-1000,updated_at=now-2000)
 t.update(extra)
 db.execute("INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,1,'',?)",(id,pid,json.dumps(t)))
for i in range(45):add('tree-bug-%02d'%i,'订单关联 Bug %02d'%i,kind='bug',parent_requirement_id=root['id'],updated_at=now+i)
add('tree-iteration','订单追加迭代',iteration=dict(parent_task_id=root['id'],root_task_id=root['id'],source=dict(description='FROZEN_PRIVATE_BODY')),updated_at=now+200)
add('tree-grandchild','支付金额校验问题',kind='bug',parent_requirement_id='tree-iteration',updated_at=now+500)
for i in range(25):add('tree-other-%02d'%i,'独立需求 %02d'%i)
`,JSON.stringify(root));
 const first=await api(`/projects/${project.id}/tasks?view=tree`);assert.equal(first.total,26);assert.equal(first.matching_total,73);assert.equal(first.items[0].children_count,46);assert.equal(first.items[0].description,'');
 await page.goto(base);await page.getByRole('button',{name:'需求池',exact:true}).click();await page.waitForFunction(()=>document.querySelectorAll('[data-task-row][data-action=open-task]').length===20);
 assert.equal(await page.locator('[data-action=task-tree-toggle][aria-expanded=false]').count(),1);
 assert(!requests.some(url=>url.includes('tree_parent_id=')),'collapsed branches requested children');
 await page.screenshot({path:join(output,'collapsed-desktop.png')});
 const rootToggle=()=>page.locator(`[data-action=task-tree-toggle][data-id="${root.id}"]`);
 await rootToggle().focus();await page.keyboard.press('Enter');await page.locator('[data-task-row][data-id=tree-iteration]').waitFor();assert.equal(await page.locator('dialog').count(),0,'toggle opened detail');
 assert.equal(await rows().count(),40);assert.equal(await page.locator('[aria-label=子项分页]').count(),1);
 const iterationToggle=page.locator('[data-action=task-tree-toggle][data-id=tree-iteration]');
 await iterationToggle.click();await page.locator('[data-task-row][data-id=tree-grandchild]').waitFor();assert.equal(await rows().count(),41);
 assert.match(await page.locator('[data-task-row][data-id=tree-iteration]').innerText(),/追加迭代/);assert.match(await page.locator('[data-task-row][data-id=tree-grandchild]').innerText(),/关联 Bug/);
 await page.screenshot({path:join(output,'expanded-desktop.png')});
 assert.equal(await page.locator('#task-table input[type=checkbox]').count(),0,'browsing exposes batch-selection controls');
 await rootToggle().click();assert.equal(await rows().count(),20);
 await page.getByRole('button',{name:'安排目标版本',exact:true}).click();await page.locator('#pv-assign-form [data-selection-items] .pv-check').first().waitFor();
 const choice=page.locator('#pv-assign-form input[name=task_ids][value=tree-bug-44]');await choice.check();
 assert.match(await page.locator('[data-selection-count]').innerText(),/已选择 1 项/);
 await page.locator('#pv-assign-form [data-list=pv-assign][data-page="2"]').click();await page.waitForFunction(()=>document.querySelector('#pv-assign-form')?.dataset.selectionPage==='2');
 await page.locator('#pv-assign-form input[name=task_ids]').nth(20).check();assert.match(await page.locator('[data-selection-count]').innerText(),/已选择 2 项/);
 assert(await choice.isChecked());
 await page.getByRole('button',{name:'取消',exact:true}).click();assert.equal(await page.locator('#task-table input[type=checkbox]').count(),0);
 await rootToggle().click();await page.locator('[data-task-row][data-id=tree-bug-44]').waitFor();
 await page.locator('#task-search').focus();
 await page.evaluate(id=>{window.oldShell=document.querySelector('.layout');window.oldSearch=document.querySelector('#task-search');window.oldRoot=document.querySelector(`[data-task-row][data-id="${id}"]`);window.oldChild=document.querySelector('[data-task-row][data-id=tree-bug-44]');window.mutations=0;window.observer=new MutationObserver(changes=>window.mutations+=changes.length);window.observer.observe(document.querySelector('#task-table'),{subtree:true,childList:true,attributes:true,characterData:true});},root.id);
 await page.waitForResponse(r=>r.url().includes('tree_parent_id=tree-iteration')&&r.ok(),{timeout:15000});
 assert.equal(await page.evaluate(()=>window.mutations),0,'unchanged poll mutated the task table');
 sql(`
t=json.loads(db.execute("SELECT payload FROM tasks WHERE id='tree-bug-44'").fetchone()[0]);t['title']='订单关联 Bug 44 已更新';t['version']+=1
db.execute('UPDATE tasks SET payload=?,version=? WHERE id=?',(json.dumps(t),t['version'],t['id']))
`);
 await page.waitForFunction(()=>document.querySelector('[data-task-row][data-id=tree-bug-44]')?.textContent.includes('已更新'),{timeout:18000});
 assert(await page.evaluate(()=>window.oldShell===document.querySelector('.layout')&&window.oldSearch===document.activeElement&&window.oldRoot.isConnected&&window.oldChild===document.querySelector('[data-task-row][data-id=tree-bug-44]')&&document.querySelectorAll('#task-table input[type=checkbox]').length===0));
 await page.evaluate(()=>window.observer.disconnect());
 await page.locator('[data-action=task-tree-page][data-page="2"]').click();await page.waitForFunction(()=>document.querySelector('[aria-label=子项分页]')?.textContent.includes('第 2'));
 await page.locator('[data-action=task-tree-page][data-page="3"]').click();await page.waitForFunction(()=>document.querySelector('[aria-label=子项分页]')?.textContent.includes('第 3'));assert.equal(await rows().count(),26);
 assert.equal(await page.locator('#task-table input[type=checkbox]').count(),0);
 await page.locator('#task-search').fill('支付金额校验问题');await page.locator('[data-task-row][data-id=tree-grandchild]').waitFor();
 assert.equal(await rows().count(),3);assert.equal(await page.locator('.task-tree-context').count(),2);assert.equal(await page.locator('#task-table input[type=checkbox]').count(),0);assert.match(await page.locator('.queue-footer').innerText(),/1 项匹配/);
 // A delayed old-filter child response must never replace the next flat view.
 let release,started,complete,held=false;const intercepted=new Promise(done=>started=done),completed=new Promise(done=>complete=done);
 await page.route('**/api/projects/*/tasks?*',async route=>{if(!held&&route.request().url().includes('tree_parent_id=')&&new URL(route.request().url()).searchParams.get('q')==='支付金额校验问题'){held=true;const response=await route.fetch();await new Promise(done=>{release=done;started();});await route.fulfill({response});complete();}else await route.continue();});
 await page.locator('[data-action=task-tree-retry]').count();
 // Trigger a reload with the same filter, then switch while the branch is pending.
 await rootToggle().click();await rootToggle().click();await intercepted;
 await page.getByRole('button',{name:'平铺展示',exact:true}).click();release();await completed;await page.unroute('**/api/projects/*/tasks?*');
 await page.waitForFunction(()=>document.querySelectorAll('[data-task-row][data-action=open-task]').length===1);
 assert.equal(await page.locator('.task-tree-context').count(),0);assert.equal(await page.locator('[data-action=task-tree-toggle]').count(),0);
 await page.locator('#task-search').fill('');await page.waitForFunction(()=>document.querySelector('[data-pager=tasks]')?.textContent.includes('73'));
 // A view-switch request finishing after navigation must not rebuild the new
 // page and detach its composer or discard the user's focus.
 held=false;const viewStarted=new Promise(done=>started=done),viewCompleted=new Promise(done=>complete=done);
 await page.route('**/api/projects/*/tasks?*',async route=>{if(!held&&!new URL(route.request().url()).searchParams.has('view')){held=true;const response=await route.fetch();await new Promise(done=>{release=done;started();});await route.fulfill({response});complete();}else await route.continue();});
 await page.getByRole('button',{name:'平铺展示',exact:true}).click();await viewStarted;
 await page.locator('.nav').getByRole('button',{name:'项目问答',exact:true}).click();await page.locator('#chat-form').waitFor();
 await page.evaluate(()=>window.savedComposer=document.querySelector('#chat-form'));
 const viewResponse=page.waitForResponse(r=>r.url().includes('/tasks?')&&!new URL(r.url()).searchParams.has('view'));
 release();await viewCompleted;await viewResponse;await page.unroute('**/api/projects/*/tasks?*');
 await page.evaluate(()=>new Promise(done=>requestAnimationFrame(()=>requestAnimationFrame(done))));
 assert(await page.evaluate(()=>window.savedComposer===document.querySelector('#chat-form')),'late view request rebuilt another page');
 await page.locator('.nav').getByRole('button',{name:'需求池',exact:true}).click();
 await page.getByRole('button',{name:'层级展示',exact:true}).click();await page.waitForFunction(()=>document.querySelector('[data-pager=tasks]')?.textContent.includes('26'));
 await page.locator('[data-list=tasks][data-page="2"]').click();await page.waitForFunction(()=>document.querySelector('[data-pager=tasks]')?.textContent.includes('第 2'));assert.equal(await rows().count(),6);
 await page.locator('[data-list=tasks][data-page="1"]').click();await rootToggle().waitFor();
 await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>document.querySelector('.sidebar').getBoundingClientRect().right<=0);assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:join(output,'mobile.png')});
 assert(!requests.some(url=>url.endsWith('/context')),'full context downloaded');assert.deepEqual(errors,[]);
 await writeFile(join(output,'report.json'),JSON.stringify({tasks:73,rootFamilies:26,directChildren:46,childPages:3,nestedRelations:true,selectionOnlyInAssignmentDialog:true,assignmentSelectionAcrossPages:true,keyedPolling:true,unchangedPollMutations:0,staleResponseIgnored:true,filterAncestorContext:true,mobileWidth:390,errors},null,2));
 console.log('PASS: hierarchy, 26 root families / 73 tasks, 46 paged children, nesting, filter ancestors, dialog-only cross-page selection, keyed polling, stale responses, keyboard and mobile.');
}catch(error){console.error('Browser errors:',errors,'Server:',log,await page.locator('body').innerText());throw error;}finally{await browser.close();server.kill('SIGTERM');await stopped;await rm(data,{recursive:true,force:true});}
