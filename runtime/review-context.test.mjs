import test from "node:test";
import assert from "node:assert/strict";
import { prepareReviewInput } from "./review-context.mjs";

const decode = value => JSON.parse(value.prompt.split("AI_PM_REVIEW_INPUT_V1\n").at(-1));
const entry = value => ({type:"message",message:{role:"user",content:[{type:"text",text:value.prompt}]}});
const config = () => ({prompt:"本轮实时项目概览",review_generation:1,review_context:{requirement:{id:"task-a",revision:1,description:"用户原文",attachments:[{id:"cover"}]},conversation:[{id:"m1",role:"user",body:"第一次补充"}]},review_files:[{id:"cover",kind:"image",image_index:0}],images:[{type:"image",data:"first"}]});

test("review continuation appends only new messages and attachments, including after an interrupted prompt", () => {
  const c = config(), first = prepareReviewInput(c,[]);
  assert.equal(decode(first).requirement.description,"用户原文");
  assert.equal(first.images.length,1);
  c.review_generation++;
  c.review_context.conversation.push({id:"a1",role:"assistant",body:"已经在 Pi 工具记录中"},{id:"m2",role:"user",body:"最新纠正",attachments:[{id:"note"}]});
  c.review_files.push({id:"note",kind:"image",image_index:1});
  c.images.push({type:"image",data:"second"});
  assert.deepEqual(decode(first).agenda,[]);
  c.review_context.agenda=[{id:"mode",title:"改价方式",kind:"direction",status:"resolved",conclusion:"一次性改价"}];
  const second=prepareReviewInput(c,[entry(first)]), body=decode(second);
  assert.deepEqual(body.conversation.map(m=>m.id),["m2"]);
  // The current agenda accompanies every round, even when the requirement is unchanged.
  assert.deepEqual(body.agenda,c.review_context.agenda);
  assert.deepEqual(body.requirement,{id:"task-a",revision:1});
  assert.deepEqual(second.images,[{type:"image",data:"second"}]);
  const retry=prepareReviewInput(c,[entry(first),entry(second)]);
  assert.deepEqual(decode(retry).conversation,[]);
  assert.deepEqual(retry.images,[]);
  c.review_context.requirement.revision=2;
  c.review_context.requirement.description="新版业务范围";
  assert.equal(decode(prepareReviewInput(c,[entry(first),entry(second)])).requirement.description,"新版业务范围");
});

test("legacy review bootstrap is bounded and explicitly points to older review pages", () => {
  const c=config();
  c.review_context.conversation=Array.from({length:95},(_,i)=>({id:"m"+i,role:"user",body:"历史补充"+i}));
  const first=prepareReviewInput(c,[]), body=decode(first);
  assert.equal(body.conversation.length,10);
  assert.equal(body.older_messages_available,true);
  assert.equal(body.through_message_id,"m94");
  c.review_context.conversation.push({id:"m95",role:"user",body:"新消息"});
  assert.deepEqual(decode(prepareReviewInput(c,[entry(first)])).conversation.map(m=>m.id),["m95"]);
});

test("accepted iteration documents and screenshots accompany the new requirement once", () => {
  const c=config();
  c.review_context.requirement.iteration={source:{id:"previous",attachments:[{id:"old-picture"},{id:"old-rules"}]}};
  c.review_files.push({id:"old-picture",kind:"image",image_index:1},{id:"old-rules",kind:"document",text:"已验收业务规则"});
  c.images.push({type:"image",data:"previous"});
  const first=prepareReviewInput(c,[]);
  assert.equal(first.images.length,2);
  assert(decode(first).documents.some(f=>f.text==="已验收业务规则"));
  assert.equal(prepareReviewInput(c,[entry(first)]).images.length,0);
});
