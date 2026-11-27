// Isolated SQLite/HTTP/UI regression for responses arriving after a newer view.
import assert from 'node:assert/strict';
import {spawn,execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp,rm,mkdir,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import net from 'node:net';
import {once} from 'node:events';
const load=createRequire(process.env.AI_PM_QA_NODE_MODULES?join(process.env.AI_PM_QA_NODE_MODULES,'package.json'):import.meta.url);
const {chromium}=load('playwright'),data=await mkdtemp(join(tmpdir(),'ai-pm-response-races-'));
const reservation=net.createServer().listen(0,'127.0.0.1');await once(reservation,'listening');const port=reservation.address().port;await new Promise(done=>reservation.close(done));
const base=`http://127.0.0.1:${port}`,server=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-response-race-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});server.stderr.resume();
const serverExit=once(server,'exit'),browser=await chromium.launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[];
page.on('pageerror',e=>errors.push(e.message));
const api=async(path,body,method)=>{const r=await fetch(base+'/api'+path,{method:method||(body?'POST':'GET'),headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const v=await r.json();assert(r.ok,`${path}: ${JSON.stringify(v)}`);return v;};
const nextTick=()=>page.evaluate(()=>new Promise(done=>requestAnimationFrame(()=>requestAnimationFrame(done))));
// Hold an actual HTTP response, then explicitly deliver it after another UI load.
async function holdResponse(match){
 let release,started,finished;const begun=new Promise(done=>started=done),held=new Promise(done=>release=done),delivered=new Promise(done=>finished=done);
 let intercepted=false;
 const handler=async route=>{
  if(intercepted||!match(new URL(route.request().url()))){await route.fallback();return;}
  intercepted=true;
  const response=await route.fetch();started();await held;await route.fulfill({response});finished();
 };
 await page.route('**/api/**',handler);
 return {begun,async release(){release();await delivered;await nextTick();await page.unroute('**/api/**',handler);}};
}
try{
 for(let i=0;i<120;i++){try{if((await fetch(base+'/api/health')).ok)break;}catch{}await new Promise(done=>setTimeout(done,50));}
 const project=await api('/projects',{name:'Response races',branch:'main'});
 const a=(await api(`/projects/${project.id}/tasks`,{title:'Requirement A',description:'A',request_id:'a'})).task;
 const b=(await api(`/projects/${project.id}/tasks`,{title:'Requirement B',description:'B',request_id:'b'})).task;
 for(let i=0;i<45;i++)await api(`/tasks/${a.id}/actions`,{action:'report',report_kind:'progress',body:`ONLY_A_EVENT_${i}`,request_id:`hist-a-${i}`});
 const tasksURL=`${base}/?project=${project.id}&page=tasks`;
 const otherProject=await api('/projects',{name:'Other workspace',branch:'main'});
 const other=(await api(`/projects/${otherProject.id}/tasks`,{title:'Other requirement',description:'Other',request_id:'other'})).task;
 const taskRow=id=>page.locator(`[data-task-row][data-id="${id}"]`);
 const selectProject=async id=>{
  const mobileMenu=page.getByRole('button',{name:'打开导航'});
  if(await mobileMenu.isVisible() && !await page.locator('.sidebar.open').count())await mobileMenu.click();
  await page.locator('#project-switch').click();
  await page.locator(`[data-action=switch-project][data-id="${id}"]`).click();
 };
 const assertWorkspace=async (p,t)=>{
  await taskRow(t.id).waitFor({timeout:5000});
  assert.equal(await page.locator('.project-switch-label strong').textContent(),p.name);
  assert.equal(new URL(page.url()).searchParams.get('project'),p.id);
  assert.equal(await page.evaluate(()=>localStorage.getItem('pm-project')),p.id);
 };
 await page.goto(tasksURL);
 await assertWorkspace(project,a);
 const failedSwitch=`${base}/api/projects/${otherProject.id}/summary`;
 await page.route(failedSwitch,route=>route.fulfill({status:503,json:{error:'切换测试失败'}}),{times:1});
 await selectProject(otherProject.id);
 await page.locator('#toast.show').getByText('切换测试失败').waitFor();
 await assertWorkspace(project,a);
 // A direct link selects its project even when the saved preference differs.
 await page.goto(`${base}/?project=${otherProject.id}&page=tasks`);
 await assertWorkspace(otherProject,other);
 await page.goto(tasksURL);
 await assertWorkspace(project,a);

 const staleList=await holdResponse(u=>u.pathname===`/api/projects/${project.id}/tasks`&&u.searchParams.get('q')==='Requirement A');
 await page.locator('#task-search').fill('Requirement A');
 await staleList.begun;
 try {
  await selectProject(otherProject.id);
  await assertWorkspace(otherProject,other);
 } finally {await staleList.release();}
 assert.equal(await taskRow(a.id).count(),0);
 assert.equal(await page.locator('#task-search').inputValue(),'');
 await page.goBack();await assertWorkspace(project,a);
 await page.goForward();await assertWorkspace(otherProject,other);
 await page.reload();await assertWorkspace(otherProject,other);
 await page.setViewportSize({width:390,height:844});
 await selectProject(project.id);await assertWorkspace(project,a);
 await page.setViewportSize({width:1440,height:1000});
 await page.route(`${base}/api/projects/${otherProject.id}/access`,async route=>{
  const response=await route.fetch();
  const access=await response.json();
  await route.fulfill({response,json:{...access,pages:['overview']}});
 },{times:1});
 await selectProject(otherProject.id);
 await page.getByRole('heading',{name:'项目概览',exact:true}).waitFor();
 assert.equal(new URL(page.url()).searchParams.get('project'),otherProject.id);
 assert.equal(new URL(page.url()).searchParams.get('page'),'overview');
 await page.goBack();await assertWorkspace(project,a);

 await page.goto(tasksURL);await page.locator(`[data-task-row][data-id="${a.id}"]`).click();await page.locator('.detail-title').waitFor();
 let held=await holdResponse(u=>u.pathname.endsWith('/events')&&u.searchParams.get('task_id')===a.id&&u.searchParams.get('page')==='2');
 await page.locator(`[data-list="detail-events:${a.id}"][data-page="2"]`).click();await held.begun;
 await page.getByRole('button',{name:'关闭',exact:true}).click();await page.locator(`[data-task-row][data-id="${b.id}"]`).click();await page.locator('.detail-title').waitFor();
 const bEvents=await page.locator('#task-detail-events').innerText();await held.release();
 assert.equal(await page.locator('.detail-title').innerText(),'Requirement B');assert.equal(await page.locator('#task-detail-events').innerText(),bEvents,'A events overwrote B');
 // The original task opener must not replace a newer task modal either.
 await page.getByRole('button',{name:'关闭',exact:true}).click();
 held=await holdResponse(u=>u.pathname===`/api/tasks/${a.id}`&&u.searchParams.get('compact')==='true');
 await page.locator(`[data-task-row][data-id="${a.id}"]`).click();await held.begun;await page.locator(`[data-task-row][data-id="${b.id}"]`).click();await page.locator('.detail-title').waitFor();await held.release();assert.equal(await page.locator('.detail-title').innerText(),'Requirement B');
 await page.getByRole('button',{name:'关闭',exact:true}).click();await page.locator(`[data-task-row][data-id="${a.id}"]`).click();await page.locator('.detail-title').waitFor();
 await page.locator(`[data-list="detail-events:${a.id}"][data-page="2"]`).click();
 await page.waitForFunction(()=>document.querySelectorAll('#task-detail-events .record').length===40);
 await page.locator(`[data-list="detail-events:${a.id}"][data-page="3"]`).click();
 await page.locator('#task-detail-events').getByText('ONLY_A_EVENT_0',{exact:true}).waitFor();
 assert.equal(await page.locator('#task-detail-events').getByText('ONLY_A_EVENT_44',{exact:true}).count(),1);
 // Version openers and access dialogs also bind their response to the view
 // where the user requested them, rather than reopening after navigation.
 const va=(await api(`/projects/${project.id}/versions`,{name:'Version A',request_id:'version-a'})).project_version;
 const vb=(await api(`/projects/${project.id}/versions`,{name:'Version B',request_id:'version-b'})).project_version;
 for(let i=0;i<43;i++)await api(`/projects/${project.id}/tasks`,{title:`Version A task ${i}`,description:'scope fixture',project_version_id:va.id,request_id:`scope-${i}`});
 await page.goto(`${base}/?project=${project.id}&page=versions`);await page.locator(`[data-action=pv-open][data-id="${va.id}"]`).first().waitFor();
 held=await holdResponse(u=>u.pathname===`/api/project-versions/${va.id}`&&u.searchParams.get('summary')==='1');
 await page.locator(`[data-action=pv-open][data-id="${va.id}"]`).first().click();await held.begun;
 await page.locator(`[data-action=pv-open][data-id="${vb.id}"]`).first().click();await page.locator('.pv-detail .detail-title').filter({hasText:'Version B'}).waitFor();await held.release();assert.equal(await page.locator('.pv-detail .detail-title').innerText(),'Version B');
 await page.getByRole('button',{name:'关闭',exact:true}).click();await page.locator(`[data-action=pv-open][data-id="${va.id}"]`).first().click();await page.locator('[data-list=pv-scope][data-page="2"]').click();
 await page.waitForFunction(()=>document.querySelectorAll('[data-version-scope] .pv-scope-row').length===40);
 held=await holdResponse(u=>u.pathname===`/api/project-versions/${va.id}/scope`&&u.searchParams.get('page')==='3');
 await page.locator('[data-list=pv-scope][data-page="3"]').click();await held.begun;
 await page.locator('[data-version-include-removed]').check();
 await page.waitForFunction(()=>document.querySelectorAll('[data-version-scope] .pv-scope-row').length===20);
 await held.release();assert.equal(await page.locator('[data-version-scope] .pv-scope-row').count(),20,'stale response overwrote the filtered scope');
 await page.goto(`${base}/?project=${project.id}&page=access`);await page.locator('[data-action=access-edit-role]').first().waitFor();
 held=await holdResponse(u=>u.pathname===`/api/projects/${project.id}/access`);
 await page.locator('[data-action=access-edit-role]').first().click();await held.begun;
 await page.locator('.nav [data-page=settings]').click();await held.release();await page.locator('#project-settings-form').waitFor();assert.equal(await page.locator('dialog').count(),0,'old role editor reopened after navigation');
 // Real repository data: tree and search pages use one resolved commit and reject
 // superseded responses for their original DOM nodes.
 const repo=join(data,'source');await mkdir(repo);execFileSync('git',['init','-b','main',repo],{stdio:'ignore'});
 for(let i=0;i<43;i++)await writeFile(join(repo,`file-${String(i).padStart(2,'0')}.txt`),`ALPHA_${i}\nBETA_${i}\n`);
 execFileSync('git',['-C',repo,'add','.']);execFileSync('git',['-C',repo,'-c','user.name=Test','-c','user.email=test@example.com','commit','-m','fixture'],{stdio:'ignore'});
 await api(`/projects/${project.id}/git`,{repository:repo,branch:'main',repository_auth:{mode:'server'}},'PATCH');
 let receipt;for(let i=0;i<100;i++){receipt=await api(`/projects/${project.id}/repository-status`);if(receipt.sha&&!receipt.syncing)break;await new Promise(done=>setTimeout(done,50));}assert(receipt.sha);
 await page.goto(`${base}/?project=${project.id}&page=code`);await page.locator('[data-list=files]').waitFor();
 const codeRefs=[];page.on('request',r=>{const u=new URL(r.url());if(u.pathname.endsWith('/code'))codeRefs.push(u.searchParams.get('ref'));});
 held=await holdResponse(u=>u.pathname.endsWith('/code')&&u.searchParams.get('action')==='tree'&&u.searchParams.get('page')==='2');
 await page.locator('[data-list=files][data-page="2"]').click();await held.begun;await page.locator('.nav [data-page=overview]').click();await held.release();
 await page.locator('.nav [data-page=code]').click();
 await page.locator('[data-list=files][data-page="3"]').waitFor();
 await page.locator('[data-list=files][data-page="3"]').click();
 await page.waitForFunction(()=>document.querySelectorAll('#file-list [data-action=open-file]').length===43);
 held=await holdResponse(u=>u.pathname.endsWith('/code')&&u.searchParams.get('action')==='search'&&u.searchParams.get('query')==='ALPHA');
 await page.locator('#code-search').fill('ALPHA');await page.locator('[data-action=search-code]').click();await held.begun;
 await page.locator('#code-search').fill('BETA');await page.locator('[data-action=search-code]').click();await page.waitForFunction(()=>document.querySelector('#file-content')?.textContent.includes('BETA_'));
 await held.release();assert(!(await page.locator('#file-content').innerText()).includes('ALPHA_'));
 await page.locator('[data-list=code-results][data-page="2"]').click();await page.waitForFunction(()=>document.querySelector('#file-content [data-list=code-results]')?.dataset.page==='3');assert.equal((await page.locator('#file-content pre').innerText()).match(/BETA_/g).length,40);
 assert(codeRefs.length>3&&codeRefs.every(ref=>ref===receipt.sha),'code list requests drifted from their resolved SHA');
 // Options are refreshed when paging the model catalogue while keeping an
 // unsaved choice pinned, so every model remains selectable.
 for(let i=0;i<23;i++)await api('/profiles',{name:`Model ${i}`,provider:'openai',model:`model-${i}`});
 await page.goto(`${base}/?project=${project.id}&page=platform`);await page.locator('[data-list=profiles]').waitFor();
 const before=await page.locator('#platform-form select option').evaluateAll(nodes=>nodes.map(n=>n.value));const unsaved=before[1];await page.locator('#platform-form select').selectOption(unsaved);
 await page.locator('[data-list=profiles][data-page="2"]').click();await page.locator('[data-list=profiles]').waitFor({state:'detached'});
 const after=await page.locator('#platform-form select option').evaluateAll(nodes=>nodes.map(n=>n.value));assert(after.some(id=>id&&!before.includes(id)),'page 2 profiles missing from model selector');assert.equal(await page.locator('#platform-form select').inputValue(),unsaved);
 assert.deepEqual(errors,[]);
 console.log('PASS: project switch on desktop and mobile, history, reload, failed switch and permission fallback, task and version modal identity, detail page generations, role dialog cancellation, tree/search response ordering, fixed code SHA, paged model options and draft selection.');
}catch(e){console.error('BROWSER',errors,await page.locator('body').innerText());throw e;}finally{await browser.close();server.kill('SIGTERM');await serverExit.catch(()=>{});await rm(data,{recursive:true,force:true});}
