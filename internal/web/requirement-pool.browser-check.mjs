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
const output=resolve('test-results/requirement-pool');await mkdir(output,{recursive:true});
const data=await mkdtemp(join(tmpdir(),'ai-pm-pool-'));
const reservation=net.createServer().listen(0,'127.0.0.1');await once(reservation,'listening');
const port=reservation.address().port;await new Promise(done=>reservation.close(done));const base=`http://127.0.0.1:${port}`;
const keepPreview=process.env.AI_PM_QA_KEEP_PREVIEW==='1';let passed=false;
const server=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-pool-redesign-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-sync-interval','0'],{detached:keepPreview,stdio:['ignore','ignore',keepPreview?'ignore':'pipe']});
let log='';server.stderr?.on('data',chunk=>log+=chunk);const stopped=once(server,'exit');
const browser=await chromium.launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000}});
const errors=[],requests=[];page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>requests.push(r.url()));
async function api(path,body){const r=await fetch(base+'/api'+path,{method:body?'POST':'GET',headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const v=await r.json();assert(r.ok,JSON.stringify(v));return v;}
const sql=(code,...args)=>execFileSync('python3',['-c',`import sqlite3,json,sys,time\ndb=sqlite3.connect(sys.argv[1])\n${code}\ndb.commit()`,join(data,'ai-pm.db'),...args]);
const rows=()=>page.locator('[data-task-row][data-action=open-task]');
try{
 for(let i=0;i<100;i++){try{if((await fetch(base+'/api/health')).ok)break;}catch{}await new Promise(done=>setTimeout(done,50));}
 const project=await api('/projects',{name:'产品协作示例',branch:'main'});
 const created=await api(`/projects/${project.id}/tasks`,{title:'完善订阅续费与多币种账单',description:'改善订阅与账单流程',request_id:'visual-root'}),root=created.task;
 const second=await api(`/projects/${project.id}/tasks`,{title:'重新组织客户服务工作台',description:'优化服务工作台的协作流程',request_id:'visual-root-2'});
 const version2=await api(`/projects/${project.id}/versions`,{name:'下一期 · 协作体验',goal:'优化服务流程',request_id:'visual-v2'});
 sql(`
r=json.loads(sys.argv[2]);second=json.loads(sys.argv[3]);v2=json.loads(sys.argv[4]);pid=r['project_id'];now=int(time.time()*1000)
v=json.loads(db.execute('SELECT payload FROM project_versions WHERE id=?',(r['project_version_id'],)).fetchone()[0]);v['name']='2026.11 · 订阅与协作';db.execute('UPDATE project_versions SET payload=? WHERE id=?',(json.dumps(v),v['id']))
def update(t,**extra):
 t.update(extra);db.execute('UPDATE tasks SET payload=? WHERE id=?',(json.dumps(t),t['id']))
def add(id,title,**extra):
 t=dict(id=id,project_id=pid,title=title,description='仅用于隔离界面验证',acceptance=['符合预期流程'],kind='requirement',status='ready',priority='next',revision=2,version=1,project_version_id=r['project_version_id'],created_at=now-1000,updated_at=now-5000)
 t.update(extra);db.execute("INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,1,'',?)",(id,pid,json.dumps(t)))
update(r,status='fixing',priority='now',owner='林川',updated_at=now+1000)
update(second,status='doing',priority='next',owner='陈思',updated_at=now+900)
add('bug-payment','续费成功后账单金额显示异常',kind='bug',parent_requirement_id=r['id'],status='doing',owner='林川',updated_at=now+800,priority='now')
add('bug-currency','切换结算币种后未同步刷新税费',kind='bug',parent_requirement_id=r['id'],status='verifying',owner='许宁',updated_at=now+700)
add('iteration-service','支持跨部门流转的服务工单',iteration=dict(parent_task_id=second['id'],root_task_id=second['id']),project_version_id=v2.get('project_version',v2)['id'],owner='陈思',status='ready',updated_at=now+600)
add('bug-service','服务记录导出后缺少负责人信息',kind='bug',parent_requirement_id='iteration-service',project_version_id=v2.get('project_version',v2)['id'],owner='许宁',status='retest',updated_at=now+500)
add('item-search','优化知识库搜索结果与摘要展示',status='clarify',priority='next',updated_at=now+300)
add('item-access','调整成员邀请与权限说明',status='testing',owner='陈思',priority='later',updated_at=now+200)
add('item-alignment','确认跨部门工单的流转范围',status='aligning',updated_at=now+400)
add('item-background','补充账单导出规则',status='clarify',updated_at=now+10000)
add('item-timestamp','修复海外用户看到的日期格式',kind='bug',status='resolved',owner='林川',priority='later',updated_at=now+20000)
review=dict(task_id='item-search',task_revision=2,status='needs_input',generation=1,version=1)
db.execute("INSERT INTO requirement_reviews(task_id,version,payload) VALUES('item-search',1,?)",(json.dumps(review),))
db.execute("INSERT INTO requirement_reviews(task_id,version,payload) VALUES('item-background',1,?)",(json.dumps(dict(task_id='item-background',task_revision=2,status='running',generation=1,version=1)),))
`,JSON.stringify(root),JSON.stringify(second.task),JSON.stringify(version2));
 await page.setViewportSize({width:1600,height:1180});await page.goto(base);await page.getByRole('button',{name:'需求池',exact:true}).click();
 await page.locator(`[data-task-row][data-id="${root.id}"]`).waitFor();
 const hierarchyOrder=['item-alignment','item-search',root.id,second.task.id,'item-access','item-background','item-timestamp'];
 assert.deepEqual(await rows().evaluateAll(nodes=>nodes.map(node=>node.dataset.id)),hierarchyOrder,'manual decisions and actionable descendants must outrank newer background/finished work');
 assert.equal(await page.locator('.pool-family.pool-has-children').count(),2);assert.equal(await page.locator('.pool-branch').count(),0);
 const weight=await page.evaluate(parentID=>{
  const measure=id=>{
   const row=document.querySelector(`[data-task-row][data-id="${id}"]`),title=getComputedStyle(row.querySelector('.pool-title')),rs=getComputedStyle(row),family=getComputedStyle(row.parentElement);
   return {font:title.fontSize,weight:title.fontWeight,lineHeight:title.lineHeight,color:title.color,padding:rs.padding,background:rs.backgroundColor,height:row.getBoundingClientRect().height,familyBorder:family.borderLeftWidth,familyRadius:family.borderRadius,familyBackground:family.backgroundColor,familyMargin:family.margin};
  };
  return {withChildren:measure(parentID),independent:measure('item-access')};
 },root.id);
 assert.deepEqual(weight.withChildren,weight.independent,'relationships must not give a requirement more visual weight');

 await page.evaluate(async()=>{await Promise.all([...document.querySelectorAll('.pool-branch')].flatMap(el=>el.getAnimations()).map(animation=>animation.finished.catch(()=>{})));document.activeElement?.blur();scrollTo(0,0);});await page.screenshot({path:join(output,'desktop-collapsed.png'),fullPage:true,animations:'disabled'});
 const toggle=id=>page.locator(`[data-action=task-tree-toggle][data-id="${id}"]`);
 await toggle(root.id).focus();await page.keyboard.press('Enter');await page.locator('[data-task-row][data-id=bug-payment]').waitFor();
 await toggle(second.task.id).click();await page.locator('[data-task-row][data-id=iteration-service]').waitFor();await toggle('iteration-service').click();await page.locator('[data-task-row][data-id=bug-service]').waitFor();
 assert.equal(await page.locator('dialog').count(),0);
 const parent=page.locator('.pool-family').filter({has:page.locator(`[data-task-row][data-id="${root.id}"]`)});
 assert.equal(await parent.locator('.pool-children [data-task-row]').count(),2);assert.equal(await parent.locator('.pool-kind--bug').count(),2);
 const nested=page.locator('.pool-family').filter({has:page.locator(`[data-task-row][data-id="${second.task.id}"]`)});
 assert.equal(await nested.locator('.pool-children .pool-children [data-task-row]').count(),1);
 assert.match(await page.locator('[data-task-row][data-id=iteration-service]').innerText(),/下一期 · 协作体验/);
 assert.equal(await page.locator('.pool-status .review-attention').count(),2);
 assert.deepEqual(await parent.locator('.pool-children [data-task-row]').evaluateAll(nodes=>nodes.map(node=>node.dataset.id)),['bug-currency','bug-payment'],'pending verification must precede newer development work');
 const geometry=await page.locator(`[data-task-row][data-id="${root.id}"]`).evaluate(row=>({row:row.getBoundingClientRect().width,title:row.querySelector('.pool-main').getBoundingClientRect().width,font:parseFloat(getComputedStyle(row.querySelector('.pool-title')).fontSize)}));
 assert(geometry.title>geometry.row*.4&&geometry.font>=14,'the title lost its primary visual weight');
 const line=await page.locator('.pool-children > .pool-child').first().evaluate(el=>({width:parseFloat(getComputedStyle(el,'::before').width),height:parseFloat(getComputedStyle(el,'::after').height),color:getComputedStyle(el,'::after').backgroundColor}));
 assert(line.width>0&&line.height>0&&line.color!=='rgba(0, 0, 0, 0)','relationship connectors are invisible');
 await page.evaluate(async()=>{await Promise.all([...document.querySelectorAll('.pool-branch')].flatMap(el=>el.getAnimations()).map(animation=>animation.finished.catch(()=>{})));document.activeElement?.blur();scrollTo(0,0);});await page.screenshot({path:join(output,'desktop-expanded.png'),fullPage:true});
 assert.equal(await page.locator('#task-table input[type=checkbox]').count(),0);
 await page.getByRole('button',{name:'安排目标版本',exact:true}).click();await page.locator('#pv-assign-form input[name=task_ids][value=bug-payment]').check();
 assert.match(await page.locator('[data-selection-count]').innerText(),/已选择 1 项/);assert.equal(await page.locator('#task-table input[type=checkbox]').count(),0);
 await page.screenshot({path:join(output,'assignment-dialog.png')});
 await page.getByRole('button',{name:'取消',exact:true}).click();assert.equal(await page.locator('#pv-assign-form').count(),0);
 assert.equal(await page.locator('#task-table input[type=checkbox]').count(),0);
 await page.locator('[data-task-row][data-id=bug-payment] .pool-title').focus();await page.keyboard.press('Enter');await page.locator('.detail-title').waitFor();await page.getByRole('button',{name:'关闭',exact:true}).click();
 await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>document.querySelector('.sidebar').getBoundingClientRect().right<=0);
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'mobile page overflow');
 const fits=await page.locator('.pool-row,.pool-title,.pool-status,.pool-owner').evaluateAll(nodes=>nodes.every(el=>{const r=el.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth+1;}));assert(fits,'mobile task content needs horizontal scrolling');
 await page.evaluate(async()=>{await Promise.all([...document.querySelectorAll('.pool-branch')].flatMap(el=>el.getAnimations()).map(animation=>animation.finished.catch(()=>{})));document.activeElement?.blur();scrollTo(0,0);});await page.screenshot({path:join(output,'mobile-expanded.png'),fullPage:true});
 // Long unbroken names must wrap inside the same mobile relationship group.
 sql(`
t=json.loads(db.execute("SELECT payload FROM tasks WHERE id='item-access'").fetchone()[0]);t['title']='成员权限说明与协作流程中的默认可见范围需要保持一致。'*3+'LongUnbrokenTitle'*8
db.execute('UPDATE tasks SET payload=? WHERE id=?',(json.dumps(t),t['id']))
v=json.loads(db.execute('SELECT payload FROM project_versions WHERE id=?',(sys.argv[2],)).fetchone()[0]);v['name']='LongUnbrokenVersionName'*8
db.execute('UPDATE project_versions SET payload=? WHERE id=?',(json.dumps(v),v['id']))
`,version2.project_version?.id||version2.id);
 await page.waitForFunction(()=>document.querySelector('[data-task-row][data-id=item-access] .pool-title')?.textContent.includes('LongUnbrokenTitle'));
 await page.waitForFunction(()=>document.querySelector('[data-task-row][data-id=iteration-service]')?.textContent.includes('LongUnbrokenVersionName'));
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'long titles or versions overflowed the mobile page');
 sql(`
t=json.loads(db.execute("SELECT payload FROM tasks WHERE id='item-access'").fetchone()[0]);t['title']='调整成员邀请与权限说明';db.execute('UPDATE tasks SET payload=? WHERE id=?',(json.dumps(t),t['id']))
v=json.loads(db.execute('SELECT payload FROM project_versions WHERE id=?',(sys.argv[2],)).fetchone()[0]);v['name']='下一期 · 协作体验';db.execute('UPDATE project_versions SET payload=? WHERE id=?',(json.dumps(v),v['id']))
`,version2.project_version?.id||version2.id);
 await page.waitForFunction(()=>document.querySelector('[data-task-row][data-id=item-access] .pool-title')?.textContent==='调整成员邀请与权限说明');
 await page.getByRole('button',{name:'平铺展示',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.requirement-pool.is-flat'));
 assert.equal(await page.locator('.pool-branch,.pool-family.pool-has-children').count(),0);assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 await page.getByRole('button',{name:'版本分组',exact:true}).click();
 const flatOrder=['item-alignment','item-search','bug-currency','bug-service','item-access','item-background',root.id,second.task.id,'bug-payment','iteration-service','item-timestamp'];
 assert.deepEqual(await rows().evaluateAll(nodes=>nodes.map(node=>node.dataset.id)),flatOrder,'flat mode must use the same priority and update-time ordering');
 await page.evaluate(async()=>{await Promise.all([...document.querySelectorAll('.pool-branch')].flatMap(el=>el.getAnimations()).map(animation=>animation.finished.catch(()=>{})));document.activeElement?.blur();scrollTo(0,0);});await page.screenshot({path:join(output,'mobile-flat.png'),fullPage:true});
 await page.setViewportSize({width:1024,height:900});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 await page.setViewportSize({width:1600,height:1180});await page.getByRole('button',{name:'层级展示',exact:true}).click();await page.locator('[data-task-row][data-id=bug-service]').waitFor();
 await page.getByRole('button',{name:'版本分组',exact:true}).click();
 // Finish the view-switch handler before observing an ordinary polling update.
 await page.evaluate(async()=>{await Promise.all(document.getAnimations().filter(animation=>animation.effect?.getComputedTiming().iterations!==Infinity).map(animation=>animation.finished.catch(()=>{})));});
 // A newly pending item moves during polling without replacing the search or
 // the existing keyed row.
 await page.locator('#task-search').focus();
 await page.evaluate(()=>{window.savedSearch=document.querySelector('#task-search');window.savedPendingRow=document.querySelector('[data-task-row][data-id=item-background]');});
 sql(`
t=json.loads(db.execute("SELECT payload FROM tasks WHERE id='item-background'").fetchone()[0]);t.update(status='aligning',updated_at=int(time.time()*1000)+30000);db.execute('UPDATE tasks SET payload=? WHERE id=?',(json.dumps(t),t['id']))
`);
 await page.waitForFunction(()=>document.querySelector('[data-task-row]')?.dataset.id==='item-background');
 const preserved=await page.evaluate(()=>({search:window.savedSearch===document.activeElement,row:window.savedPendingRow===document.querySelector('[data-task-row][data-id=item-background]')}));
 assert.deepEqual(preserved,{search:true,row:true},'priority refresh replaced focused search or row');
 assert.deepEqual(errors,[]);assert(!requests.some(url=>url.endsWith('/context')));
 const report={passed:true,priority_before_time:true,hierarchyOrder,flatOrder,priority_polling_preserves_nodes:true,equal_top_level_weight:weight,hierarchy_containment:true,visible_connectors:line,desktop_title_geometry:geometry,mobile_no_horizontal_scroll:true,long_titles_and_versions_fit:true,selection_only_in_dialog:true,keyboard_open:true,cross_version_visible:true,errors,preview:keepPreview?base+`/?page=tasks&project=${project.id}`:null};
 await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report));passed=true;
}catch(error){console.error('BROWSER',errors,log,await page.locator('body').innerText());throw error;}finally{await browser.close();if(keepPreview&&passed){server.unref();console.log('Preview retained at '+base);}else{server.kill('SIGTERM');await stopped;await rm(data,{recursive:true,force:true});}}
