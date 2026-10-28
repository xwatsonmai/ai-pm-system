// The workspace can be mounted under a path such as /workspace behind a proxy.
// Modules are served from that path, so their own URL tells where it is.
const here = new URL(".", import.meta.url);
export const BASE = /^https?:$/.test(here.protocol) ? here.pathname.replace(/\/$/, "") : "";

export const appURL = (path) => BASE + path;

// Saved documents keep attachment links app-relative (/api/...), so the same
// text works wherever the workspace is mounted. Rendering adds the mount path
// and the editor removes it again before saving.
export const displayLinks = (html, base = BASE) =>
  base ? String(html).replace(/(\s(?:src|href)=")\/api\//g, `$1${base}/api/`) : html;

export const savedLinks = (markdown, base = BASE) =>
  base ? String(markdown).replace(new RegExp(`(\\]\\(|\\s(?:src|href)=")${base.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}/api/`, "g"), "$1/api/") : markdown;
