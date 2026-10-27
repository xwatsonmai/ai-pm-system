import test from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import { spawn } from "node:child_process";
import { mkdtemp, rm, readdir, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import readline from "node:readline";

test("Pi ends accepted review batches, retries rejected submissions and resumes saved sessions", {timeout:45000}, async () => {
  const directory = await mkdtemp(path.join(tmpdir(), "ai-pm-review-complete-"));
  const requests = [];
  let accepted = 0;
  let rejectFirst = true;
  const server = http.createServer(async (req, res) => {
    let raw = "";
    for await (const chunk of req) raw += chunk;
    const body = JSON.parse(raw);
    requests.push(body);
    // A request after an accepted submission reproduces the formerly failing
    // upstream sign-off. No further request is needed until the next user input.
    const last = body.messages.at(-1);
    if (last.role === "tool" && !last.content.includes("Rejected fixture")) {
      res.writeHead(500).end("Unexpected redundant completion request");
      return;
    }
    const names = accepted === 0 ? ["submit_requirement_review"] : ["project_context", "submit_requirement_review"];
    res.writeHead(200, {"Content-Type":"text/event-stream"});
    const calls = names.map((name,index) => ({index,id:`call_${requests.length}_${index}`,type:"function",function:{name,arguments:"{}"}}));
    for (const [delta,finish_reason] of [[{role:"assistant",tool_calls:calls},null],[{},"tool_calls"]]) {
      res.write("data: "+JSON.stringify({id:"fixture",choices:[{index:0,delta,finish_reason}]})+"\n\n");
    }
    res.end("data: [DONE]\n\n");
  });
  await new Promise(resolve => server.listen(0,"127.0.0.1",resolve));
  const config = {
    type:"start",directory,conversation_id:"review",review_generation:1,
    profile:{provider:"fixture",model:"fixture-model",base_url:`http://127.0.0.1:${server.address().port}/v1`,api:"openai-completions"},
    api_key:"fixture",system:"Review and submit",prompt:"Review this requirement",
    review_context:{requirement:{id:"task",revision:1},conversation:[{id:"user1",role:"user",body:"Initial requirement"}]},
    tools:["project_context","submit_requirement_review"].map(name => ({name,description:name,schema:{type:"object",properties:{}}})),
  };
  const run = () => new Promise((resolve,reject) => {
    const child = spawn(process.execPath,["worker.mjs"],{cwd:import.meta.dirname,stdio:["pipe","pipe","pipe"]});
    let stderr = "";
    const events = [];
    const timer = setTimeout(() => {child.kill();reject(new Error("Worker timed out "+stderr));},20000);
    child.stderr.on("data",chunk => stderr += chunk);
    readline.createInterface({input:child.stdout}).on("line",line => {
      const event = JSON.parse(line);
      events.push(event);
      if (event.type === "tool_call") {
        const result = {type:"tool_result",id:event.id,data:{project:"fixture"}};
        if (event.name === "submit_requirement_review") {
          if (rejectFirst) {result.error="Rejected fixture";rejectFirst=false;}
          else {result.data={received:true};accepted++;}
        }
        child.stdin.write(JSON.stringify(result)+"\n");
      }
    });
    child.on("error",reject);
    child.on("exit",code => {
      clearTimeout(timer);
      if (code !== 0) reject(new Error(stderr || JSON.stringify(events)));
      else resolve(events);
    });
    child.stdin.write(JSON.stringify(config)+"\n");
  });
  try {
    const first = await run();
    assert.equal(requests.length,2,"only rejected submissions request correction");
    assert.equal(accepted,1);
    assert(first.some(e => e.type === "done" && !e.stopped));
    assert(!first.some(e => e.type === "error"));
    const files = await readdir(path.join(directory,"sessions","review"));
    const saved = await readFile(path.join(directory,"sessions","review",files.find(f => f.endsWith(".jsonl"))),"utf8");
    assert(saved.includes('"received":true') || saved.includes('\\"received\\":true'),"accepted tool result persists before ending");
    config.review_generation = 2;
    config.review_context.conversation.push({id:"user2",role:"user",body:"Correction to earlier scope"});
    const second = await run();
    assert.equal(requests.length,3,"a mixed batch also ends without redundant completion");
    assert.equal(accepted,2);
    assert(second.some(e => e.type === "done" && !e.stopped));
    assert(!second.some(e => e.type === "error"));
    assert(JSON.stringify(requests.at(-1).messages).includes("Correction to earlier scope"));
    assert(JSON.stringify(requests.at(-1).messages).includes("Initial requirement"));
  } finally {
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
    await rm(directory,{recursive:true,force:true});
  }
});
