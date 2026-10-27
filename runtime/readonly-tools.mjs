import path from "node:path";
import { realpath } from "node:fs/promises";
import {
  createReadToolDefinition, createGrepToolDefinition,
  createFindToolDefinition, createLsToolDefinition,
} from "@earendil-works/pi-coding-agent";

const inside = (root, target) => target === root || target.startsWith(root + path.sep);

// Keep Pi's schemas, search/read algorithms and truncation metadata. This adapter
// supplies only the project boundary, evidence envelope, budget and audit events.
export async function readonlyTools(workspace, snapshot, beforeCall, emit) {
  const root = await realpath(workspace);
  return [createReadToolDefinition, createGrepToolDefinition, createFindToolDefinition, createLsToolDefinition].map((factory) => {
    const native = factory(root);
    return {
      ...native,
      executionMode: "sequential",
      async execute(id, args, signal, onUpdate, ctx) {
        beforeCall(signal);
        emit({ type: "native_tool_start", id, name: native.name, args });
        try {
          const requested = path.resolve(root, args.path || ".");
          if (!inside(root, requested)) throw new Error("PATH_OUTSIDE_WORKSPACE：只能阅读当前项目源码快照");
          let canonical;
          try { canonical = await realpath(requested); }
          catch { throw new Error(`PATH_NOT_FOUND：当前提交中不存在路径 ${args.path || "."}；请用 ls 或 find 核对路径`); }
          if (!inside(root, canonical)) throw new Error("SYMLINK_OUTSIDE_WORKSPACE：符号链接目标超出当前项目");
          const result = await native.execute(id, { ...args, path: canonical }, signal, onUpdate, { ...ctx, cwd: root });
          const evidence = {
            source: "git_snapshot", sha: snapshot.sha || "", path: path.relative(root, canonical) || ".",
            ...(native.name === "read" ? { offset: args.offset || 1, ...(args.limit ? { limit: args.limit } : {}) } : {}),
          };
          emit({ type: "native_tool_end", id, name: native.name, details: { ...result.details, ...evidence } });
          return { ...result, content: [{ type: "text", text: JSON.stringify(evidence) }, ...result.content] };
        } catch (error) {
          emit({ type: "native_tool_end", id, name: native.name, error: String(error.message).slice(0, 1000) });
          throw error;
        }
      },
    };
  });
}
