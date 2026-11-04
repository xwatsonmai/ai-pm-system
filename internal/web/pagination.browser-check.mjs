// Isolated real SQLite/HTTP/UI regression. Never contacts a model or production.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp,rm,mkdir,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import net from 'node:net';
import {once} from 'node:events';
const load=createRequire(process.env.AI_PM_QA_NODE_MODULES?join(process.env.AI_PM_QA_NODE_MODULES,'package.json'):import.meta.url);
const {chromium}=load('playwright');
const data=await mkdtemp(join(tmpdir(),'ai-pm-pagination-'));
const reservation=net.createServer().listen(0,'127.0.0.1');await once(reservation,'listening');const port=reservation.address().port;await new Promise(done=>reservation.close(done));
const base=`http://127.0.0.1:${port}`;
const server=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-pagination-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});server.stderr.resume();
const browser=await chromium.launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[];
page.on('pageerror',e=>errors.push(e.message));
const api=async(path,body,method)=>{const r=await fetch(base+'/api'+path,{method:method||(body?'POST':'GET'),headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const v=await r.json();assert(r.ok,JSON.stringify(v));return v;};
try{
 for(let i=0;i<100;i++){try{if((await fetch(base+'/api/health')).ok)break;}catch{}await new Promise(done=>setTimeout(done,50));}
 const project=await api('/projects',{name:'列表验证',branch:'main'});
 for(let i=0;i<43;i++)await api(`/projects/${project.id}/tasks`,{title:`分页需求 ${String(i).padStart(2,'0')}`,description:'正文不应出现在列表投影中',request_id:`create-${i}`});
 const list=await api(`/projects/${project.id}/tasks?page=1&page_size=20`);assert.equal(list.total,43);assert.equal(list.items.length,20);assert.equal(list.items[0].description,'');
 assert.equal((await api(`/projects/${project.id}/tasks`)).length,43);
 const requests=[];page.on('request',r=>requests.push(r.url()));
 await page.goto(base);await page.getByRole('button',{name:'需求池',exact:true}).click();await page.waitForFunction(()=>document.querySelectorAll('.task-table tbody tr').length===20);
 assert.match(await page.locator('[data-pager=tasks]').innerText(),/43/);
 await page.locator('[data-list=tasks][data-page="2"]').click();await page.waitForFunction(()=>document.querySelector('[data-pager=tasks]')?.textContent.includes('第 2'));
 await page.locator('[data-list=tasks][data-page="3"]').click();await page.waitForFunction(()=>document.querySelectorAll('.task-table tbody tr').length===3);
 await page.locator('#task-search').fill('分页需求 00');await page.waitForFunction(()=>document.querySelectorAll('.task-table tbody tr').length===1);
 await page.locator('#task-search').fill('');await page.waitForFunction(()=>document.querySelectorAll('.task-table tbody tr').length===20);
 // Observe the original shell, focused search and selected row across polling.
 const id=await page.locator('.task-table tbody tr').nth(1).getAttribute('data-id');
 await page.locator(`tr[data-id="${id}"] input`).check();
 await page.evaluate(id=>{window.savedShell=document.querySelector('.layout');window.savedRow=document.querySelector(`tr[data-id="${id}"]`);window.savedSearch=document.querySelector('#task-search');window.savedCheckbox=window.savedRow.querySelector('input');},id);
 const changing=list.items.find(t=>t.id!==id);const full=(await api(`/tasks/${changing.id}?compact=true`)).task;
 await api(`/tasks/${full.id}`,{title:full.title+' 已更新',description:full.description,kind:full.kind,priority:full.priority,version:full.version,request_id:'edit-poll'},'PUT');
 await page.waitForFunction(()=>document.querySelector('#task-table')?.textContent.includes('已更新'),{timeout:18000});
 assert(await page.evaluate(()=>window.savedShell===document.querySelector('.layout')&&window.savedSearch===document.querySelector('#task-search')&&window.savedRow.isConnected&&window.savedCheckbox.checked));
 await page.locator(`tr[data-id="${id}"] input`).uncheck();await page.locator(`tr[data-id="${id}"]`).click();await page.locator('.detail-title').waitFor();assert(await page.locator('#task-detail-events [data-pager]').count());
 await page.getByRole('button',{name:'关闭',exact:true}).click();
 // A long requirement room starts with a bounded window; older windows retain
 // the actual input node, draft and current workflow state.
 for(let i=0;i<45;i++)await api(`/tasks/${id}/actions`,{action:'report',report_kind:'progress',body:`历史记录 ${i}`,request_id:`history-${i}`});
 const reviewPage=await api(`/tasks/${id}/requirement-review?message_limit=20`);assert.equal(reviewPage.messages.length,20);assert(reviewPage.message_page.has_more);
 await page.locator(`tr[data-id="${id}"]`).click();await page.getByRole('button',{name:'继续需求评审',exact:true}).click();
 await page.locator('[data-review-action=history-more]').waitFor();await page.locator('#review-input').fill('历史翻页保留的草稿');
 await page.evaluate(()=>window.reviewInput=document.querySelector('#review-input'));
 const beforeHistory=await page.locator('.review-feed>[data-key]').count();await page.locator('[data-review-action=history-more]').click();
 await page.waitForFunction(n=>document.querySelectorAll('.review-feed>[data-key]').length>n,beforeHistory);
 assert(await page.evaluate(()=>window.reviewInput===document.querySelector('#review-input')&&window.reviewInput.value==='历史翻页保留的草稿'));
 await page.getByRole('button',{name:'关闭',exact:true}).click();
 // Current page and responsive layout survive navigation-independent polling.
 await page.setViewportSize({width:390,height:844});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 assert(!requests.some(url=>url.endsWith('/context')),'workspace requested full context');assert.deepEqual(errors,[]);
 const output=resolve('test-results/pagination');await mkdir(output,{recursive:true});await page.screenshot({path:join(output,'mobile.png')});await writeFile(join(output,'report.json'),JSON.stringify({tasks:43,rows:20,legacyCompatible:true,keyedRefresh:true,compactDetail:true,errors},null,2));
 console.log('PASS: SQL pagination, 43-item reachability, filters, keyed polling, compact detail, mobile.');
}catch(e){console.error("BROWSER",errors,await page.locator("body").innerText());throw e;}finally{await browser.close();server.kill('SIGTERM');await once(server,'exit').catch(()=>{});await rm(data,{recursive:true,force:true});}
