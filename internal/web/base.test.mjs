import test from "node:test";
import assert from "node:assert/strict";
import { BASE, appURL, displayLinks, savedLinks } from "./static/base.mjs";
import { markdownHTML } from "./static/markdown.mjs";

test("a mount path changes how attachment links display, never what is saved", () => {
  // Modules read from disk are not mounted anywhere.
  assert.equal(BASE, "");
  assert.equal(appURL("/api/projects"), "/api/projects");
  const html = '<p><img src="/api/projects/p/attachments/a?preview=1" alt="图"> <a href="/api/projects/p/attachments/b">附件</a> <a href="https://example.test/api/x">外链</a> <a href="/other">其他</a></p>';
  const shown = displayLinks(html, "/syai/zhiqu");
  assert.ok(shown.includes('src="/syai/zhiqu/api/projects/p/attachments/a?preview=1"'));
  assert.ok(shown.includes('href="/syai/zhiqu/api/projects/p/attachments/b"'));
  assert.ok(shown.includes('href="https://example.test/api/x"'));
  assert.ok(shown.includes('href="/other"'));
  assert.equal(displayLinks(shown, "/syai/zhiqu"), shown, "display is idempotent");
  assert.equal(displayLinks(html, ""), html);

  const edited = '![图](/syai/zhiqu/api/projects/p/attachments/a?preview=1)\n\n[附件](/syai/zhiqu/api/projects/p/attachments/b) [外链](https://example.test/syai/zhiqu/api/x) <img src="/syai/zhiqu/api/projects/p/attachments/c">';
  assert.equal(savedLinks(edited, "/syai/zhiqu"), '![图](/api/projects/p/attachments/a?preview=1)\n\n[附件](/api/projects/p/attachments/b) [外链](https://example.test/syai/zhiqu/api/x) <img src="/api/projects/p/attachments/c">');
  assert.equal(savedLinks(edited, ""), edited);
  assert.equal(savedLinks("![图](/a.b/api/x)", "/a.b"), "![图](/api/x)");
  assert.equal(savedLinks("![图](/aXb/api/x)", "/a.b"), "![图](/aXb/api/x)", "the mount path is matched literally");

  assert.ok(displayLinks(markdownHTML("![图](/api/projects/p/attachments/a)"), "/m").includes('src="/m/api/projects/p/attachments/a"'));
});
