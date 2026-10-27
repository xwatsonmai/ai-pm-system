import test from "node:test";
import assert from "node:assert/strict";
import { webcrypto } from "node:crypto";
import { createID } from "./static/id.mjs";

const uuidV4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

test("uses the browser's native UUID API when available", () => {
  const crypto = {
    randomUUID() {
      assert.equal(this, crypto);
      return "native-uuid";
    },
    getRandomValues() {
      assert.fail("native UUID should not use fallback");
    },
  };
  assert.equal(createID(crypto), "native-uuid");
  assert.match(createID(webcrypto), uuidV4);
});

test("HTTP browser without randomUUID generates distinct v4 IDs", () => {
  const httpCrypto = {
    getRandomValues: webcrypto.getRandomValues.bind(webcrypto),
  };
  const ids = Array.from({ length: 1000 }, () => createID(httpCrypto));
  for (const id of ids) assert.match(id, uuidV4);
  assert.equal(new Set(ids).size, ids.length);
});

test("HTTP UUID preserves leading zeros and sets version and variant bits", () => {
  for (const value of [0, 255]) {
    const id = createID({ getRandomValues: (bytes) => bytes.fill(value) });
    assert.match(id, uuidV4);
    assert.ok(id.startsWith(value === 0 ? "00000000-" : "ffffffff-"));
  }
});
