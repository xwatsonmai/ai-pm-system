// Real loopback APIs and temporary SQLite only; no production or model calls.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp,mkdir,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {once} from 'node:events';
import net from 'node:net';
const load=createRequire(process.env.AI_PM_QA_NODE_MODULES?join(process.env.AI_PM_QA_NODE_MODULES,'package.json'):import.meta.url);
const {chromium}=load('playwright'),data=await mkdtemp(join(tmpdir(),'ai-pm-version-picker-')),output=resolve('test-results/version-picker');await mkdir(output,{recursive:true});
const reserve=net.createServer().listen(0,'127.0.0.1');await once(reserve,'listening');const port=reserve.address().port;await new Promise(done=>reserve.close(done));
const base=`http://127.0.0.1:${port}`,service=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-version-picker-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});const exited=once(service,'exit');let log='';service.stderr.on('data',chunk=>log+=chunk);
const browser=await chromium.launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),errors=[],report={};page.on('pageerror',error=>errors.push(error.message));page.setDefaultTimeout(15000);
const api=async(path,body,method)=>{const response=await fetch(base+'/api'+path,{method:method||(body?'POST':'GET'),headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});const result=await response.json();assert(response.ok,`${path}: ${JSON.stringify(result)}`);return result;};
const tick=()=>page.evaluate(()=>new Promise(done=>requestAnimationFrame(()=>requestAnimationFrame(done))));
async function tasks(project){await page.goto(`${base}/?page=tasks&project=${project.id}`);await page.locator('[data-task-version-picker] [data-version-trigger]').waitFor();}
const filter=()=>page.locator('[data-task-version-picker]'),formPicker=()=>page.locator('.milestone-association.version-picker');
async function open(picker){await picker.locator('[data-version-trigger]').click();await picker.locator('.version-picker-panel').waitFor();await tick();}
async function assertNoOverflow(){assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'page overflows viewport');}
async function assertPanelBounds(picker){const box=await picker.locator('.version-picker-panel').boundingBox(),size=page.viewportSize();assert(box&&box.x>=0&&box.y>=0&&box.x+box.width<=size.width+1&&box.y+box.height<=size.height+1,JSON.stringify(box));}
try{
 for(let i=0;i<120;i++){try{if((await fetch(base+'/api/health')).ok)break;}catch{}await new Promise(done=>setTimeout(done,50));}assert.equal(service.exitCode,null,log);
 const projects=[];
 for(const count of [0,1,20,21]){
  const project=await api('/projects',{name:`Picker ${count}`,branch:'main'}),versions=[];
  for(let n=0;n<count;n++)versions.push((await api(`/projects/${project.id}/versions`,{name:count===1?'用于验证长名称仍保持单行且不会撑宽筛选栏的版本名称'.repeat(2):`Version ${String(n).padStart(2,'0')}`,request_id:crypto.randomUUID()})).project_version);
  projects.push({project,versions});await tasks(project);await open(filter());
  await page.waitForFunction(count=>document.querySelector('[data-task-version-picker] [data-version-options]')?.options.length===Math.min(count,20)+1,count);
  assert.equal(await filter().locator('[data-version-page]').count(),count>20?2:0,`${count} options pagination`);
  assert.equal(await filter().locator('[data-pager],[data-page-size]').count(),0,'version options use the business-list pager');
  assert.equal(await filter().locator('[data-version-pager]').evaluate(node=>node.closest('.version-picker-panel')!==null),true);
  await page.keyboard.press('Escape');assert.equal(await filter().locator('.version-picker-panel').isVisible(),false);
  const bounds=await page.locator('#task-search,#task-filter,#kind-filter,[data-task-version-picker] [data-version-trigger]').evaluateAll(nodes=>nodes.map(node=>node.getBoundingClientRect().toJSON()));
  assert(bounds.every(box=>Math.abs(box.y-bounds[0].y)<=2),'desktop filter controls do not share one row');
  assert.equal(await filter().evaluate(node=>node.clientHeight),await filter().locator('[data-version-trigger]').evaluate(node=>node.offsetHeight),'closed picker adds another visible row');
  if(count===1){await open(filter());await filter().locator('[data-version-options]').selectOption(versions[0].id);await assertNoOverflow();assert((await filter().locator('[data-version-trigger]').innerText()).includes(versions[0].name));await page.screenshot({path:join(output,'one-long-version-desktop.png')});}
 }
 report.singlePage='0/1/20 versions have no option pager; closed desktop picker is one aligned control, including long names';
 const {project,versions}=projects.at(-1),second=(await api(`/projects/${project.id}/version-options?page=2&page_size=20`)).items[0];
 for(let n=0;n<43;n++)await api(`/projects/${project.id}/tasks`,{title:`Task ${n}`,description:'picker fixture',request_id:crypto.randomUUID()});
 await tasks(project);await page.locator('[data-list=tasks][data-page="2"]').click();await page.waitForFunction(()=>document.querySelector('[data-pager=tasks]')?.textContent.includes('第 2'));
 const before=await page.locator('[data-task-row]').evaluateAll(nodes=>nodes.map(node=>node.dataset.id));await open(filter());
 await filter().locator('[data-version-page="2"]').click();await filter().locator(`[data-version-options] option[value="${second.id}"]`).waitFor({state:'attached'});
 assert.deepEqual(await page.locator('[data-task-row]').evaluateAll(nodes=>nodes.map(node=>node.dataset.id)),before,'option paging changed task rows');assert((await page.locator('[data-pager=tasks]').innerText()).includes('第 2'));
 await filter().locator('[data-version-options]').selectOption(second.id);assert.equal(await filter().locator('[name=task_version_filter]').inputValue(),second.id);
 await open(filter());await filter().locator('[data-version-page="1"]').click();await page.waitForFunction(()=>document.querySelector('[data-task-version-picker] .version-picker-pages span')?.textContent==='1 / 2');
 assert.equal(await filter().locator('[data-version-options]').inputValue(),second.id,'selected option was lost on another option page');
 await filter().locator('[data-version-search]').fill('Version 03');await filter().locator('[data-version-options] option').filter({hasText:'Version 03'}).waitFor({state:'attached'});await page.waitForFunction(()=>!document.querySelector('[data-task-version-picker] [data-version-page]'));
 assert.equal(await filter().locator('[data-version-options]').inputValue(),second.id,'search lost the selected ID');
 await filter().locator('[data-version-options]').selectOption('');assert.equal(await filter().locator('[name=task_version_filter]').inputValue(),'');
 await page.screenshot({path:join(output,'all-versions-desktop.png')});report.paging='21 options paginate inside the popup; option paging leaves task page 2 unchanged; search and option paging retain the selected ID';
 // All operations below happen inside the actual requirement dialog.
 await page.locator('#page [data-action=new-task]').click();await formPicker().locator('[data-version-trigger]').waitFor();
 await formPicker().locator('[data-version-trigger]').focus();await page.keyboard.press('Enter');await formPicker().locator('.version-picker-panel').waitFor();await tick();assert.equal(await formPicker().locator('[data-version-search]').evaluate(node=>node===document.activeElement),true);
 await page.keyboard.press('Escape');assert.equal(await page.locator('dialog[open]').count(),1);assert.equal(await formPicker().locator('.version-picker-panel').isVisible(),false);assert.equal(await formPicker().locator('[data-version-trigger]').evaluate(node=>node===document.activeElement),true);
 await open(formPicker());await page.locator('#task-form [name=title]').click();assert.equal(await formPicker().locator('.version-picker-panel').isVisible(),false);assert.equal(await page.locator('dialog[open]').count(),1);
 let creations=0;const creationListener=request=>{if(request.method()==='POST'&&new URL(request.url()).pathname===`/api/projects/${project.id}/tasks`)creations++;};page.on('request',creationListener);
 await open(formPicker());await formPicker().locator('[data-version-search]').fill('Version 07');await formPicker().locator('[data-version-search]').press('Enter');await tick();assert.equal(creations,0,'search Enter submitted the requirement form');assert.equal(await page.locator('dialog[open]').count(),1);assert.equal(await page.locator('#task-form [name=title]').inputValue(),'');page.off('request',creationListener);
 await formPicker().locator('[data-version-search]').press('ArrowDown');assert.equal(await formPicker().locator('[data-version-options]').evaluate(node=>node===document.activeElement),true);
 const chosen=versions.find(version=>version.name==='Version 07');await formPicker().locator(`[data-version-options] option[value="${chosen.id}"]`).waitFor({state:'attached'});await formPicker().locator('[data-version-options]').press('End');assert.equal(await formPicker().locator('.version-picker-panel').isVisible(),true,'keyboard option navigation closed popup before Enter');await formPicker().locator('[data-version-options]').press('Enter');assert.equal(await formPicker().locator('.version-picker-panel').isVisible(),false);assert.equal(await page.locator('#task-form [name=project_version_id]').inputValue(),chosen.id);
 await page.locator('#task-form [name=title]').fill('Picker draft preserved');await page.locator('.requirement-prose').fill('Selected version survives popup closure and draft restore.');await page.locator('#task-form [name=acceptance]').fill('The selected version is retained.');
 await page.getByRole('button',{name:'暂存并关闭',exact:true}).click();await page.locator('#page [data-action=new-task]').click();await formPicker().locator('[data-version-trigger]').waitFor();
 assert.equal(await page.locator('#task-form [name=project_version_id]').inputValue(),chosen.id);await page.waitForFunction(name=>document.querySelector('.milestone-association [data-version-value]')?.textContent===name,chosen.name);
 assert.equal(await page.locator('#task-form [name=title]').inputValue(),'Picker draft preserved');assert((await page.locator('.requirement-prose').innerText()).includes('Selected version survives popup closure and draft restore.'));assert.equal(await page.locator('#task-form [name=acceptance]').inputValue(),'The selected version is retained.');
 await page.setViewportSize({width:390,height:844});await open(formPicker());await assertPanelBounds(formPicker());await assertNoOverflow();await page.screenshot({path:join(output,'requirement-picker-mobile.png')});await page.keyboard.press('Escape');
 await open(formPicker());await page.getByRole('button',{name:'暂存并关闭',exact:true}).click();assert.equal(await page.locator(':popover-open').count(),0);await page.locator('#page [data-action=new-task]').click();await formPicker().locator('[data-version-trigger]').waitFor();
 report.dialog='Search Enter does not submit; ArrowDown focuses options and End keeps popup open until Enter; keyboard opening focuses search; Escape restores trigger focus; outside click closes only popup; chosen ID, title and body survive draft restoration';
 // Hold a real creation request to observe submission locking.
 let release,started;const held=new Promise(done=>release=done),begun=new Promise(done=>started=done);
 const handler=async route=>{if(route.request().method()==='POST'){started();await held;}await route.continue();};await page.route(`**/api/projects/${project.id}/tasks`,handler);
 await page.locator('#task-form button[type=submit]').click();await begun;assert.equal(await formPicker().locator('[data-version-trigger]').isDisabled(),true);assert.equal(await formPicker().locator('[data-version-options]').isDisabled(),true);assert.equal(await formPicker().locator('[data-version-search]').isDisabled(),true);release();await page.locator('#task-form').waitFor({state:'detached'});assert.equal(await page.locator(':popover-open').count(),0,'closed requirement dialog left an open popup');await page.unroute(`**/api/projects/${project.id}/tasks`,handler);
 const saved=(await api(`/projects/${project.id}/tasks`)).find(task=>task.title==='Picker draft preserved');assert.equal(saved.project_version_id,chosen.id);
 await tasks(project);await open(filter());await assertPanelBounds(filter());await assertNoOverflow();await page.screenshot({path:join(output,'filter-picker-mobile.png')});await page.keyboard.press('Escape');await page.screenshot({path:join(output,'filter-closed-mobile.png')});
 report.mobile='390px toolbar and popup stay within viewport; search/select/trigger are disabled throughout an in-flight creation request; saved task retains chosen version';
 // Exercise the inline fallback with the browser feature absent before mount.
 await page.addInitScript(()=>Object.defineProperty(HTMLElement.prototype,'showPopover',{value:undefined,configurable:true}));
 await page.setViewportSize({width:1440,height:1000});await page.reload();await filter().locator('[data-version-trigger]').waitFor();assert.equal(await page.evaluate(()=>typeof HTMLElement.prototype.showPopover),'undefined');
 assert.equal(await filter().locator('.version-picker-panel').getAttribute('popover'),null);assert.equal(await filter().locator('.version-picker-panel').isVisible(),false);
 const fallbackBounds=await page.locator('#task-search,#task-filter,#kind-filter,[data-task-version-picker] [data-version-trigger]').evaluateAll(nodes=>nodes.map(node=>node.getBoundingClientRect().toJSON()));assert(fallbackBounds.every(box=>Math.abs(box.y-fallbackBounds[0].y)<=2),'closed fallback toolbar loses alignment');assert.equal(await filter().evaluate(node=>node.clientHeight),await filter().locator('[data-version-trigger]').evaluate(node=>node.offsetHeight),'closed fallback adds another row');
 await open(filter());assert.equal(await filter().locator('[data-version-search]').isVisible(),true);assert.equal(await filter().locator('[data-version-options]').isVisible(),true);await page.keyboard.press('Escape');await filter().locator('.version-picker-panel').waitFor({state:'hidden'});await assertNoOverflow();await page.screenshot({path:join(output,'fallback-closed-desktop.png')});
 await page.locator('#page [data-action=new-task]').click();await formPicker().locator('[data-version-trigger]').waitFor();await open(formPicker());assert.equal(await formPicker().locator('[data-version-search]').isVisible(),true);assert.equal(await formPicker().locator('[data-version-options]').isVisible(),true);
 await page.keyboard.press('Escape');await formPicker().locator('.version-picker-panel').waitFor({state:'hidden'});assert.equal(await page.locator('dialog[open]').count(),1);assert.equal(await formPicker().locator('[data-version-trigger]').evaluate(node=>node===document.activeElement),true);
 await open(formPicker());await page.locator('#task-form [name=title]').click();await formPicker().locator('.version-picker-panel').waitFor({state:'hidden'});assert.equal(await page.locator('dialog[open]').count(),1);assert.equal(await page.locator('#task-form [name=title]').evaluate(node=>node===document.activeElement),true);await page.getByRole('button',{name:'暂存并关闭',exact:true}).click();
 report.fallback='Without showPopover, closed toolbar remains one aligned row; inline search/options open normally; Escape closes only picker and restores trigger focus; clicking title closes picker and retains the parent dialog';
 assert.deepEqual(errors,[]);await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report,null,2));
}catch(error){console.error('BROWSER',errors,await page.locator('body').innerText());await page.screenshot({path:join(output,'failure.png'),fullPage:true}).catch(()=>{});throw error;}finally{await browser.close();service.kill('SIGTERM');await exited;await rm(data,{recursive:true,force:true});}
