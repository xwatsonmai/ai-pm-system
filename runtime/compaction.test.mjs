import test from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import { spawn } from "node:child_process";
import { mkdtemp, rm, readdir, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import readline from "node:readline";

test("real Pi SDK compacts a continued session, reports lifecycle and preserves raw history", {timeout:45000}, async () => {
  const directory=await mkdtemp(path.join(tmpdir(),"ai-pm-compact-"));
  let normal=0, summaries=0, transientErrors=0;
  const requests=[];
  const server=http.createServer(async(req,res)=>{
    let raw="";for await(const chunk of req)raw+=chunk;
    const body=JSON.parse(raw);requests.push(body);
    const summary=JSON.stringify(body.messages[0]).toLowerCase().includes("summar");
    if (!summary && transientErrors===0) {
      transientErrors++;
      res.writeHead(500,{"Content-Type":"application/json"});
      res.end(JSON.stringify({error:{message:"fixture transient provider error"}}));
      return;
    }
    if(summary)summaries++;else normal++;
    const content=summary?"COMPACTED_FACTS: user requires preserving original decisions; continue from the latest request.":"Completed round "+normal;
    const tokens=summary?300:normal===3?3500:normal*1000;
    res.writeHead(200,{"Content-Type":"text/event-stream"});
    res.write("data: "+JSON.stringify({id:"fixture",choices:[{index:0,delta:{role:"assistant",content},finish_reason:null}]})+"\n\n");
    res.end("data: "+JSON.stringify({id:"fixture",choices:[{index:0,delta:{},finish_reason:"stop"}],usage:{prompt_tokens:tokens,completion_tokens:20,total_tokens:tokens+20}})+"\n\ndata: [DONE]\n\n");
  });
  await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));
  const run=prompt=>new Promise((resolve,reject)=>{
    const child=spawn(process.execPath,["worker.mjs"],{cwd:import.meta.dirname,stdio:["pipe","pipe","pipe"]});
    const messages=[];let stderr="";
    const timer=setTimeout(()=>{child.kill();reject(new Error("worker timed out: "+stderr));},12000);
    child.stderr.on("data",b=>stderr+=b);
    readline.createInterface({input:child.stdout}).on("line",line=>{try{messages.push(JSON.parse(line));}catch{}});
    child.on("error",reject);
    child.on("exit",code=>{clearTimeout(timer);if(code||messages.some(m=>m.type==="error"))reject(new Error(stderr+JSON.stringify(messages)));else resolve(messages);});
    child.stdin.write(JSON.stringify({type:"start",directory,conversation_id:"continued",profile:{provider:"fixture",model:"small-model",api:"openai-completions",base_url:`http://127.0.0.1:${server.address().port}/v1`,context_window:4096,max_tokens:512},system:"Answer the latest user request concisely.",prompt,tools:[]})+"\n");
  });
  try {
    const events=[];
    for(const prefix of ["FIRST_RAW_DECISION","SECOND_RAW_MESSAGE","THIRD_RAW_MESSAGE"]){events.push(...await run(prefix+"x".repeat(3900)));}
    assert(summaries>0,"SDK never requested compaction");
    assert.equal(transientErrors,1,"transient retry path was not exercised");
    assert(events.some(e=>e.type==="compaction"&&e.status==="running"));
    assert(events.some(e=>e.type==="compaction"&&e.status==="succeeded"));
    await run("Continue with the saved decisions.");
    const last=JSON.stringify(requests.at(-1).messages);
    assert(last.includes("COMPACTED_FACTS"));
    assert(!last.includes("FIRST_RAW_DECISION"),"old raw prompt was replayed after compaction");
    const sessionDir=path.join(directory,"sessions","continued");
    const files=await readdir(sessionDir);
    const transcript=await readFile(path.join(sessionDir,files.find(f=>f.endsWith(".jsonl"))),"utf8");
    assert(transcript.includes('"type":"compaction"'));
    assert(transcript.includes("FIRST_RAW_DECISION"),"compaction destroyed the original record");
  } finally {await new Promise(resolve=>server.close(resolve));await rm(directory,{recursive:true,force:true});}
});
