// Isolated HTTP + SQLite + Chromium regression; no external model or real IM.
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
const output=resolve('test-results/review-mentions');await mkdir(output,{recursive:true});
const data=await mkdtemp(join(tmpdir(),'ai-pm-mentions-'));
const reservation=net.createServer().listen(0,'127.0.0.1');await once(reservation,'listening');const port=reservation.address().port;await new Promise(done=>reservation.close(done));
const base=`http://127.0.0.1:${port}`;
const keepPreview=process.env.AI_PM_QA_KEEP_PREVIEW==='1';let passed=false;
const server=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-mentions-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-sync-interval','0'],{detached:keepPreview,stdio:['ignore','ignore',keepPreview?'ignore':'pipe']});
const serverExit=once(server,'exit');let serverLog='';server.stderr?.on('data',chunk=>serverLog+=chunk);
const browser=await chromium.launch({headless:true});
const page=await browser.newPage({viewport:{width:1440,height:1000},reducedMotion:'reduce'});
const errors=[];page.on('pageerror',e=>errors.push(e.message));
const report={};
async function api(path,body){const response=await fetch(base+'/api'+path,{method:body?'POST':'GET',headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});const result=await response.json();assert(response.ok,JSON.stringify(result));return result;}
try {
  for(let i=0;i<100;i++){try{if((await fetch(base+'/api/health')).ok)break;}catch{}await new Promise(done=>setTimeout(done,50));}
  const project=await api('/projects',{name:'项目成员提及验证',branch:'main'});
  // Seed only this script's temporary database, preserving the real API rules.
  execFileSync('python3',['-c',`
import sqlite3,json,sys
db=sqlite3.connect(sys.argv[1]);pid=sys.argv[2]
p=json.loads(db.execute("SELECT payload FROM documents WHERE kind='project' AND id=?",(pid,)).fetchone()[0])
users=[{'id':'mai-qing','name':'麦青','login_id':'maiqing'},{'id':'mai-dong','name':'麦冬','login_id':'maidong'},{'id':'lin','name':'林晓','login_id':'linxiao'},{'id':'outsider','name':'麦外','login_id':'outsider'}]
for u in users: db.execute("INSERT INTO documents(kind,id,project_id,payload) VALUES('user',?,'',?)",(u['id'],json.dumps(u)))
p['memberships'] += [{'user_id':u['id'],'roles':['product']} for u in users if u['id']!='outsider']
db.execute("UPDATE documents SET payload=? WHERE kind='project' AND id=?",(json.dumps(p),pid));db.commit()
`,join(data,'ai-pm.db'),project.id]);
  const made=await api(`/projects/${project.id}/tasks`,{title:'评审中提醒项目同事',description:'通过提及通知当前项目成员。',request_id:'create'});
  const endpoint=`/tasks/${made.task.id}/requirement-review`;
  const open=async message=>{await page.goto(`${base}/?task=${made.task.id}&review_message=${message||'rm_'+ '0'.repeat(24)}`);await page.locator('#review-input').waitFor();};
  await open();
  const input=page.locator('#review-input'),popup=page.locator('.rc-mention-popup'),options=popup.getByRole('option'),chips=page.locator('.rc-mention-chip');
  assert.match(await input.getAttribute('placeholder'),/@/);
  await input.fill('@麦');await options.nth(1).waitFor();
  assert.equal(await options.count(),2);assert(!await popup.getByText('麦外',{exact:true}).count());
  assert.deepEqual(await options.allTextContents(),['麦青','麦冬']);
  assert(!/企微|企业微信|maiqing|maidong|搜索|Enter/.test(await popup.innerText()));
  const desktopBox=await popup.boundingBox();assert(desktopBox.width<=220 && desktopBox.height<=90);
  await page.screenshot({path:join(output,'desktop-search.png')});
  await popup.screenshot({path:join(output,'compact-menu.png')});
  await input.press('1');assert.equal(await input.inputValue(),'@麦1');assert.equal(await chips.count(),0);
  await input.fill('@麦');await options.nth(1).waitFor();await input.press('ArrowDown');await input.press('Enter');
  assert.equal(await input.inputValue(),'@麦冬 ');assert.equal(await chips.count(),1);
  await input.press('Home');await page.keyboard.insertText('😀');await input.press('End');await page.keyboard.insertText('请看 @麦');await options.nth(1).waitFor();await input.press('Enter');
  assert.equal(await chips.count(),2);
  const savedBody=await input.inputValue();
  await page.getByRole('button',{name:'关闭',exact:true}).click();await open();
  assert.equal(await input.inputValue(),savedBody);assert.equal(await chips.count(),2);
  report.draft='姓名搜索仅含项目成员；方向键、回车选择；数字正常输入；emoji 前插后偏移正确；重新打开保留提及草稿';
  await input.fill('@麦青');await options.first().waitFor();assert.equal(await chips.count(),0);
  await input.press('Escape');assert(await popup.isHidden());assert(await input.isVisible());
  await page.locator('.review-send').click();await page.getByText('消息已保存',{exact:true}).waitFor();
  let review=await api(endpoint),users=review.messages.filter(m=>m.role==='user');
  assert.equal(users.at(-1).body,'@麦青');assert.equal(users.at(-1).mentions,undefined);
  await input.fill('a@example.com');assert(await popup.isHidden());
  await input.fill('@123');assert.equal(await chips.count(),0);await input.press('Escape');
  report.plain='手输完整姓名、邮箱、@123 都不产生提及；Esc 关闭搜索保留文字';
  await input.fill('');await input.fill('@麦');await options.first().waitFor();
  await input.dispatchEvent('compositionstart');
  await input.dispatchEvent('keydown',{key:'Enter',isComposing:true,bubbles:true,cancelable:true});
  assert.equal(await chips.count(),0);
  await input.dispatchEvent('compositionend');await options.first().waitFor();await input.press('Enter');
  // Edit the selected name: stale identity must disappear.
  await input.evaluate(el=>el.setSelectionRange(2,3));await page.keyboard.insertText('红');assert.equal(await chips.count(),0);
  await input.fill('@麦');await options.first().waitFor();await input.press('Enter');
  await page.keyboard.insertText('请看 @麦');await options.first().waitFor();await input.press('Enter');
  assert.equal(await chips.count(),1);
  let dropped=false;
  await page.route(`**/api${endpoint}*`,async route=>{
    if(route.request().method()==='POST' && route.request().postDataJSON().action==='message' && !dropped){dropped=true;await route.fetch();await route.abort('failed');}else await route.continue();
  });
  const countBefore=users.length;
  await page.locator('.review-send').click();await page.locator('.review-send-error').filter({hasText:'草稿已保留'}).waitFor();
  await page.getByRole('button',{name:'关闭',exact:true}).click();await open();
  await page.locator('.review-send').filter({hasText:'重试保存'}).click();await page.locator('.review-send-error').filter({hasText:'消息已保存'}).waitFor();
  review=await api(endpoint);users=review.messages.filter(m=>m.role==='user');
  assert.equal(users.length,countBefore+1);assert.equal(users.at(-1).mentions.length,2);
  assert.equal(new Set(users.at(-1).mentions.map(m=>m.user_id)).size,1);
  assert.equal(users.at(-1).mention_warnings,undefined);
  assert.equal(await page.locator('.rc-member .rc-mention').count(),2);
  report.retry='中文输入法回车不选人；修改姓名取消身份；同人重复提及、响应丢失、重开重试只保存一条消息';
  const target=users.at(-1).id;
  await open(target);await page.locator(`.review-feed > [data-key="${target}"].rc-flash`).waitFor();
  await page.setViewportSize({width:390,height:844});
  await input.fill('@麦');await options.nth(1).waitFor();
  const box=await popup.boundingBox();assert(box.x>=0 && box.x+box.width<=390 && box.y>=0);
  await page.screenshot({path:join(output,'mobile-search.png')});
  await options.nth(1).click();assert.equal(await input.inputValue(),'@麦冬 ');
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  await page.screenshot({path:join(output,'mobile-selected.png')});
  report.mobile='390px 搜索浮层无横向溢出，可触屏选择；通知链接打开并定位目标消息';
  assert.deepEqual(errors,[]);
  if(keepPreview)report.preview=`${base}/?task=${made.task.id}&review_message=${target}`;
  await writeFile(join(output,'checks.json'),JSON.stringify(report,null,2));
  passed=true;
  console.log(JSON.stringify(report,null,2));
} catch(error) {
  await page.screenshot({path:join(output,'failure.png')});console.error(serverLog);throw error;
} finally {
  await browser.close();
  if(keepPreview && passed)server.unref();
  else {server.kill('SIGTERM');await serverExit;await rm(data,{recursive:true,force:true});}
}
