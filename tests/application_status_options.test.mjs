import assert from "node:assert/strict";
import test from "node:test";

import { applicationStatusOptions } from "../src/intake_ui_static/features/applications.js";

function options(html) {
  return [...html.matchAll(/<option value="([^"]+)"([^>]*)>/g)].map(([, value, attrs]) => ({
    value,
    selected: attrs.includes("selected"),
    disabled: attrs.includes("disabled"),
  }));
}

test("the inline stage list never offers the legacy Reviewed status", () => {
  const values = options(applicationStatusOptions("applied")).map((option) => option.value);
  assert.deepEqual(values, ["captured", "preparing", "applied", "interview", "offer", "rejected", "withdrawn", "closed"]);
});

test("a legacy Reviewed record shows it once, selected and disabled", () => {
  const reviewed = options(applicationStatusOptions("reviewed")).filter((option) => option.value === "reviewed");
  assert.deepEqual(reviewed, [{ value: "reviewed", selected: true, disabled: true }]);
});
