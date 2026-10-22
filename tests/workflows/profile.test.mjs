import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { before, after, test } from "node:test";
import { chromium } from "playwright";
import { buildFixture, withWorkflow } from "./fixture.mjs";

let build, browser;
before(async () => {
  build = await buildFixture();
  browser = await chromium.launch({ headless: true });
}, { timeout: 130_000 });
after(async () => { await browser?.close(); });

async function openProfile({ page, step, origin }) {
  await step("open Profile", async () => {
    await page.goto(origin + "/#profile", { waitUntil: "domcontentloaded" });
    assert.match(await page.title(), /Career Hub/);
    await page.getByRole("heading", { name: "Set up Career Vault", exact: true }).waitFor();
  });
}

async function setupProfile(flow) {
  await openProfile(flow);
  const { page, step } = flow;
  await step("preview synthetic profile", async () => {
    await page.getByLabel("Your name", { exact: true }).fill("Synthetic Test Candidate");
    await page.getByRole("button", { name: "Check new profile", exact: true }).click();
    await page.waitForFunction(() => !document.querySelector("#vault-setup-apply").disabled);
  });
  await step("confirm profile setup", async () => {
    await page.getByRole("button", { name: "Create profile", exact: true }).click();
    await page.waitForFunction(() => document.querySelector("#profile-name").value === "Synthetic Test Candidate");
  });
}

async function profileBytes(home) {
  return fs.readFile(path.join(home, "career_vault/profile.yaml"), "utf8");
}

async function save(page) {
  const [response] = await Promise.all([
    page.waitForResponse((item) => new URL(item.url()).pathname === "/api/vault/profile" && item.request().method() === "PUT"),
    page.getByRole("button", { name: "Save profile", exact: true }).click(),
  ]);
  return response;
}

test("Profile: setup preview requires a current explicit confirmation", { timeout: 45_000 }, (t) =>
  withWorkflow(t, build, browser, async (flow) => {
    await openProfile(flow);
    const { page, step, home } = flow;
    await step("empty name cannot start setup", async () => {
      assert.equal(await page.getByRole("button", { name: "Check new profile", exact: true }).isDisabled(), true);
      assert.equal(await page.getByRole("button", { name: "Confirm setup", exact: true }).isDisabled(), true);
    });
    await step("preview does not write a profile", async () => {
      await page.getByLabel("Your name", { exact: true }).fill("Synthetic First Name");
      await page.getByRole("button", { name: "Check new profile", exact: true }).click();
      await page.waitForFunction(() => !document.querySelector("#vault-setup-apply").disabled);
      await assert.rejects(profileBytes(home), { code: "ENOENT" });
    });
    await step("editing invalidates the previous preview", async () => {
      await page.getByLabel("Your name", { exact: true }).fill("Synthetic Revised Name");
      assert.equal(await page.getByRole("button", { name: "Create profile", exact: true }).isDisabled(), true);
    });
  }));

test("Profile: save survives reload and cancel discards later edits", { timeout: 45_000 }, (t) =>
  withWorkflow(t, build, browser, async (flow) => {
    await setupProfile(flow);
    const { page, step, home } = flow;
    await step("save edited profile through the native API", async () => {
      await page.getByRole("button", { name: "Edit profile", exact: true }).click();
      await page.locator("#profile-form").getByLabel("Name", { exact: true }).fill("Synthetic Saved Candidate");
      assert.equal((await save(page)).status(), 200);
      await page.locator("#profile-message").filter({ hasText: "Profile saved." }).waitFor();
      assert.match(await profileBytes(home), /Synthetic Saved Candidate/);
    });
    await step("reload reads the persisted value", async () => {
      await page.reload();
      await page.waitForFunction(() => document.querySelector("#profile-name").value === "Synthetic Saved Candidate");
    });
    await step("cancel restores saved data without changing the file", async () => {
      const before = await profileBytes(home);
      await page.getByRole("button", { name: "Edit profile", exact: true }).click();
      await page.locator("#profile-form").getByLabel("Name", { exact: true }).fill("Unsaved Synthetic Name");
      const discard = page.waitForEvent("dialog").then(async (dialog) => {
        assert.equal(dialog.type(), "confirm");
        assert.equal(dialog.message(), "Discard unsaved profile changes?");
        await dialog.accept();
      });
      await Promise.all([discard, page.locator("#profile-form").getByRole("button", { name: "Cancel", exact: true }).click()]);
      await page.waitForFunction(() => document.querySelector("#profile-name").value === "Synthetic Saved Candidate");
      assert.equal(await profileBytes(home), before);
    });
  }));

test("Profile: empty required name is rejected without a write", { timeout: 45_000 }, (t) =>
  withWorkflow(t, build, browser, async (flow) => {
    await setupProfile(flow);
    const { page, step, home } = flow;
    await step("browser validation blocks an empty name", async () => {
      const before = await profileBytes(home);
      const writes = [];
      page.on("request", (request) => {
        if (new URL(request.url()).pathname === "/api/vault/profile" && request.method() === "PUT") writes.push(request);
      });
      await page.getByRole("button", { name: "Edit profile", exact: true }).click();
      const name = page.locator("#profile-form").getByLabel("Name", { exact: true });
      await name.fill("");
      await page.getByRole("button", { name: "Save profile", exact: true }).click();
      assert.equal(await name.evaluate((element) => element.validity.valueMissing), true);
      assert.equal(await name.evaluate((element) => element === document.activeElement), true);
      assert.equal(writes.length, 0);
      assert.equal(await profileBytes(home), before);
    });
  }));

test("Profile: failed save keeps input and permits explicit retry", { timeout: 45_000 }, (t) =>
  withWorkflow(t, build, browser, async (flow) => {
    await setupProfile(flow);
    const { page, step, home } = flow;
    const route = "**/api/vault/profile";
    let failedWrites = 0;
    await page.route(route, (request) => {
      if (request.request().method() !== "PUT") return request.continue();
      failedWrites++;
      return request.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({
        error: "Synthetic save failure", error_code: "SYNTHETIC_SAVE_FAILED", suggestion: "Retry saving.",
      }) });
    });
    await step("failed write displays its reason and preserves unsaved input", async () => {
      const before = await profileBytes(home);
      await page.getByRole("button", { name: "Edit profile", exact: true }).click();
      await page.locator("#profile-form").getByLabel("Name", { exact: true }).fill("Synthetic Retry Candidate");
      assert.equal((await save(page)).status(), 500);
      await page.locator("#profile-message").filter({ hasText: "SYNTHETIC_SAVE_FAILED" }).waitFor();
      assert.equal(await page.locator("#profile-name").inputValue(), "Synthetic Retry Candidate");
      assert.equal(await page.getByRole("button", { name: "Save profile", exact: true }).isEnabled(), true);
      assert.equal(await profileBytes(home), before);
      assert.equal(failedWrites, 1, "failed writes must not be retried automatically");
    });
    await step("explicit retry reaches the real backend and persists", async () => {
      await page.unroute(route);
      assert.equal((await save(page)).status(), 200);
      await page.locator("#profile-message").filter({ hasText: "Profile saved." }).waitFor();
      assert.match(await profileBytes(home), /Synthetic Retry Candidate/);
      await page.reload();
      await page.waitForFunction(() => document.querySelector("#profile-name").value === "Synthetic Retry Candidate");
    });
  }, { expectedHTTPFailures: [{ method: "PUT", path: "/api/vault/profile", status: 500 }] }));
