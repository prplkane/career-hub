import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { CareerHubApiClient } from "../src/intake_ui_static/ui/api.js";
import { installDesktopSession } from "./helpers/desktop_session.mjs";

installDesktopSession("synthetic");

for (const [name, capabilities, supported] of [
  ["legacy Python", undefined, true],
  ["Go preview", { email_connection: false }, false],
  ["connected implementation", { email_connection: 1 }, true],
]) {
  test(`email capability: ${name}`, async (t) => {
    const original = globalThis.fetch;
    t.after(() => { globalThis.fetch = original; });
    const paths = [];
    globalThis.fetch = async (url) => {
      paths.push(url);
      assert.equal(url, "/api/session");
      return new Response(JSON.stringify({ token: "synthetic", capabilities }), { status: 200 });
    };
    const client = new CareerHubApiClient();
    assert.equal(await client.hasEmailConnection(), supported);
    assert.equal(await client.hasEmailConnection(), supported);
    assert.deepEqual(paths, ["/api/session"]);
  });
}

test("Profile save row remains outside a non-sticky side rail", async () => {
  const css = await readFile(new URL("../src/intake_ui_static/styles/career-vault.css", import.meta.url), "utf8");
  const firstRail = css.match(/\.profile-side-rail\s*\{([^}]+)\}/)?.[1];
  assert.match(firstRail, /position:\s*static/);
  assert.match(css, /\.profile-layout\s*>\s*\.vault-form-actions\s*\{\s*grid-column:\s*1\s*\/\s*-1/);
});
