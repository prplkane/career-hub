import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";

const html = await readFile(new URL("../src/intake_ui_static/index.html", import.meta.url), "utf8");
const profile = await readFile(new URL("../src/intake_ui_static/features/profile.js", import.meta.url), "utf8");

test("the derived-name note starts hidden beside the Identity fields", () => {
  assert.match(html, /<p class="profile-policy-note" id="profile-name-parts-note" hidden>[^<]*filled in from Name[^<]*<\/p>/);
  assert.ok(html.indexOf('id="profile-family-name"') < html.indexOf('id="profile-name-parts-note"'));
});

test("the note shows only while the backend marks the parts as derived", () => {
  assert.match(profile, /elements\.profileNamePartsNote\.hidden = record\?\.name_parts_from_name !== true;/);
});
