const renderedHTML = new WeakMap();

function sameKind(current, next) {
  return current.nodeType === next.nodeType && current.nodeName === next.nodeName
    && current.namespaceURI === next.namespaceURI;
}

function updateNode(current, next) {
  let changed = false;
  if (current.nodeType === 3 || current.nodeType === 8) {
    if (current.data !== next.data) {
      current.data = next.data;
      changed = true;
    }
    return changed;
  }
  for (const attribute of [...current.attributes]) {
    if (!next.hasAttribute(attribute.name)) {
      current.removeAttribute(attribute.name);
      changed = true;
    }
  }
  for (const attribute of [...next.attributes]) {
    if (current.getAttribute(attribute.name) !== attribute.value) {
      current.setAttribute(attribute.name, attribute.value);
      changed = true;
    }
  }
  return updateChildren(current, next) || changed;
}

function updateChildren(current, next) {
  let changed = false;
  let child = current.firstChild;
  for (const desired of [...next.childNodes]) {
    if (!child) {
      current.append(desired.cloneNode(true));
      changed = true;
      continue;
    }
    const sibling = child.nextSibling;
    if (sameKind(child, desired)) changed = updateNode(child, desired) || changed;
    else {
      child.replaceWith(desired.cloneNode(true));
      changed = true;
    }
    child = sibling;
  }
  while (child) {
    const sibling = child.nextSibling;
    child.remove();
    child = sibling;
    changed = true;
  }
  return changed;
}

// Callers provide escaped application markup or sanitized Markdown.
export function patchHTML(element, html) {
  if (renderedHTML.get(element) === html) return false;
  const template = element.ownerDocument.createElement("template");
  template.innerHTML = html;
  const changed = updateChildren(element, template.content);
  renderedHTML.set(element, html);
  return changed;
}

// A slower external refresh must not overwrite a newer streaming poll.
export function mergeRunRefresh(incoming, current, atRequestStart) {
  const latest = new Map(current.map((run) => [run.id, run]));
  const received = incoming.map((run) => {
    const local = latest.get(run.id);
    latest.delete(run.id);
    if (local && local !== atRequestStart.get(run.id)) return local;
    // A message boundary moves text into process history and resets the reply.
    // Length alone cannot order snapshots across that boundary.
    if (local && ["running", "queued"].includes(run.status)
      && (local.output_version || 0) > (run.output_version || 0)) {
      return { ...run, output: local.output, process_output: local.process_output, output_version: local.output_version };
    }
    // SQLite checkpoints can lag live text even if no new token arrived while
    // this request was in flight (for example while a tool is running).
    if (local && !local.output_version && !run.output_version && ["running", "queued"].includes(run.status)
      && local.status === run.status && local.output?.length > (run.output || "").length
      && local.output.startsWith(run.output || "")) return { ...run, output: local.output };
    return run;
  });
  for (const run of latest.values()) {
    if (!atRequestStart.has(run.id)) received.push(run);
  }
  return received;
}
