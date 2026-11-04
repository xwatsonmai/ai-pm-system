// Isolated end-to-end checks: no production data, model calls or deployment.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp,mkdir,writeFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {once} from 'node:events';
import net from 'node:net';

const load=createRequire(process.env.AI_PM_QA_NODE_MODULES?join(process.env.AI_PM_QA_NODE_MODULES,'package.json'):import.meta.url);
const {chromium}=load('playwright');
const data=await mkdtemp(join(tmpdir(),'ai-pm-overall-')),out=resolve('test-results/overall-optimization');await mkdir(out,{recursive:true});
const reservation=net.createServer().listen(0,'127.0.0.1');await once(reservation,'listening');const port=reservation.address().port;await new Promise(done=>reservation.close(done));
const site=`http://127.0.0.1:${port}`;
await writeFile(join(data,'auth.json'),JSON.stringify({provider:'local',public_url:site}),{mode:0o600});
const server=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-overall-check'),['-addr',`127.0.0.1:${port}`,'-data',data,'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});
const serverExit=once(server,'exit');let log='';server.stderr.on('data',chunk=>{log+=chunk;});
const browser=await chromium.launch({headless:true}),admin=await browser.newPage({viewport:{width:1440,height:1050},reducedMotion:'reduce'}),errors=[];
browser.on('page',page=>page.on('pageerror',e=>errors.push(e.message)));admin.on('pageerror',e=>errors.push(e.message));
const report={};
async function api(page,path,body,method,want=200){const r=await page.request.fetch(site+'/api'+path,{method:method||(body?'POST':'GET'),...(body?{data:body}:{})});const v=await r.json();assert.equal(r.status(),want,`${path}: ${JSON.stringify(v)}`);return v;}
async function nav(page,id){await page.goto(`${site}/?page=${id}&project=${project.id}`);await page.locator('#page').waitFor();await page.locator(`.nav [data-page="${id}"][aria-current="page"]`).waitFor();}
async function roleList(){return api(admin,`/projects/${project.id}/roles`);}
let project;
try{
  for(let i=0;i<120;i++){try{if((await fetch(site+'/api/health')).ok)break;}catch{}await new Promise(done=>setTimeout(done,50));}
  const setup=log.match(/初始化口令 (\S+) /)?.[1];assert(setup,'missing local setup token');
  await api(admin,'/auth/setup',{token:setup,username:'admin',name:'测试管理员',password:'admin-pass-1'});
  project=await api(admin,'/projects',{name:'整体改造验证',description:'初始说明',branch:'main'});
  for(let i=0;i<43;i++)await api(admin,`/projects/${project.id}/tasks`,{request_id:crypto.randomUUID(),title:`分页需求 ${String(i).padStart(2,'0')}`,description:'测试正文',acceptance:['验收项'],kind:'requirement'});

  await nav(admin,'settings');await admin.locator('#project-settings-form [name=name]').fill('新的项目名称');await admin.locator('#project-settings-form [name=description]').fill('新的项目说明');await admin.locator('#project-settings-form button[type=submit]').click();await admin.getByText('项目设置已保存',{exact:true}).waitFor();
  assert.equal(await admin.locator('#page [name=repository]').count(),0);
  await nav(admin,'git');assert.equal(await admin.locator('#page [name=name]').count(),0);await admin.locator('#git-form [name=branch]').fill('develop');await admin.locator('#git-form button[type=submit]').click();await admin.getByText('Git 设置已保存',{exact:true}).waitFor();
  const saved=(await api(admin,'/projects')).find(p=>p.id===project.id);assert.equal(saved.name,'新的项目名称');assert.equal(saved.description,'新的项目说明');assert.equal(saved.branch,'develop');
  await admin.reload();await admin.locator('#git-form').waitFor();assert.equal(await admin.locator('#git-form [name=branch]').inputValue(),'develop');
  await admin.locator('[data-action=toggle-nav-group][data-group=requirements]').click();assert.equal(await admin.locator('#nav-group-requirements').isHidden(),true);await admin.reload();await admin.locator('#git-form').waitFor();assert.equal(await admin.locator('#nav-group-requirements').isHidden(),true);
  report.settings='独立保存项目/Git配置互不覆盖，直达与刷新恢复、折叠状态保存';

  await nav(admin,'access');await admin.locator('#project-roles .role-row').first().waitFor();
  await admin.locator('[data-action=access-create-role]').click();await admin.locator('#access-role-form [name=name]').fill('版本观察员');await admin.locator('#access-role-form [name=description]').fill('只访问版本页面');await admin.locator('#access-role-form [name=pages][value=versions]').check();await admin.locator('#access-role-form button[type=submit]').click();await admin.locator('#access-role-form').waitFor({state:'detached'});
  const role=(await roleList()).items.find(r=>r.name==='版本观察员');assert(role);
  const row=admin.locator('.role-row').filter({hasText:'版本观察员'});await row.locator('[data-action=access-edit-role]').click();await admin.locator('#access-role-form [name=name]').fill('版本审阅员');await admin.locator('#access-role-form button[type=submit]').click();await admin.locator('#access-role-form').waitFor({state:'detached'});assert.equal((await roleList()).items.find(r=>r.id===role.id).name,'版本审阅员');
  const account=await api(admin,'/platform/accounts',{username:'member',name:'项目成员',password:'temporary-1'});
  const access=await api(admin,`/projects/${project.id}/access`);
  await api(admin,`/projects/${project.id}/members/${account.user_id}`,{roles:[role.id],responsibilities:['developer'],version:access.version},'PUT');
  await admin.reload();await admin.locator('.member-row').filter({hasText:'项目成员'}).waitFor();
  await admin.locator('.member-row').filter({hasText:'项目成员'}).locator('[data-action=access-edit-member]').click();await admin.locator('[data-role-choice]').first().waitFor();assert(await admin.locator('[name=responsibilities][value=developer]').isChecked());assert.equal(await admin.locator('.role-selected').innerText(),'版本审阅员 ×');await admin.locator('#access-member-form button[type=submit]').click();await admin.locator('#access-member-form').waitFor({state:'detached'});

  // Separate contexts are essential: this user must not share administrator cookies.
  const memberContext=await browser.newContext({viewport:{width:1440,height:1050}}),member=await memberContext.newPage();
  await api(member,'/auth/login',{username:'member',password:'temporary-1'});await api(member,'/auth/password',{current_password:'temporary-1',new_password:'member-own-pass'});
  await nav(member,'versions');assert.equal(await member.locator('.nav [data-page=tasks]').count(),0);assert.equal(await member.locator('.nav [data-page=access]').count(),0);assert.equal(await member.locator('.nav [data-group=management]').count(),0);
  await api(member,`/projects/${project.id}/git`,{branch:'unauthorized'},'PATCH',403);
  const context=await api(member,`/projects/${project.id}/context`);assert.equal((context.runs||[]).length,0);assert.equal((context.artifacts||[]).length,0);
  await member.goto(`${site}/?page=access&project=${project.id}`);await member.locator('.nav [data-page=versions][aria-current=page]').waitFor();assert.equal(await member.locator('#project-roles').count(),0);
  const firstTask=(await api(admin,`/projects/${project.id}/tasks?page=1&page_size=20`)).items[0];
  await member.goto(`${site}/?page=versions&project=${project.id}&task=${firstTask.id}`);await member.locator('.detail-title').waitFor();
  assert.equal(await member.locator('dialog [data-action=edit-task]').count(),0);assert.equal(await member.locator('#report-form').count(),0);
  const currentAccess=await api(admin,`/projects/${project.id}/access`);
  await api(admin,`/projects/${project.id}/roles/${role.id}`,{name:'版本审阅员',description:'已撤销页面访问',pages:[],version:currentAccess.version},'PUT');
  await member.locator('.nav [data-page=versions]').waitFor({state:'detached',timeout:15000});assert.equal(await member.locator('.detail-title').count(),0);
  await api(member,`/projects/${project.id}/tasks?page=1&page_size=20`,undefined,'GET',403);
  await nav(admin,'access');await admin.locator('.role-row').filter({hasText:'版本审阅员'}).locator('[data-action=access-delete-role]').click();await admin.locator('#access-delete-role-form [name=replacement_role_id]').selectOption('developer');await admin.locator('#access-delete-role-form button[type=submit]').click();await admin.locator('#access-delete-role-form').waitFor({state:'detached'});
  assert.equal((await roleList()).items.some(r=>r.id===role.id),false);
  const migrated=(await api(admin,`/projects/${project.id}/members`)).items.find(m=>m.user.id===account.user_id);assert.deepEqual(migrated.roles,['developer']);assert.deepEqual(migrated.responsibilities,['developer']);
  report.permissions='角色创建/改名、成员角色与职责独立保存、限制菜单/直达/API，普通用户使用独立会话验证';
  report.revocation='已有详情和菜单在撤权后清除，后端立即拒绝；删除已分配角色时迁移成员并保留职责';

  await nav(admin,'tasks');await admin.locator('.task-table tbody tr').first().waitFor();assert.equal(await admin.locator('.task-table tbody tr').count(),20);
  const response=await api(admin,`/projects/${project.id}/tasks?page=2&page_size=20`);assert.equal(response.items.length,20);assert.equal(response.total,43);assert(response.items.every(t=>!t.description),'page query must not return full task documents');
  report.pagination='43条真实需求，列表首屏20条，第二页20条，响应为当前页摘要';

  await admin.setViewportSize({width:390,height:844});await nav(admin,'access');await admin.locator('#project-roles .role-row').first().waitFor();assert(await admin.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await admin.screenshot({path:join(out,'access-mobile.png'),fullPage:true});
  await admin.locator('[data-action=menu]').click();await admin.locator('.sidebar.open').waitFor();assert(await admin.locator('[data-action=toggle-nav-group][data-group=management]').isVisible());await admin.screenshot({path:join(out,'navigation-mobile.png')});
  await admin.setViewportSize({width:1440,height:1050});await nav(admin,'access');await admin.screenshot({path:join(out,'access-desktop.png'),fullPage:true});
  report.mobile='390px页面不横向溢出，折叠菜单与权限页可用';
  assert.deepEqual(errors,[]);await writeFile(join(out,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report,null,2));
}catch(error){await admin.screenshot({path:join(out,'failure.png'),fullPage:true}).catch(()=>{});console.error(log.slice(-3000));throw error;}
finally{await browser.close();server.kill('SIGTERM');await serverExit;await rm(data,{recursive:true,force:true});}
