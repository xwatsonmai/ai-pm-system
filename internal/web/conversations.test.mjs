import test from "node:test";
import assert from "node:assert/strict";
import { chatConversations } from "./static/conversations.mjs";

test("history groups only question turns, retains long conversations and uses the first AI title", () => {
  const runs = Array.from({length: 15}, (_, i) => ({id: `r${i}`, conversation_id: "a", kind: "question", owner_id: "alice", started_at: i, prompt: `turn ${i}`, status: "succeeded", ...(i === 0 ? {conversation_title: "AI 会话标题", title_status: "ready"} : {})}));
  runs.push({id: "b1", conversation_id: "b", kind: "question", started_at: 20, prompt: "另一个问题", status: "running"});
  runs.push({id: "analysis", kind: "progress", started_at: 30});
  const groups = chatConversations(runs.reverse());
  assert.equal(groups.length, 2);
  assert.equal(groups[0].id, "b");
  assert.equal(groups[0].active, true);
  assert.equal(groups[1].title, "AI 会话标题");
  assert.equal(groups[1].turns.length, 15);
  assert.equal(groups[1].latest.id, "r14");
});

test("empty drafts never appear; legacy and attachment-only messages remain readable", () => {
  assert.deepEqual(chatConversations([]), []);
  const [legacy] = chatConversations([{id: "old", kind: "question", started_at: 1, attachments: [{name: "错误截图.png"}]}]);
  assert.equal(legacy.id, "old");
  assert.equal(legacy.title, "错误截图.png");
  assert.equal(legacy.legacy, true);
});
