import test from "node:test";
import assert from "node:assert/strict";
import { streamRun, waitForPoll } from "./static/run-stream.mjs";

class Source extends EventTarget {
  static instances = [];
  constructor(url) { super(); this.url = url; Source.instances.push(this); }
  close() { this.closed = true; }
  send(run, append = false, process_append = false) {
    this.dispatchEvent(new MessageEvent("run", { data: JSON.stringify({ run, append, process_append }) }));
  }
}
const run = (output, status = "running") => ({ id: "one", output, status });

test("process deltas survive message boundaries and final output replacement", async () => {
  const received = [];
  const promise = streamRun("one", (r) => received.push(r), new AbortController().signal, Source);
  const source = Source.instances.at(-1);
  source.send(run("检查资料🙂"));
  source.send({ ...run(""), process_output: "检查资料🙂" }, false, true);
  source.send({ ...run("检查版本"), process_output: "" }, true, true);
  source.send({ ...run("结论", "succeeded"), process_output: "\n\n检查版本" }, false, true);
  await promise;
  assert.equal(received.at(-1).output, "结论");
  assert.equal(received.at(-1).process_output, "检查资料🙂\n\n检查版本");
});

test("stream joins Unicode deltas, handles replacement and closes on completion", async () => {
  const controller = new AbortController(), received = [];
  const promise = streamRun("one", (r) => received.push(r), controller.signal, Source);
  const source = Source.instances.at(-1);
  source.send(run("项目"));
  source.send(run("🙂\n\n**分析**"), true);
  source.send(run("最终回答"));
  source.send(run("。", "succeeded"), true);
  await promise;
  assert.deepEqual(received.map((r) => r.output), ["项目", "项目🙂\n\n**分析**", "最终回答", "最终回答。"]);
  assert.equal(source.closed, true);
});

test("disconnect discards connection-local state; reconnect starts with a snapshot", async () => {
  const controller = new AbortController(), received = [];
  const first = streamRun("one", (r) => received.push(r.output), controller.signal, Source);
  const source = Source.instances.at(-1);
  source.send(run("项目"));
  source.onerror();
  await assert.rejects(first);
  assert.equal(source.closed, true);
  const second = streamRun("one", (r) => received.push(r.output), controller.signal, Source);
  Source.instances.at(-1).send(run("项目完整回答", "succeeded"));
  await second;
  assert.deepEqual(received, ["项目", "项目完整回答"]);
});

test("abort and view rejection close the source; invalid appends trigger resync", async () => {
  const controller = new AbortController();
  const pending = streamRun("one", () => {}, controller.signal, Source);
  controller.abort();
  await pending;
  assert.equal(Source.instances.at(-1).closed, true);
  await waitForPoll(controller.signal, 10000);
  const signal = new AbortController().signal;
  const rejected = streamRun("one", () => false, signal, Source);
  Source.instances.at(-1).send(run("忽略"));
  await rejected;
  assert.equal(Source.instances.at(-1).closed, true);
  const invalid = streamRun("one", () => {}, signal, Source);
  Source.instances.at(-1).send(run("无起点"), true);
  await assert.rejects(invalid);
});
