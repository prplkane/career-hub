import assert from "node:assert/strict";
import test from "node:test";
import { hasCurrentIntakeApprovalPreview } from "../src/intake_ui_static/features/resumes.js";

test("saved review cannot use the previous approval comparison during refresh", () => {
  const before = { review_revision: `sha256:${"a".repeat(64)}` };
  const after = { review_revision: `sha256:${"b".repeat(64)}` };
  assert.equal(hasCurrentIntakeApprovalPreview(before, before), true);
  assert.equal(hasCurrentIntakeApprovalPreview(before, after), false);
  assert.equal(hasCurrentIntakeApprovalPreview(after, after), true);
});

test("absent or empty revisions never authorize an approval", () => {
  for (const revision of [undefined, null, "", 1]) {
    assert.equal(hasCurrentIntakeApprovalPreview(
      { review_revision: revision }, { review_revision: revision },
    ), false);
  }
  assert.equal(hasCurrentIntakeApprovalPreview(null, null), false);
  assert.equal(hasCurrentIntakeApprovalPreview({}, {}), false);
  assert.equal(hasCurrentIntakeApprovalPreview(null, { review_revision: "saved" }), false);
});
