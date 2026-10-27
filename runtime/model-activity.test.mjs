import test from "node:test";
import assert from "node:assert/strict";
import { observeModelActivity } from "./model-activity.mjs";

test("provider activity excludes empty events and preserves stream contents", async () => {
  const events = [
    { type: "start" },
    { type: "text_start" },
    { type: "text_delta", delta: "" },
    { type: "text_delta", delta: "visible" },
    { type: "thinking_delta", delta: "private" },
    { type: "toolcall_delta", delta: '{"path":' },
    { type: "done" },
  ];
  const messages = [];
  const result = { role: "assistant", content: [] };
  const wrapped = observeModelActivity(async () => ({
    async *[Symbol.asyncIterator]() { yield* events; },
    result: async () => result,
  }), (message) => messages.push(message));
  const stream = await wrapped();
  assert.deepEqual(messages, [{ type: "model_start", id: "model_1" }]);
  const received = [];
  for await (const event of stream) {
    received.push(event);
    if (event.type === "done") assert.equal(await stream.result(), result);
  }
  assert.deepEqual(received, events);
  assert.deepEqual(messages.map((m) => m.type), ["model_start", "model_activity", "model_activity", "model_activity", "model_end"]);
  assert(!JSON.stringify(messages).includes("private"));
  // Compaction only reads result(), but its generated content still counts.
  assert.equal(await (await wrapped()).result(), result);
  assert.equal(messages.at(-1).id, "model_2");
  assert.equal(messages.filter((m) => m.type === "model_activity").length, 6);
});

test("provider setup and stream errors end the request and propagate", async () => {
  for (const failAt of ["setup", "stream"]) {
    const messages = [];
    const wrapped = observeModelActivity(async () => {
      if (failAt === "setup") throw new Error("fixture failure");
      return {
        async *[Symbol.asyncIterator]() { throw new Error("fixture failure"); },
        result: async () => ({}),
      };
    }, (message) => messages.push(message));
    await assert.rejects(async () => (await wrapped()).result(), /fixture failure/);
    assert.deepEqual(messages.map((m) => m.type), ["model_start", "model_end"]);
  }
});
