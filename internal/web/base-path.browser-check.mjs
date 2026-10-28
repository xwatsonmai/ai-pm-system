// Mount the workspace under a path, as behind a reverse proxy, and use it in a
// real browser: every request must stay inside the mount path. No live services.
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
const output=resolve('test-results/base-path-ui'); await mkdir(output,{recursive:true});
const data=await mkdtemp(join(tmpdir(),'ai-pm-base-path-'));
const reservation=net.createServer().listen(0,'127.0.0.1');await once(reservation,'listening');const port=reservation.address().port;await new Promise(done=>reservation.close(done));
const root=`http://127.0.0.1:${port}`,mount='/zhiqu',site=root+mount;
await writeFile(join(data,'auth.json'),JSON.stringify({provider:'local',public_url:site}),{mode:0o600});
const server=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-requirements-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});const serverExit=once(server,'exit');
let log='';server.stderr.on('data',chunk=>{log+=chunk;});
const browser=await chromium.launch({headless:true});const page=await browser.newPage({viewport:{width:1440,height:1050},reducedMotion:'reduce'});
const errors=[],outside=[],failed=[];
page.on('pageerror',e=>errors.push(e.message));
page.on('request',r=>{const u=new URL(r.url());if(u.origin===root&&!u.pathname.startsWith(mount+'/')&&u.pathname!==mount)outside.push(u.pathname);});
page.on('response',r=>{if(new URL(r.url()).origin===root&&r.status()>=400)failed.push(`${r.status()} ${r.url()}`);});
async function api(path,body){const r=body?await page.request.post(site+'/api'+path,{data:body}):await page.request.get(site+'/api'+path);const v=await r.json();assert(r.ok(),JSON.stringify(v));return v;}
const imageLoaded=locator=>locator.evaluate(img=>new Promise(done=>{const ok=()=>done(img.naturalWidth>0);if(img.complete)ok();else{img.onload=ok;img.onerror=()=>done(false);}}));
const report={};
try{
 for(let i=0;i<100;i++){try{if((await fetch(root+'/api/health')).ok)break;}catch{} await new Promise(done=>setTimeout(done,50));}
 const token=log.match(/初始化口令 (\S+) /)?.[1];assert(token,'setup token not logged');
 await page.goto(site);assert.equal(new URL(page.url()).pathname,mount+'/');
 await page.locator('#setup-form [name=token]').fill(token);
 await page.locator('#setup-form [name=username]').fill('admin');
 await page.locator('#setup-form [name=new_password]').fill('admin-pass-1');
 await page.locator('#setup-form [name=confirm_password]').fill('admin-pass-1');
 await page.locator('#setup-form button[type=submit]').click();await page.locator('.sidebar').waitFor();
 assert.equal(new URL(page.url()).pathname,mount+'/');
 report.login='/zhiqu 跳到 /zhiqu/，首位管理员创建后留在子路径下';

 const project=await api('/projects',{name:'子路径验证',description:'挂在 /zhiqu 下',branch:'main'});
 await page.reload();await page.getByRole('button',{name:'需求池',exact:true}).click();
 await page.locator('#page [data-action=new-task]').click();
 const prose=page.locator('.requirement-prose');await prose.waitFor();
 await page.locator('[name=title]').fill('子路径下的图文需求');
 await prose.click();await page.keyboard.type('截图如下');
 const png=await page.locator('.sidebar').screenshot();
 await prose.evaluate((el,bytes)=>{const dt=new DataTransfer();dt.items.add(new File([new Uint8Array(bytes)],'截图.png',{type:'image/png'}));el.dispatchEvent(new ClipboardEvent('paste',{clipboardData:dt,bubbles:true,cancelable:true}));},[...png]);
 await page.waitForFunction(()=>document.querySelector('.attachment-count')?.textContent==='1');
 assert(await imageLoaded(prose.locator('img')),'pasted image does not load in the editor');
 await page.locator('[name=acceptance]').fill('截图可见');
 await page.locator('#task-form button[type=submit]').click();await page.locator('#task-form').waitFor({state:'detached'});
 let task=(await api(`/projects/${project.id}/context`)).tasks.find(t=>t.title==='子路径下的图文需求');
 assert.match(task.description,/\]\(\/api\/projects\//);assert.doesNotMatch(task.description,/\/zhiqu\/api/);
 await page.getByRole('button',{name:'需求池',exact:true}).click();
 await page.locator('.task-table tbody tr').filter({hasText:'子路径下的图文需求'}).click();await page.locator('.detail-title').waitFor();
 const shown=page.locator('dialog .markdown-content img').first();
 assert.match(await shown.getAttribute('src'),/^\/zhiqu\/api\/projects\//);assert(await imageLoaded(shown),'saved image does not load in the detail');
 await page.screenshot({path:join(output,'detail.png')});
 report.document='粘贴图片在编辑器和详情里都能显示，保存的正文仍是 /api/ 相对链接';

 await page.getByRole('button',{name:'编辑',exact:true}).click();await prose.waitFor();
 assert(await imageLoaded(prose.locator('img')),'saved image does not load when editing');
 await prose.click();await page.keyboard.press('ControlOrMeta+End');await page.keyboard.type('，已补充');
 await page.locator('#task-form button[type=submit]').click();await page.locator('#task-form').waitFor({state:'detached'});
 task=(await api(`/projects/${project.id}/context`)).tasks.find(t=>t.id===task.id);
 assert.match(task.description,/已补充/);assert.match(task.description,/\]\(\/api\/projects\//);assert.doesNotMatch(task.description,/\/zhiqu\/api/);
 report.edit='再次编辑保存后正文链接不带子路径';
 await page.getByRole('button',{name:'关闭',exact:true}).click();

 await page.getByRole('button',{name:'我的 MCP',exact:true}).click();await page.locator('#mcp-status').waitFor();
 assert.match(await page.locator('#page').innerText(),new RegExp(`${site}/mcp`));
 report.mcp='我的 MCP 显示 /zhiqu/mcp';

 await page.getByRole('button',{name:'退出登录',exact:true}).first().click();await page.locator('#login-form').waitFor();
 assert.equal(new URL(page.url()).pathname,mount+'/');
 report.logout='退出后回到 /zhiqu/ 登录页';

 assert.deepEqual(outside,[],'requests left the mount path');
 assert.deepEqual(failed,[],'failed requests');
 assert.deepEqual(errors,[],'page errors');
 report.requests='页面的所有请求都在 /zhiqu/ 下，没有 4xx/5xx';
 console.log(JSON.stringify(report,null,2));
}finally{
 await browser.close();server.kill();await serverExit;await rm(data,{recursive:true,force:true});
}
