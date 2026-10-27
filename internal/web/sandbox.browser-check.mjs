// Real project-settings persistence; synthetic completed run for presentation QA.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import net from "node:net";
import { once } from "node:events";

const load=createRequire(process.env.AI_PM_QA_NODE_MODULES ? join(process.env.AI_PM_QA_NODE_MODULES,"package.json") : import.meta.url);
const { chromium }=load("playwright");
const data=await mkdtemp(join(tmpdir(),"ai-pm-sandbox-ui-"));
const output=resolve("test-results/sandbox-ui");await mkdir(output,{recursive:true});
const reservation=net.createServer().listen(0,"127.0.0.1");await once(reservation,"listening");const port=reservation.address().port;await new Promise(done=>reservation.close(done));
const base=`http://127.0.0.1:${port}`;
const server=spawn(resolve(process.env.AI_PM_QA_BIN||"bin/ai-pm-sandbox-check"),["-addr",`127.0.0.1:${port}`,"-data",data,"-sync-interval","0"],{stdio:["ignore","ignore","pipe"]});
const exited=once(server,"exit");server.stderr.resume();
const browser=await chromium.launch({headless:true});const page=await browser.newPage({viewport:{width:1440,height:1000}});
const errors=[];page.on("pageerror",e=>errors.push(e.message));
async function api(path,body,method=body?"POST":"GET") {const r=await fetch(base+"/api"+path,{method,headers:{"Content-Type":"application/json"},body:body&&JSON.stringify(body)});const result=await r.json();assert(r.ok,JSON.stringify(result));return result;}
try {
  for(let i=0;i<100;i++){try{if((await fetch(base+"/api/health")).ok)break;}catch{}await new Promise(done=>setTimeout(done,50));}
  const profile=await api("/profiles",{name:"本地测试",provider:"openai",model:"fixture",key:"fixture-only"});
  await api("/platform/settings",{profile_id:profile.id},"PUT");
  const project=await api("/projects",{name:"项目源码阅读验证"});
  await page.goto(base);await page.getByRole("button",{name:"项目设置",exact:true}).click();
  await page.locator("[name=agent_read_mode]").selectOption("sandbox");
  const sha="87a06f5b73a919c22ca966f98754ab1744c3f457";
  await page.locator("[name=agent_ref]").fill(sha);
  await page.locator("#project-form button[type=submit]").click();
  await page.getByText("项目已保存",{exact:true}).waitFor();
  const saved=(await api(`/projects/${project.id}/context`)).project;
  assert.equal(saved.agent_read_mode,"sandbox");assert.equal(saved.agent_ref,sha);
  await page.reload();await page.getByRole("button",{name:"项目设置",exact:true}).click();await page.locator("[name=agent_ref]").waitFor();
  assert.equal(await page.locator("[name=agent_read_mode]").inputValue(),"sandbox");
  assert.equal(await page.locator("[name=agent_ref]").inputValue(),sha);
  await page.screenshot({path:join(output,"settings-desktop.png"),fullPage:true,animations:"disabled"});
  const fixture={id:"run_ui_fixture",project_id:project.id,conversation_id:"conv_ui_fixture",kind:"question",status:"succeeded",prompt:"核对相框合成服务",conversation_title:"源码阅读核验",started_at:Date.now(),output:"已核对源码中的图层顺序。",code_snapshot:{sha,ref:"main",status:"ready",synced_at:Date.now(),submodules:[{path:"mobile",status:"not_expanded"}]},steps:[{name:"find",status:"done",path:"server/services",sha},{name:"read",status:"done",path:"server/services/photo-compositor/service.py",sha},{name:"read",status:"failed",path:"/state",error:"PATH_OUTSIDE_WORKSPACE"}]};
  await page.route(`**/api/projects/${project.id}/runs`,route=>route.request().method()==="GET"?route.fulfill({contentType:"application/json",body:JSON.stringify([fixture])}):route.continue());
  await page.getByRole("button",{name:"项目问答",exact:true}).click();
  await page.locator('[data-action="select-conversation"][data-id="conv_ui_fixture"]').click();
  await page.locator(".code-snapshot").waitFor();
  assert.match(await page.locator(".code-snapshot").innerText(),/87a06f5b73a9/);
  await page.locator(".run-process-details summary").click();
  assert.match(await page.locator(".tool-tags").innerText(),/find ✓/);
  assert.match(await page.locator(".tool-tags span").last().getAttribute("title"),/PATH_OUTSIDE_WORKSPACE/);
  await page.screenshot({path:join(output,"reading-desktop.png"),fullPage:true,animations:"disabled"});
  await page.setViewportSize({width:390,height:844});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),"mobile overflow");
  await page.screenshot({path:join(output,"reading-mobile.png"),fullPage:true,animations:"disabled"});
  await page.locator("textarea[name=question]").fill("继续核对");
  await page.getByRole("button",{name:"发送",exact:true}).click();
  await page.getByText(/沙盒 runner 尚未配置/).waitFor();
  assert.equal(await page.locator("textarea[name=question]").inputValue(),"继续核对");
  assert.deepEqual(errors,[]);
  await writeFile(join(output,"report.json"),JSON.stringify({settings:"real API persistence and reload",reading:"synthetic SHA and native tool evidence rendered",mobile:"390px no overflow",failure:"runner unavailable reported, draft preserved",errors},null,2));
  console.log("sandbox UI passed: settings, version, native steps, mobile, fail-closed");
} finally {await browser.close();server.kill("SIGTERM");await exited;await rm(data,{recursive:true,force:true});}
