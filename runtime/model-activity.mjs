// Observe real provider content, including tool arguments and reasoning deltas,
// without forwarding their contents to the host. Headers, empty events and local
// heartbeats do not count as model progress. Tool execution is outside this scope.
export function observeModelActivity(streamFunction, emit) {
  let sequence = 0;
  return async (...args) => {
    const id = `model_${++sequence}`;
    emit({ type: "model_start", id });
    let ended = false;
    const end = () => {
      if (!ended) emit({ type: "model_end", id });
      ended = true;
    };
    let stream;
    try {
      stream = await streamFunction(...args);
    } catch (error) {
      end();
      throw error;
    }
    let iterating = false;
    const iterate = async function* () {
      iterating = true;
      try {
        for await (const event of stream) {
          if (["text_delta", "thinking_delta", "toolcall_delta"].includes(event.type) && event.delta?.length) {
            emit({ type: "model_activity", id });
          }
          if (event.type === "done" || event.type === "error") end();
          yield event;
        }
      } finally {
        end();
      }
    };
    return {
      [Symbol.asyncIterator]: iterate,
      async result() {
        // Pi's compaction consumes result() directly. Drain its stream too so
        // long, actively generated summaries refresh the same idle timer.
        if (!iterating) for await (const event of iterate()) { /* observe only */ }
        return stream.result();
      },
    };
  };
}
