export const conversationID = (run) => run.conversation_id || run.id;

export function chatConversations(runs) {
  const groups = new Map();
  for (const run of runs) {
    if (run.kind !== "question") continue;
    const id = conversationID(run);
    if (!groups.has(id)) groups.set(id, { id, turns: [] });
    groups.get(id).turns.push(run);
  }
  return [...groups.values()].map((conversation) => {
    const turns = conversation.turns.sort((a, b) => a.started_at - b.started_at || a.id.localeCompare(b.id));
    const first = turns.find((run) => run.title_status) || turns[0];
    const last = turns.at(-1);
    const fallback = first.prompt?.trim().replace(/\s+/g, " ") || first.attachments?.[0]?.name || "附件讨论";
    return { ...conversation, title: first.conversation_title || [...fallback].slice(0, 28).join(""),
      naming: first.title_status === "pending" && ["running", "queued"].includes(first.status),
      legacy: !first.owner_id, updatedAt: last.started_at, latest: last,
      active: turns.some((run) => ["running", "queued"].includes(run.status)) };
  }).sort((a, b) => b.updatedAt - a.updatedAt || a.id.localeCompare(b.id));
}
