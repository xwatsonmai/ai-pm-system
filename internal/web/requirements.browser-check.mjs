// Run against a temporary SQLite database and local server. No live services.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp, mkdir, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import net from 'node:net';
import {once} from 'node:events';
const load=createRequire(process.env.AI_PM_QA_NODE_MODULES ? join(process.env.AI_PM_QA_NODE_MODULES,'package.json') : import.meta.url);
const {chromium}=load('playwright');
const output=resolve('test-results/requirements-ui'); await mkdir(output,{recursive:true});
const data=await mkdtemp(join(tmpdir(),'ai-pm-requirements-'));
const reservation=net.createServer().listen(0,'127.0.0.1');await once(reservation,'listening');const port=reservation.address().port;await new Promise(done=>reservation.close(done));
const base=`http://127.0.0.1:${port}`;
const server=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-requirements-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});const serverExit=once(server,'exit');server.stderr.resume();
const browser=await chromium.launch({headless:true});const page=await browser.newPage({viewport:{width:1440,height:1050},reducedMotion:'reduce'});
const errors=[];page.on('pageerror',e=>errors.push(e.message));
async function api(path,body,method){const r=await fetch(base+'/api'+path,{method:method||(body?'POST':'GET'),headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});const result=await r.json();assert(r.ok,JSON.stringify(result));return result;}
const report={};
try{
 for(let i=0;i<100;i++){try{if((await fetch(base+'/api/health')).ok)break;}catch{} await new Promise(done=>setTimeout(done,50));}
 const project=await api('/projects',{name:'智驱产品迭代',description:'需求池图文文档验证',branch:'main'});
 const legacy=await api(`/projects/${project.id}/tasks`,{title:'原有纯文本需求',kind:'requirement',description:'已有说明保持可读',request_id:'legacy'});
 await page.goto(base);await page.getByRole('button',{name:'需求池',exact:true}).click();
 await page.locator('#page [data-action=new-task]').click();
 await page.locator('.requirement-prose').waitFor();
 assert.equal(errors.length,0,errors.join('\n'));
 await page.screenshot({path:join(output,'create-empty-desktop.png')});
 await page.locator('[name=title]').fill('订单退款：补全部分退款流程');
 await page.locator('[data-editor-action=template]').click();
 assert.equal(await page.locator('.requirement-prose h2').count(),3);
 const prose=page.locator('.requirement-prose');
 await prose.click();await page.keyboard.press('ControlOrMeta+End');await page.keyboard.press('Enter');
 await page.getByRole('button',{name:'加粗',exact:true}).click();await page.keyboard.type('退款规则与证据');await page.getByRole('button',{name:'加粗',exact:true}).click();await page.keyboard.press('Enter');
 await page.getByRole('button',{name:'插入表格',exact:true}).click();
 assert.equal(await prose.locator('table').count(),1);
 await prose.locator('th').first().click();await page.keyboard.type('场景');await page.keyboard.press('Tab');await page.keyboard.type('预期结果');
 // Upload a text document and PNG through the actual multipart API.
 await page.locator('#requirement-files').setInputFiles({name:'补充规则.md',mimeType:'text/markdown',buffer:Buffer.from('# 金额规则\n累计退款不得超过实付金额。')});
 await page.waitForFunction(()=>document.querySelector('.attachment-count')?.textContent==='1');
 const png=await prose.locator('table').screenshot();
 // Clipboard event validates the real editor paste hook without host clipboard access.
 await prose.click();
 await prose.evaluate((el,bytes)=>{const dt=new DataTransfer();dt.items.add(new File([new Uint8Array(bytes)],'错误截图.png',{type:'image/png'}));el.dispatchEvent(new ClipboardEvent('paste',{clipboardData:dt,bubbles:true,cancelable:true}));},[...png]);
 await page.waitForFunction(()=>document.querySelector('.attachment-count')?.textContent==='2');
 assert.equal(await prose.locator('img').count(),1);
 await page.locator('[name=acceptance]').fill('部分退款金额不超过剩余可退款金额\n每次退款都保留审计记录');
 await page.locator('[name=reviewer]').fill('产品负责人');
 await page.getByRole('button',{name:'预览',exact:true}).click();
 assert.equal(await page.locator('.document-preview h2').count(),3);assert.equal(await page.locator('.document-preview img').count(),1);
 await page.getByRole('button',{name:'继续编辑',exact:true}).click();
 await page.screenshot({path:join(output,'document-desktop.png')});
 await page.getByRole('button',{name:'暂存并关闭',exact:true}).click();
 await page.reload();await page.locator('.topbar [data-action=new-task]').click();await prose.waitFor();
 assert.equal(await page.locator('[name=title]').inputValue(),'订单退款：补全部分退款流程');assert.equal(await prose.locator('img').count(),1);assert.equal(await prose.locator('table').count(),1);
 assert.equal(await page.locator('.attachment-count').textContent(),'2');
 report.draft='title, rich text, table, inline image and file restored after close/reload';
 // Lose the creation response after the server commits; retry must create once.
 let dropped=false;
 await page.route(`**/api/projects/${project.id}/tasks`,async route=>{if(route.request().method()==='POST'&&!dropped){dropped=true;await route.fetch();await route.abort('failed');}else await route.continue();});
 await page.locator('#task-form button[type=submit]').click();await page.locator('#task-form .form-error').filter({hasText:/fetch|请求|网络|Failed/i}).waitFor();
 await page.getByRole('button',{name:'暂存并关闭',exact:true}).click();await page.reload();await page.locator('.topbar [data-action=new-task]').click();await prose.waitFor();
 await page.locator('#task-form button[type=submit]').click();await page.locator('#task-form').waitFor({state:'detached'});assert.equal(await page.locator('dialog').count(),0);await page.getByRole('button',{name:'需求池',exact:true}).click();await page.locator('.task-table tbody tr').filter({hasText:'订单退款：补全部分退款流程'}).click();await page.locator('.detail-title').waitFor();
 let context=await api(`/projects/${project.id}/context`);const tasks=context.tasks.filter(t=>t.title==='订单退款：补全部分退款流程');assert.equal(tasks.length,1);let task=tasks[0];
 assert.equal(task.attachments.length,2);assert.match(task.description,/!\[错误截图\.png\]/);assert.match(task.description,/\|/);assert.equal(task.acceptance.length,2);
 assert.equal(await page.locator('.requirement-description img').count(),1);assert.equal(await page.locator('.requirement-description table').count(),1);
 assert.equal(await page.locator('.sent-attachments a').count(),2);
 report.persistence='real API, SQLite, sanitized Markdown detail, downloadable attachments and lost-response retry passed';
 await page.getByRole('button',{name:'编辑',exact:true}).click();await prose.waitFor();
 await page.getByRole('button',{name:'移除 错误截图.png',exact:true}).click();assert.equal(await prose.locator('img').count(),0);
 await page.locator('label:has(input[value=bug])').click();
 await page.locator('#task-form button[type=submit]').click();await page.locator('.detail-title').waitFor();
 task=(await api(`/tasks/${task.id}`)).task;assert.equal(task.kind,'bug');assert.equal(task.attachments.length,1);assert.equal(task.revision,2);assert(!task.description.includes('错误截图.png'));
 await page.getByRole('button',{name:'关闭',exact:true}).click();
 await page.getByRole('button',{name:'需求池',exact:true}).click();
 await page.locator('#kind-filter').selectOption('bug');assert.equal(await page.locator('.task-table tbody tr').count(),1);
 await page.locator('#kind-filter').selectOption('requirement');assert.equal(await page.locator('.task-table tbody tr').count(),1);
 report.revision='remove inline image, switch to Bug fix, revision increment and type filters passed';
 await page.locator('#page [data-action=new-task]').click();await prose.waitFor();
 await page.locator('label:has(input[value=bug])').click();await page.getByRole('button',{name:'插入填写模板',exact:true}).click();assert.equal(await prose.locator('h2').count(),4);
 // Drag a file over the editor, then test failed upload and retained draft.
 await prose.evaluate(el=>{const dt=new DataTransfer();dt.items.add(new File(['复现日志'],'复现.log',{type:'text/plain'}));el.dispatchEvent(new DragEvent('drop',{dataTransfer:dt,bubbles:true,cancelable:true,clientX:el.getBoundingClientRect().x+10,clientY:el.getBoundingClientRect().y+10}));});
 await page.waitForFunction(()=>document.querySelector('.attachment-count')?.textContent==='1');
 await page.route(`**/api/projects/${project.id}/attachments`,route=>route.fulfill({status:400,contentType:'application/json',body:JSON.stringify({error:'测试上传失败'})}));
 await page.locator('#requirement-files').setInputFiles({name:'失败.txt',mimeType:'text/plain',buffer:Buffer.from('test')});await page.getByText('失败.txt：测试上传失败').waitFor();
 assert.equal(await prose.locator('h2').count(),4);assert.equal(await page.locator('.attachment-count').textContent(),'1');
 report.uploads='clipboard PNG, file selection, drag/drop, failed-upload retention and attachment deletion passed';
 // Mobile document and property sections remain within a single scroll container.
 await page.setViewportSize({width:390,height:844});await page.locator('[name=title]').fill('支付结果回调异常');
 await page.screenshot({path:join(output,'document-mobile.png')});
 assert(await page.evaluate(()=>document.querySelector('dialog').scrollWidth<=innerWidth));
 await page.locator('[name=acceptance]').scrollIntoViewIfNeeded();await page.locator('[name=acceptance]').fill('重复回调只更新一次');
 assert(await page.locator('#task-form button[type=submit]').isVisible());
 await page.screenshot({path:join(output,'properties-mobile.png')});
 report.mobile='390px width, document and properties scroll, footer actions visible';
 await page.getByRole('button',{name:'暂存并关闭',exact:true}).click();
 await page.setViewportSize({width:1440,height:1050});
 await page.locator('.task-table tbody tr').filter({hasText:'原有纯文本需求'}).click();
 await page.getByRole('button',{name:'编辑',exact:true}).click();await prose.waitFor();
 await page.locator('#task-form button[type=submit]').click();await page.locator('.detail-title').waitFor();
 const unchanged=(await api(`/tasks/${legacy.task.id}`)).task;
 assert.equal(unchanged.description,legacy.task.description);assert.equal(unchanged.revision,legacy.task.revision);
 await page.getByRole('button',{name:'编辑',exact:true}).click();await prose.waitFor();
 await prose.evaluate(el=>{const dt=new DataTransfer();dt.setData('text/html','<p><strong>外部资料</strong><img src=x onerror="window.requirementAttack=1"><a href="javascript:alert(1)">危险链接</a></p>');el.dispatchEvent(new ClipboardEvent('paste',{clipboardData:dt,bubbles:true,cancelable:true}));});
 assert.equal(await prose.locator('[onerror], [href^="javascript:"]').count(),0);
 assert.equal(await page.evaluate(()=>window.requirementAttack),undefined);
 const updated=await api(`/tasks/${unchanged.id}`,{title:unchanged.title,kind:unchanged.kind,description:'其他成员更新的要求',version:unchanged.version,request_id:'external-revision'},'PUT');
 await page.locator('#task-form button[type=submit]').click();await page.getByText('记录已变化，请刷新后重试',{exact:true}).waitFor();
 assert((await prose.textContent()).includes('外部资料'));
 assert.equal((await api(`/tasks/${unchanged.id}`)).task.description,updated.task.description);
 report.compatibility='legacy no-op save keeps revision, pasted HTML sanitized, concurrent edits rejected with draft retained';

 assert.equal(errors.length,0,errors.join('\n'));
 await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report,null,2));
}catch(e){await page.screenshot({path:join(output,'failure.png'),fullPage:true});console.error('Browser errors:',errors, await page.locator('.upload-status').textContent().catch(()=>''));throw e;}
finally{await browser.close();server.kill('SIGTERM');await serverExit;await rm(data,{recursive:true,force:true});}
