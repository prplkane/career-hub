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

const VAULT_FILES = ["profile.yaml", "education.yaml", "visa_profile.yaml", "experience_blocks.yaml", "project_blocks.yaml", "skill_blocks.yaml", "proof_blocks.yaml", "style_rules.yaml", "taxonomy/skill_groups.yaml", "taxonomy/technologies.yaml"];

async function vaultBytes(home) {
  const files = {};
  for (const name of VAULT_FILES) files[name] = await fs.readFile(path.join(home, "career_vault", name));
  return files;
}

test("Career Vault: export from one Home and import the file into a second", { timeout: 120_000 }, async (t) => {
  let exported, sourceFiles;
  await withWorkflow(t, build, browser, async ({ page, step, origin, home }) => {
    await step("create a synthetic profile", async () => {
      await page.goto(origin, { waitUntil: "domcontentloaded" });
      await page.getByLabel("Your name", { exact: true }).fill("Synthetic Export Candidate");
      await page.getByRole("button", { name: "Continue", exact: true }).click();
      await page.waitForFunction(() => !document.querySelector("#vault-setup-apply").disabled);
      await page.getByRole("button", { name: "Create profile", exact: true }).click();
      await page.waitForFunction(() => document.querySelector("#profile-name").value === "Synthetic Export Candidate");
    });
    await step("add a synthetic custom skill group and skill", async () => {
      // The registry is read fresh per request, so on-disk custom catalogs travel.
      await fs.writeFile(path.join(home, "career_vault/taxonomy/skill_groups.yaml"),
        "- id: user_my_custom_skills\n  name: My custom skills\n  archived: false\n- {id: user_synthetic_workflow_group, name: Synthetic workflow group}\n");
      await fs.writeFile(path.join(home, "career_vault/taxonomy/technologies.yaml"),
        "- {id: user_synthetic_workflow_skill, name: Synthetic Workflow Skill, category: tool, primary_group_id: user_synthetic_workflow_group}\n");
    });
    await step("Settings shows the export section with its warning", async () => {
      await page.locator('[popovertarget="interface-settings-panel"]:not([popovertargetaction="hide"])').first().click();
      const section = page.locator("#vault-export-settings");
      await section.waitFor({ state: "visible" });
      assert.match(await section.textContent(), /This file is not encrypted\./);
    });
    await step("Export Career Vault saves one file", async () => {
      const [download] = await Promise.all([
        page.waitForEvent("download"),
        page.getByRole("button", { name: "Export Career Vault", exact: true }).click(),
      ]);
      assert.match(download.suggestedFilename(), /^career-vault-export-\d{4}-\d{2}-\d{2}\.chvault$/);
      const target = path.join(path.dirname(home), "export.chvault");
      await download.saveAs(target);
      exported = await fs.readFile(target);
      await page.locator("#vault-export-status").filter({ hasText: "Export file ready." }).waitFor();
      assert.match(exported.subarray(0, 60).toString("utf8"), /^\{"format":"career-hub-vault-bundle","format_version":1/);
      sourceFiles = await vaultBytes(home);
    });
  });
  for (const language of ["en", "ru"]) {
    const ru = language === "ru";
    await withWorkflow(t, build, browser, async ({ page, step, origin, home }) => {
      await step(`open the portable import in ${language}`, async () => {
        await page.goto(origin, { waitUntil: "domcontentloaded" });
        await page.locator("#setup-locale-preference").selectOption(language);
        await page.getByRole("button", { name: ru ? "Импортировать Career Vault" : "Import a Career Vault", exact: true }).click();
        assert.equal(await page.locator("#vault-setup-name").isVisible(), false);
        assert.equal(await page.locator("#vault-setup-files").isVisible(), false);
        const [chooser] = await Promise.all([
          page.waitForEvent("filechooser"),
          page.getByText(ru ? "Выбрать файл .chvault" : "Choose a .chvault file", { exact: true }).click(),
        ]);
        await chooser.setFiles({ name: "career-vault-export.chvault", mimeType: "application/octet-stream", buffer: exported });
        await page.getByRole("button", { name: ru ? "Проверить импорт" : "Check import", exact: true }).click();
        const summary = ru ? "Будет создано файлов: 8; сохранено идентичных: 0; заменено стандартных файлов навыков: 2."
          : "8 files to create; 0 identical files retained; 2 default skill files replaced.";
        await page.locator("#vault-setup-message").filter({ hasText: summary }).waitFor();
        await page.screenshot({ path: path.join(path.dirname(home), `import-${language}.png`) });
      });
      await step("confirm the import", async () => {
        await page.getByRole("button", { name: ru ? "Подтвердить импорт" : "Confirm import", exact: true }).click();
        await page.waitForFunction(() => document.querySelector("#profile-name").value === "Synthetic Export Candidate");
        await page.locator("#profile-message").filter({ hasText: ru ? "удалите файл экспорта" : "delete the export file" }).waitFor();
      });
      await step("the reloaded skills registry already holds the custom skill", async () => {
        const registry = await page.evaluate(async () => {
          const { state } = await import("/app_context.js");
          return JSON.stringify(state.skillsRegistry || null);
        });
        assert.match(registry, /"Synthetic Workflow Skill"/);
      });
      await step("the profile and all ten files survive reopening", async () => {
        await page.reload();
        await page.waitForFunction(() => document.querySelector("#profile-name").value === "Synthetic Export Candidate");
        assert.equal(await page.locator("html").getAttribute("lang"), language);
        const imported = await vaultBytes(home);
        for (const name of VAULT_FILES) assert.ok(imported[name].equals(sourceFiles[name]), name);
      });
    });
  }
});
