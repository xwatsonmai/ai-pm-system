const active = (run) => ["running", "queued"].includes(run.status);

// Each connection starts with a full snapshot. Append events are relative to
// this connection, never to a possibly older background/history response.
export function streamRun(runID, onRun, signal, Source = globalThis.EventSource) {
  return new Promise((resolve, reject) => {
    if (signal.aborted) { resolve(); return; }
    const source = new Source(`/api/runs/${encodeURIComponent(runID)}/events`);
    let previous, timer, finished = false;
    const finish = (error) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      source.close();
      signal.removeEventListener("abort", abort);
      if (error) reject(error);
      else resolve();
    };
    const abort = () => finish();
    const touch = () => {
      clearTimeout(timer);
      timer = setTimeout(() => finish(new Error("实时连接暂未响应")), 25000);
    };
    signal.addEventListener("abort", abort, { once: true });
    source.addEventListener("ping", touch);
    source.addEventListener("run", (event) => {
      if (finished) return;
      try {
        const update = JSON.parse(event.data);
        if (update.run?.id !== runID || ((update.append || update.process_append) && !previous)) throw new Error("实时回答需要重新同步");
        const run = { ...update.run,
          output: (update.append ? previous.output : "") + (update.run.output || ""),
          process_output: (update.process_append ? previous.process_output || "" : "") + (update.run.process_output || ""),
        };
        previous = run;
        touch();
        if (onRun(run) === false || !active(run)) finish();
      } catch (error) { finish(error); }
    });
    source.onerror = () => finish(new Error("实时连接已断开"));
    touch();
  });
}

export function waitForPoll(signal, delay = 900) {
  return new Promise((resolve) => {
    if (signal.aborted) { resolve(); return; }
    const done = () => {
      clearTimeout(timer);
      signal.removeEventListener("abort", done);
      resolve();
    };
    const timer = setTimeout(done, delay);
    signal.addEventListener("abort", done, { once: true });
  });
}
