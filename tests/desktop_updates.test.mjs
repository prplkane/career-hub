import assert from "node:assert/strict";
import test from "node:test";

import { versionLabel } from "../src/intake_ui_static/features/desktop_updates.js";

test("Settings shows the version with the short build commit when it is known", () => {
  assert.equal(versionLabel({ currentVersion: "0.3.0", currentCommit: "abc1234" }), "v0.3.0 (abc1234)");
});

test("a missing or malformed commit shows the version alone", () => {
  for (const currentCommit of [null, undefined, "", "ABC1234", "abc123", "abc1234<b>", 42]) {
    assert.equal(versionLabel({ currentVersion: "0.3.0", currentCommit }), "v0.3.0", String(currentCommit));
  }
});
