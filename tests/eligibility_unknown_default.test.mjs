import assert from "node:assert/strict";
import test from "node:test";
import {readFile} from "node:fs/promises";

const html = await readFile(new URL("../src/intake_ui_static/index.html", import.meta.url), "utf8");

// A select with no selected option shows its first enabled one. These two put a
// disabled "Not confirmed" first, so without `selected` they read "Yes" until
// the eligibility facts load, and keep reading "Yes" if that load fails.
test("unconfirmed eligibility facts start as Not confirmed, never Yes", () => {
  for (const id of ["eligibility-sponsorship-future", "eligibility-stem-opt"]) {
    const select = html.match(new RegExp(`<select id="${id}">([\\s\\S]*?)</select>`));
    assert.ok(select, `missing #${id}`);
    const options = [...select[1].matchAll(/<option\b([^>]*)>/g)].map((match) => match[1]);
    assert.match(options[0], /value=""/, `#${id} must lead with the unknown option`);
    assert.match(options[0], /\bselected\b/, `#${id} must start on the unknown option`);
    assert.equal(options.filter((attributes) => /\bselected\b/.test(attributes)).length, 1, `#${id} selects one option`);
  }
});
