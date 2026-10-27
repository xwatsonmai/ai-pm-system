import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile, symlink, rm, realpath } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { readonlyTools } from "./readonly-tools.mjs";

test("Pi readonly tools preserve native reading, path bounds and evidence", async () => {
  const dir = await realpath(await mkdtemp(path.join(os.tmpdir(), "pi-scope-")));
  try {
    const root = path.join(dir, "source");
    await mkdir(path.join(root, "server/services/photo-compositor"), { recursive:true });
    await writeFile(path.join(dir,"private.txt"), "private-sentinel");
    await writeFile(path.join(root,"long.txt"), Array.from({length:4000},(_,i)=>`line-${i+1}`).join("\n"));
    await writeFile(path.join(root,"server/services/photo-compositor/service.py"), "canvas.alpha_composite(background)\ncanvas.alpha_composite(photo)\ncanvas.alpha_composite(frame)\n");
    await symlink("../private.txt",path.join(root,"escape"));
    await symlink("long.txt",path.join(root,"internal"));
    let calls=0; const events=[];
    const tools = Object.fromEntries((await readonlyTools(root,{sha:"a".repeat(40)},()=>{calls++},e=>events.push(e))).map(t=>[t.name,t]));
    const invoke = (name,args) => tools[name].execute(`call-${calls}`,args,new AbortController().signal,undefined,{cwd:dir});
    const result = await invoke("read",{path:"long.txt",offset:3500,limit:2});
    assert.equal(JSON.parse(result.content[0].text).sha,"a".repeat(40));
    assert.match(result.content[1].text,/line-3500\nline-3501/);
    assert.doesNotMatch(result.content[1].text,/line-3502/);
    await invoke("read",{path:"internal",limit:1});
    for(const name of Object.keys(tools)) {
      await assert.rejects(invoke(name,{path:"../private.txt",pattern:"*"}),/PATH_OUTSIDE_WORKSPACE/);
      await assert.rejects(invoke(name,{path:path.join(dir,"private.txt"),pattern:"*"}),/PATH_OUTSIDE_WORKSPACE/);
      await assert.rejects(invoke(name,{path:"escape",pattern:"*"}),/SYMLINK_OUTSIDE_WORKSPACE/);
      await assert.rejects(invoke(name,{path:"missing",pattern:"*"}),/PATH_NOT_FOUND/);
    }
    const listing=await invoke("ls",{path:"server/services"});
    assert.match(JSON.stringify(listing),/photo-compositor/);
    assert.equal(events.filter(e=>e.type==="native_tool_start").length,calls);
    assert.equal(events.filter(e=>e.type==="native_tool_end").length,calls);
    assert.equal(events.filter(e=>e.error).length,16);
  } finally { await rm(dir,{recursive:true,force:true}); }
});

test("Pi grep and find use preinstalled rg/fd on the complete tree", {
  skip: spawnSync("rg",["--version"]).status!==0 || spawnSync("fd",["--version"]).status!==0 ? "rg/fd required; Worker image runs this check with both installed" : false,
}, async () => {
  const dir = await realpath(await mkdtemp(path.join(os.tmpdir(),"pi-search-")));
  try {
    await mkdir(path.join(dir,"server/services/photo-compositor"),{recursive:true});
    await writeFile(path.join(dir,"server/services/photo-compositor/service.py"),"canvas.alpha_composite(background)\ncanvas.alpha_composite(photo)\n");
    const tools=await readonlyTools(dir,{sha:"b".repeat(40)},()=>{},()=>{});
    const run=(name,args)=>tools.find(t=>t.name===name).execute(name,args,new AbortController().signal);
    const found=await run("find",{pattern:"**/service.py"});
    assert.match(JSON.stringify(found),/server\/services\/photo-compositor\/service.py/);
    const matches=await run("grep",{path:"server/services",pattern:"alpha_composite\\((background|photo)",context:1});
    assert.match(JSON.stringify(matches),/service.py:1/);
    assert.match(JSON.stringify(matches),/service.py:2/);
  } finally { await rm(dir,{recursive:true,force:true}); }
});
