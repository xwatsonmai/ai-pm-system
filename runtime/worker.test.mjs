import test from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import readline from "node:readline";

test(
  "real Pi SDK: tool call, follow-up, streaming and persisted conversation",
  { timeout: 45000 },
  async () => {
    const directory = await mkdtemp(path.join(tmpdir(), "ai-pm-pi-"));
    const requests = [];
    const server = http.createServer(async (req, res) => {
      let raw = "";
      for await (const chunk of req) raw += chunk;
      const body = JSON.parse(raw);
      requests.push(body);
      res.writeHead(200, { "Content-Type": "text/event-stream" });
      const send = (delta) =>
        res.write(
          "data: " +
            JSON.stringify({
              id: "test",
              object: "chat.completion.chunk",
              created: 1,
              model: "test-model",
              choices: [{ index: 0, delta, finish_reason: null }],
            }) +
            "\n\n",
        );
      send({ role: "assistant" });
      if (!body.messages.some((m) => m.role === "tool")) {
        send({ content: "先读取项目事实。" });
        send({
          tool_calls: [
            {
              index: 0,
              id: "call_context",
              type: "function",
              function: { name: "project_context", arguments: "{}" },
            },
          ],
        });
        res.write(
          "data: " +
            JSON.stringify({
              id: "test",
              choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }],
            }) +
            "\n\n",
        );
      } else {
        send({ content: "已读取项目事实。尚无部署证据。" });
        res.write(
          "data: " +
            JSON.stringify({
              id: "test",
              choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
              usage: {
                prompt_tokens: 20,
                completion_tokens: 10,
                total_tokens: 30,
              },
            }) +
            "\n\n",
        );
      }
      res.end("data: [DONE]\n\n");
    });
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
    try {
      const config = {
        type: "start",
        directory,
        conversation_id: "conv-test",
        profile: {
          provider: "test",
          model: "test-model",
          base_url: `http://127.0.0.1:${server.address().port}/v1`,
          api: "openai-completions",
          supports_images: true,
        },
        api_key: "test-secret",
        system: "Use project_context to read project facts before answering.",
        prompt: "Read project facts.",
        images: [
          {
            type: "image",
            mimeType: "image/png",
            data: "iVBORw0KGgoAAAANSUhEUgAAAIAAAABQCAIAAABeYuqzAAAAqElEQVR4nO3RQQ0AIAzAwJlEBkJxhYw9ekkFNLm552mxWT+IBwBAOwAA2gEA0A4AgHYAALQDAKAdAADtAABoBwBAOwAA2gEA0A4AgHYAALQDAKAdAADtAABoBwBAOwAA2gEA0A4AgHYAALQDAKAdAADtAABoBwBAOwAA2gEA0A4AgHYAALQDAKAdAADtAABoBwBAOwAA2gEA0A4AgHYAALQDAKAdAADtPm/Au5RuuiSnAAAAAElFTkSuQmCC",
          },
        ],
        tools: [
          {
            name: "project_context",
            description: "Read facts",
            schema: { type: "object", properties: {}, required: [] },
          },
        ],
      };
      const run = () =>
        new Promise((resolve, reject) => {
          const child = spawn(process.execPath, ["worker.mjs"], {
            cwd: import.meta.dirname,
            stdio: ["pipe", "pipe", "pipe"],
          });
          let stderr = "";
          const messages = [];
          const timer = setTimeout(() => {
            child.kill();
            reject(new Error("Pi worker timed out " + stderr));
          }, 20000);
          child.stderr.on("data", (chunk) => (stderr += chunk));
          readline
            .createInterface({ input: child.stdout })
            .on("line", (line) => {
              let msg;
              try {
                msg = JSON.parse(line);
              } catch {
                return;
              }
              messages.push(msg);
              if (msg.type === "tool_call")
                child.stdin.write(
                  JSON.stringify({
                    type: "tool_result",
                    id: msg.id,
                    data: { project: "fixture", deployment: "unknown" },
                  }) + "\n",
                );
            });
          child.on("error", reject);
          child.on("exit", (code) => {
            clearTimeout(timer);
            if (code !== 0)
              reject(new Error(stderr || JSON.stringify(messages)));
            else resolve(messages);
          });
          child.stdin.write(JSON.stringify(config) + "\n");
        });
      const first = await run();
      assert.equal(first.filter((m) => m.type === "model_start").length, 2);
      assert.equal(first.filter((m) => m.type === "model_end").length, 2);
      const callIndex = first.findIndex((m) => m.type === "tool_call");
      assert(first.slice(0, callIndex).some((m) => m.type === "model_end"), "model timer ends before tool execution");
      assert(first.slice(callIndex).some((m) => m.type === "model_start"), "next request gets its own first-response timer");
      assert(first.some((m) => m.type === "model_activity"), "real provider deltas report progress");
      assert.equal(first.filter((m) => m.type === "assistant_start").length, 2);
      const startIndexes = first.flatMap((m, i) => m.type === "assistant_start" ? [i] : []);
      assert(first.slice(startIndexes[0], startIndexes[1]).some((m) => m.type === "delta" && m.text.includes("先读取")));
      assert.equal(
        first.some((m) => m.type === "error"),
        false,
        JSON.stringify(first),
      );
      assert.ok(
        first.some(
          (m) => m.type === "tool_call" && m.name === "project_context",
        ),
      );
      assert.ok(
        first.some((m) => m.type === "done" && m.text.includes("尚无部署证据")),
      );
      assert.deepEqual(
        requests[0].tools.map((t) => t.function.name),
        ["project_context"],
      );
      assert.ok(!JSON.stringify(first).includes("test-secret"));
      assert.ok(
        JSON.stringify(requests[0].messages).includes("data:image/png;base64,"),
        "image reaches the model as an image block",
      );
      config.prompt = "继续说明";
      config.images = [];
      const second = await run();
      assert.ok(
        second.some(
          (m) => m.type === "done" && m.text.includes("尚无部署证据"),
        ),
      );
      assert.ok(
        requests.at(-1).messages.filter((m) => m.role === "user").length >= 2,
        "Pi session restored previous turns",
      );
      assert.ok(
        JSON.stringify(requests.at(-1).messages).includes(
          "data:image/png;base64,",
        ),
        "image survives session restore",
      );
    } finally {
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
      await rm(directory, { recursive: true, force: true });
    }
  },
);
