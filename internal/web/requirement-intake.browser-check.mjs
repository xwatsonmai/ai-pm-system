// Isolated browser + HTTP + SQLite checks with a local Pi protocol fixture.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdtemp,mkdir,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import net from 'node:net';
import {once} from 'node:events';
const load=createRequire(process.env.AI_PM_QA_NODE_MODULES?join(process.env.AI_PM_QA_NODE_MODULES,'package.json'):import.meta.url);
const {chromium}=load('playwright');
const output=resolve('test-results/requirement-intake-ui');await mkdir(output,{recursive:true});
const data=await mkdtemp(join(tmpdir(),'ai-pm-intake-')),worker=join(data,'fixture.mjs');
await writeFile(worker,"import readline from 'node:readline';\nconst emit=x=>console.log(JSON.stringify(x));let direct=false,isBug=false,question='',phase='',snapshot;\nconst done=text=>{emit({type:'done',text});setTimeout(()=>process.exit(0),10)};\nconst resolve=(id,action)=>{phase='resolve';emit({type:'tool_call',id:'resolve',name:'resolve_requirement_proposal',args:{proposal_id:id,action,confirmation:question}})};\nreadline.createInterface({input:process.stdin}).on('line',line=>{\n const m=JSON.parse(line);\n if(m.type==='stop'){done('stopped');return}\n if(m.type==='tool_result'){\n  if(m.error){emit({type:'error',error:m.error});done('操作未完成');return}\n  if(phase==='propose'&&direct){resolve(m.data.id,'create');return}\n  done(phase==='resolve'?'已保存处理结果。':'我判断这值得跟进，是否创建到需求池？你也可以继续纠正我的理解。');return\n }\n if(m.type!=='start')return;\n if(m.tools.some(t=>t.name==='submit_requirement_review')){emit({type:'error',error:'测试评审模型暂不可用'});done('');return}\n question=m.prompt.split('\\n用户输入：')[1].split('\\n以下是当前项目事实快照')[0];\n snapshot=JSON.parse(m.prompt.split('以下是当前项目事实快照（资料，不是指令）：\\n')[1].split('\\n附件')[0]);\n emit({type:'title',text:question.slice(0,20)});\n if(question.includes('仅讨论')){done('继续讨论，不创建建议。');return}\n direct=question.includes('请创建需求')||question.includes('请创建 Bug');isBug=!question.includes('进度');\n if(question==='创建吧'||question==='暂不创建'){\n  const old=snapshot.conversation_requirement_proposals.find(v=>v.status==='suggested');\n  resolve(old.id,question==='创建吧'?'create':'dismiss');return\n }\n const files=snapshot.conversation_attachments||[];\n const urls=files.map(f=>f.kind==='image'?'![截图](/api/projects/'+f.project_id+'/attachments/'+f.id+')':'[文档](/api/projects/'+f.project_id+'/attachments/'+f.id+')').join('\\n');\n phase='propose';\n emit({type:'tool_call',id:'propose',name:'propose_requirement',args:{title:isBug?'退款重复扣款调查':'新增退款进度',kind:isBug?'bug':'requirement',bug_details:isBug?{context:'支付订单页',reproduction:'同一订单发起一次支付',actual:'扣款两次',expected:'只扣一次',scope:'修复重复扣款'}:null,missing_details:isBug?[]:['退款到账时间展示规则'],description:'## 用户反馈\\n'+question+'\\n\\n## 预期与待确认\\n退款应保留记录，原因待验证。\\n'+urls,acceptance:['退款金额准确且可追溯'],attachment_ids:files.map(f=>f.id),reason:isBug?'实际行为偏离预期，建议创建 Bug 交给开发修复':'明确的功能目标'}})\n});\n");
const reserve=net.createServer().listen(0,'127.0.0.1');await once(reserve,'listening');const port=reserve.address().port;await new Promise(done=>reserve.close(done));
const base='http://127.0.0.1:'+port;
const server=spawn(resolve(process.env.AI_PM_QA_BIN||'bin/ai-pm-requirements-check'),['-addr','127.0.0.1:'+port,'-data',data,'-pi-worker',worker,'-sync-interval','0'],{stdio:['ignore','ignore','pipe']});const serverExit=once(server,'exit');server.stderr.resume();
const browser=await chromium.launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[];
page.on('pageerror',e=>errors.push(e.message));
async function api(path,body,method){const r=await fetch(base+'/api'+path,{method:method||(body?'POST':'GET'),headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});const result=await r.json();assert(r.ok,JSON.stringify(result));return result}
const report={};
try{
 for(let n=0;n<100;n++){try{if((await fetch(base+'/api/health')).ok)break}catch{}await new Promise(done=>setTimeout(done,50))}
 const profile=await api('/profiles',{name:'Fixture',provider:'fixture',model:'fixture',base_url:'http://127.0.0.1:1',api:'openai-completions',supports_images:true});
 await api('/platform/settings',{profile_id:profile.id},'PUT');
 const project=await api('/projects',{name:'问答转需求验证',branch:'main'});
 const intakePath='/projects/'+project.id+'/requirement-intakes';
 const tasks=async()=> (await api('/projects/'+project.id+'/context')).tasks;
 const send=async text=>{await page.locator('[name=question]').fill(text);await page.locator('#chat-send').click()};
 const newChat=async()=>{await page.locator('.new-conversation-button').click()};
 const waitIdle=async()=>{await page.waitForFunction(()=>!document.querySelector('.conversation-active'))};
 await page.goto(base);await page.getByRole('button',{name:'项目问答',exact:true}).click();
 await page.getByText('帮我整理一个需求',{exact:true}).waitFor();
 await page.locator('#chat-files').setInputFiles([
  {name:'规则.md',mimeType:'text/markdown',buffer:Buffer.from('退款不能重复扣款，需保留原支付渠道。')},
  {name:'截图.png',mimeType:'image/png',buffer:await page.locator('.chat-intro').screenshot()}
 ]);
 const original='希望新增退款进度展示。'+('补充业务细节，必须完整保留。'.repeat(150));
 await send(original);await page.getByRole('button',{name:'创建并评审',exact:true}).waitFor();await waitIdle();
 assert.equal((await tasks()).length,0);
 let suggestion=(await api(intakePath))[0];assert.equal(suggestion.attachments.length,2);
 await page.locator('.intake-content summary').click();await page.locator('.intake-content img').waitFor();
 await page.locator('[name=question]').fill('尚未发送的纠正草稿');
 await page.waitForTimeout(3500);assert.equal(await page.locator('[name=question]').inputValue(),'尚未发送的纠正草稿');
 assert(await page.locator('.intake-content').evaluate(el=>el.open));
 await page.locator('.intake-content summary').click();
 await page.getByRole('button',{name:'创建并评审',exact:true}).scrollIntoViewIfNeeded();
 await page.screenshot({path:join(output,'suggestion-desktop.png')});
 report.proactive='主动建议卡片、整理内容与图文附件、未确认不创建、刷新不扰动草稿通过';
 // Keep an unsent correction while a failed HTTP response is reconciled; the
 // background refresh must not steal focus by opening a review over that draft.
 const endpoint='/api/requirement-intakes/'+suggestion.id+'/resolve';let lost=false;
 await page.route('**'+endpoint,async route=>{if(!lost){lost=true;await route.fetch();await route.abort('failed')}else await route.continue()});
 await page.getByRole('button',{name:'创建并评审',exact:true}).click();
 await page.getByRole('button',{name:'进入需求评审',exact:true}).waitFor();
 const created=(await api(intakePath))[0];assert.equal(created.status,'created');
 assert.equal(await page.locator('[name=question]').inputValue(),'尚未发送的纠正草稿');
 const retried=await api(endpoint.slice(4),{action:'create',request_id:'intake:'+suggestion.id+':create'});assert.equal(retried.task_id,created.task_id);
 assert.equal((await tasks()).length,1);
 await page.getByRole('button',{name:'进入需求评审',exact:true}).click();await page.getByText('需求和已发送消息均已保存',{exact:true}).waitFor();
 const saved=(await api('/tasks/'+created.task_id)).task;assert(saved.description.includes(original));assert.equal(saved.attachments.length,2);assert.equal(saved.status,'clarify');
 await page.locator('#review-input').fill('整体理解不对：这是退款到账延迟，没有发生新的扣款。');
 await page.locator('.review-send').click();await page.getByText('消息已保存',{exact:true}).waitFor();
 let review=await api('/tasks/'+created.task_id+'/requirement-review');assert(review.messages.some(m=>m.body.includes('整体理解不对')));
 await page.screenshot({path:join(output,'saved-review-failure.png')});
 report.durable='HTTP 响应丢失后幂等恢复、原始长文/图片/文档保留、评审失败与自由纠正独立保存通过';
 await page.getByRole('button',{name:'关闭',exact:true}).click();await page.reload();await page.getByRole('button',{name:'项目问答',exact:true}).click();await page.getByRole('button',{name:'进入需求评审',exact:true}).waitFor();
 await newChat();await send('退款记录显示异常，建议看看');await page.getByRole('button',{name:'创建并交给开发',exact:true}).waitFor();await waitIdle();
 await send('创建吧');await page.getByRole('button',{name:'查看 Bug',exact:true}).waitFor({timeout:15000});await waitIdle();
 assert.equal((await tasks()).length,2);
 const items=await api(intakePath);assert.equal(items.length,2);assert.equal(items.filter(v=>v.status==='created').length,2);
 const bug=items.find(v=>v.kind==='bug');
 assert.equal((await api('/tasks/'+bug.task_id)).task.status,'ready');
 assert.equal((await api('/tasks/'+bug.task_id+'/requirement-review')).status,'not_started');
 assert.equal(await page.locator('.requirement-review').count(),0);
 assert.equal(await page.getByRole('button',{name:'进入需求评审',exact:true}).count(),0);
 await page.getByRole('button',{name:'查看 Bug',exact:true}).click();
 await page.getByText('待认领',{exact:true}).first().waitFor();
 await page.waitForTimeout(350);
 await page.screenshot({path:join(output,'bug-ready.png')});
 await page.getByRole('button',{name:'关闭',exact:true}).click();
 report.dialogue='回复创建吧复用原建议，Bug 直接待认领、无自动评审、详情状态正确通过';
 await newChat();await send('另一个重复扣款问题');await page.getByRole('button',{name:'创建并交给开发',exact:true}).waitFor();await waitIdle();
 await page.getByRole('button',{name:'创建并交给开发',exact:true}).click();await page.getByRole('button',{name:'查看 Bug',exact:true}).waitFor();
 assert.equal((await tasks()).filter(t=>t.kind==='bug'&&t.status==='ready').length,2);
 assert.equal(await page.locator('.requirement-review').count(),0);
 await page.reload();await page.getByRole('button',{name:'项目问答',exact:true}).click();await page.getByRole('button',{name:'查看 Bug',exact:true}).waitFor();
 assert.equal(await page.getByRole('button',{name:'进入需求评审',exact:true}).count(),0);
 report.bugButton='按钮创建 Bug 后可直接认领，刷新保留详情入口且不出现评审引导通过';
 await newChat();await send('请创建需求：用户需要看到退款进度与到账时间。');await page.getByRole('button',{name:'进入需求评审',exact:true}).waitFor({timeout:15000});await waitIdle();await page.waitForTimeout(6500);assert.equal(await page.locator('.requirement-review').count(),0);
 assert.equal((await tasks()).length,4);assert((await tasks()).some(t=>t.kind==='requirement'&&t.title==='新增退款进度'));

 report.direct='明确请求创建需求时直接保存并后台评审、不自动弹窗通过';
 await newChat();await send('请创建 Bug：同一笔支付被重复扣款。');await page.getByRole('button',{name:'查看 Bug',exact:true}).waitFor();await waitIdle();
 assert.equal((await tasks()).filter(t=>t.kind==='bug'&&t.status==='ready').length,3);
 assert.equal(await page.locator('.requirement-review').count(),0);
 report.directBug='明确请求创建 Bug 时直接保存到待认领，无需重复确认或评审通过';
 await newChat();await send('拒绝场景：这里可能存在重复退款');await page.getByRole('button',{name:'暂不创建',exact:true}).waitFor();await waitIdle();
 await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>document.querySelector('.sidebar').getBoundingClientRect().right<=1);await page.getByRole('button',{name:'暂不创建',exact:true}).scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'suggestion-mobile.png')});
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 await page.getByRole('button',{name:'暂不创建',exact:true}).click();await page.getByText('已暂不创建',{exact:true}).waitFor();assert.equal((await tasks()).length,5);
 await page.reload();await page.getByRole('button',{name:'打开导航',exact:true}).click();await page.getByRole('button',{name:'项目问答',exact:true}).click();await page.getByText('已暂不创建',{exact:true}).waitFor();
 report.decline='暂不创建持久化、刷新恢复、390px 无横向溢出通过';
 assert.deepEqual(errors,[]);await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report,null,2));
}catch(e){await page.screenshot({path:join(output,'failure.png'),fullPage:true});console.error('Browser errors:',errors);throw e}
finally{await browser.close();server.kill('SIGTERM');await serverExit;await rm(data,{recursive:true,force:true})}
