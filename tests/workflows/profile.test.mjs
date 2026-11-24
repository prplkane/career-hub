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
    await page.goto(origin, { waitUntil: "domcontentloaded" });
    assert.match(await page.title(), /Career Hub/);
    await page.getByRole("heading", { name: "Welcome to Career Hub", exact: true }).waitFor();
    assert.equal(await page.getByRole("button", { name: "Import a Career Vault", exact: true }).isVisible(), true);
  });
}

async function setupProfile(flow, name = "Synthetic Test Candidate") {
  await openProfile(flow);
  const { page, step } = flow;
  await step("preview synthetic profile", async () => {
    await page.getByLabel("Your name", { exact: true }).fill(name);
    await page.getByRole("button", { name: "Continue", exact: true }).click();
    await page.waitForFunction(() => !document.querySelector("#vault-setup-apply").disabled);
  });
  await step("confirm profile setup", async () => {
    await page.getByRole("button", { name: "Create profile", exact: true }).click();
    await page.waitForFunction((value) => document.querySelector("#profile-name").value === value, name);
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
      assert.equal(await page.getByRole("button", { name: "Continue", exact: true }).isDisabled(), true);
      assert.equal(await page.locator("#vault-setup-apply").isHidden(), true);
      assert.equal(await page.locator("#vault-setup-apply").isDisabled(), true);
    });
    await step("preview does not write a profile", async () => {
      await page.getByLabel("Your name", { exact: true }).fill("Synthetic First Name");
      await page.getByLabel("Your name", { exact: true }).press("Enter");
      await page.waitForFunction(() => !document.querySelector("#vault-setup-apply").disabled);
      await assert.rejects(profileBytes(home), { code: "ENOENT" });
    });
    await step("editing invalidates the previous preview", async () => {
      await page.getByLabel("Your name", { exact: true }).fill("Synthetic Revised Name");
      assert.equal(await page.locator("#vault-setup-apply").isDisabled(), true);
    });
  }));

test("First launch: language, first resume action and returning navigation", { timeout: 45_000 }, (t) =>
  withWorkflow(t, build, browser, async ({ page, step, origin, home }) => {
    await page.addInitScript(() => {
      Object.defineProperty(navigator, "languages", { get: () => ["en-GB", "ru"] });
    });
    await step("an empty workspace opens setup in its primary language", async () => {
      await page.goto(origin);
      await page.getByRole("heading", { name: "Welcome to Career Hub" }).waitFor();
      assert.equal(await page.locator("html").getAttribute("lang"), "en");
      assert.equal(await page.locator("#job-feed-workflow").isVisible(), false);
      await page.screenshot({ path: path.join(path.dirname(home), "welcome-en.png") });
      assert.equal(await page.locator("vite-error-overlay, #webpack-dev-server-client-overlay").count(), 0);
    });
    await step("a Russian selection survives reload on a narrow viewport", async () => {
      await page.setViewportSize({ width: 390, height: 844 });
      await page.locator("#setup-locale-preference").selectOption("ru");
      await page.reload();
      await page.getByRole("heading", { name: "Добро пожаловать в Career Hub" }).waitFor();
      assert.equal(await page.locator("#locale-preference").inputValue(), "ru");
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
      await page.screenshot({ path: path.join(path.dirname(home), "welcome-ru-mobile.png") });
    });
    await step("create a Russian-language profile and reach the first resume action", async () => {
      await page.getByLabel("Ваше имя", { exact: true }).fill("Synthetic Onboarding Candidate");
      await page.getByRole("button", { name: "Продолжить", exact: true }).click();
      await page.locator("#vault-setup-message").filter({ hasText: "Всё готово к созданию профиля" }).waitFor();
      await page.locator("#vault-setup-apply").click();
      const next = page.locator("#vault-setup-next");
      await next.getByRole("link", { name: "Добавить резюме" }).click();
      await page.locator("#view-intake.active").waitFor();
      await page.goto(origin);
      await page.locator("#view-intake.active").waitFor();
      assert.equal(await page.locator("#vault-setup-panel").isVisible(), false);
      assert.match(await profileBytes(home), /Synthetic Onboarding Candidate/);
    });
  }));

test("First launch: an interrupted setup can be retried from an older Jobs preference", { timeout: 45_000 }, (t) =>
  withWorkflow(t, build, browser, async ({ page, step, origin, home }) => {
    await fs.mkdir(path.join(home, "career_vault"), { recursive: true });
    const retained = path.join(home, "career_vault", "experience_blocks.yaml");
    await fs.writeFile(retained, "[]\n");
    await page.addInitScript(() => localStorage.setItem("careerHub.lastView", "job-feed"));
    await page.route("**/api/vault/setup", (route) => route.fulfill({
      status: 503, contentType: "application/json", body: JSON.stringify({ error_code: "SYNTHETIC_UNAVAILABLE" }),
    }), { times: 1 });
    await step("a failed status read offers retry with writes disabled", async () => {
      await page.goto(origin);
      await page.getByRole("button", { name: "Retry", exact: true }).waitFor();
      assert.equal(await page.getByLabel("Your name", { exact: true }).isDisabled(), true);
      await assert.rejects(profileBytes(home), { code: "ENOENT" });
    });
    await step("retry shows the partial Vault and the confirmed plan retains its existing file", async () => {
      await page.getByRole("button", { name: "Retry", exact: true }).click();
      await page.locator("#vault-setup-recovery").waitFor({ state: "visible" });
      await page.getByLabel("Your name", { exact: true }).fill("Synthetic Recovery Candidate");
      await page.getByRole("button", { name: "Continue", exact: true }).click();
      await page.locator("#vault-setup-message").filter({ hasText: "1 identical files retained" }).waitFor();
      await page.getByRole("button", { name: "Create profile", exact: true }).click();
      await page.waitForFunction(() => document.querySelector("#profile-name").value === "Synthetic Recovery Candidate");
      assert.equal(await fs.readFile(retained, "utf8"), "[]\n");
    });
  }, { expectedHTTPFailures: [{ method: "GET", path: "/api/vault/setup", status: 503 }] }));

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

test("Profile: suggested name parts can be corrected and stay confirmed after reload", { timeout: 45_000 }, (t) =>
  withWorkflow(t, build, browser, async (flow) => {
    await setupProfile(flow, "Rowan Example");
    const { page, step } = flow;
    const form = page.locator("#profile-form");
    const given = form.getByLabel("Given name", { exact: true });
    const family = form.getByLabel("Family name", { exact: true });
    const note = page.getByText("Given name and family name were filled in from Name.", { exact: false });
    await step("review the suggested parts and correct the family name", async () => {
      await page.getByRole("button", { name: "Edit profile", exact: true }).click();
      assert.equal(await given.inputValue(), "Rowan");
      assert.equal(await family.inputValue(), "Example");
      await note.waitFor();
      await family.fill("Example Reed");
      assert.equal((await save(page)).status(), 200);
      await page.getByText("Profile saved.", { exact: true }).waitFor();
    });
    await step("reopen the saved profile and read the confirmed correction", async () => {
      await page.reload();
      await page.waitForFunction(() => document.querySelector("#profile-name").value === "Rowan Example");
      await page.getByRole("button", { name: "Edit profile", exact: true }).click();
      assert.equal(await given.inputValue(), "Rowan");
      assert.equal(await family.inputValue(), "Example Reed");
      assert.equal(await note.isVisible(), false);
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
