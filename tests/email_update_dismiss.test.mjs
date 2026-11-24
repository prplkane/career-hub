import assert from "node:assert/strict";
import test from "node:test";

import {
  countUnmatchedEmailUpdates,
  renderApplicationEmailUpdateRows,
} from "../src/intake_ui_static/features/applications.js";

const update = (n, resolution) => ({
  outcome_id: `email_outcome_${String(n).padStart(64, "0")}`,
  revision: n,
  resolution,
  source_provider: "linkedin",
  application_status: "rejected",
  title: `Role ${n}`,
  company: `Company ${n}`,
  observed_at: "2026-08-18T18:45:00Z",
});

const items = [
  update(1, "unmatched"),
  update(2, "discovery_dismissed"),
  update(3, "status_conflict"),
  update(4, "application_updated"),
  update(5, "application_already_current"),
];

test("every unresolved update offers Dismiss at its own id and revision", () => {
  const markup = renderApplicationEmailUpdateRows(items);
  for (const n of [1, 2, 3]) {
    assert.match(markup, new RegExp(`data-email-update-dismiss data-outcome-id="email_outcome_0+${n}" data-outcome-revision="${n}"`));
  }
  assert.equal((markup.match(/data-email-update-dismiss/g) || []).length, 3, "linked updates cannot be dismissed");
});

test("linked updates fold under one line, closed unless the user opened it", () => {
  const closed = renderApplicationEmailUpdateRows(items);
  const fold = closed.slice(closed.indexOf("<details class=\"application-email-updates-linked\""));
  assert.match(fold, /^<details class="application-email-updates-linked" data-email-updates-linked>/);
  assert.match(fold, /2 updates already applied to saved applications/);
  assert.match(fold, /Role 4/);
  assert.ok(closed.indexOf("Role 1") < closed.indexOf("application-email-updates-linked"), "open updates come first");
  assert.match(renderApplicationEmailUpdateRows(items, [], { linkedOpen: true }), /data-email-updates-linked open>/);
  assert.doesNotMatch(renderApplicationEmailUpdateRows(items.slice(0, 3)), /application-email-updates-linked/);
});

test("Clear unmatched counts only updates that matched nothing", () => {
  assert.equal(countUnmatchedEmailUpdates(items), 2);
  assert.equal(countUnmatchedEmailUpdates([]), 0);
});
