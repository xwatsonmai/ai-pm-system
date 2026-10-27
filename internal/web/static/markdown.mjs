import { Marked } from "./vendor/marked.mjs";
import DOMPurify from "./vendor/dompurify.mjs";

const escapeHTML = (text) => String(text).replace(/[&<>"']/g, (char) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]);
const parser = new Marked({
  gfm: true,
  renderer: { html: ({ text }) => escapeHTML(text) },
});

// The parser output is sanitized before insertion into the document.
export function markdownHTML(source) {
  return parser.parse(String(source ?? "").replace(/^[\u200B-\u200F\uFEFF]+/, ""));
}

// Some imported documents contain escaped line breaks outside Markdown code.
// Repair only document-shaped text for display; leave saved content and code intact.
export function documentMarkdown(source) {
  const text = String(source ?? '');
  const parts = text.split(/(```[\s\S]*?(?:```|$)|~~~[\s\S]*?(?:~~~|$)|`[^`\n]*`)/g);
  const prose = parts.filter((_, index) => index % 2 === 0).join('');
  if (!/\\n(?:\\n|[ \t]*#{1,6}\s|[ \t]*[-*+]\s|[ \t]*\d+\.\s)/.test(prose) && !/^\s*#{1,6}\s[^\n]*\\n/.test(prose)) return text;
  return parts.map((part,index) => index % 2 ? part : part.replace(/\\r\\n|\\n/g, '\n')).join('');
}

export const renderDocumentMarkdown = source => renderMarkdown(documentMarkdown(source));

export function renderMarkdown(source) {
  return DOMPurify.sanitize(markdownHTML(source), {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ["form", "input", "button", "select", "textarea", "style"],
    FORBID_ATTR: ["style"],
  });
}
