import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import {
  MAX_VAULT_EXPORT_BYTES,
  checkVaultBundleFile,
  exportCareerVault,
  validVaultBundlePlan,
  vaultBundlePreviewText,
  vaultExportErrorMessage,
  vaultExportFileName,
  vaultImportErrorMessage,
} from "../src/intake_ui_static/features/vault_export.js";

const read = (path) => readFile(new URL(`../src/intake_ui_static/${path}`, import.meta.url), "utf8");
const [html, profile, locale, exportSource] = await Promise.all([
  read("index.html"), read("features/profile.js"), read("locale.js"), read("features/vault_export.js"),
]);

test("export posts the fixed request and saves the opaque bytes under the server's name", async () => {
  const calls = [];
  const blob = new Blob([new Uint8Array([123, 0, 255])]);
  const client = {
    raw: async (path, options) => {
      calls.push({ path, options });
      return {
        blob: async () => blob,
        headers: new Headers({ "content-disposition": 'attachment; filename="career-vault-export-2026-11-18.chvault"' }),
      };
    },
  };
  let saved;
  const name = await exportCareerVault({ client, save: (value, filename) => { saved = { value, filename }; } });
  assert.equal(name, "career-vault-export-2026-11-18.chvault");
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, "/api/vault/export");
  assert.equal(calls[0].options.method, "POST");
  assert.equal(calls[0].options.body, '{"contract_version":1}');
  assert.equal(calls[0].options.headers["Content-Type"], "application/json");
  assert.equal(saved.value, blob, "the downloaded bytes are saved untouched");
  assert.equal(saved.filename, name);
});

test("download names are fixed; anything else falls back to a safe name", () => {
  assert.equal(vaultExportFileName('attachment; filename="career-vault-export-2026-01-02.chvault"'), "career-vault-export-2026-01-02.chvault");
  for (const header of ["", null, 'attachment; filename="../profile.yaml"', 'attachment; filename="career-vault-export-2026-01-02.chvault.exe"']) {
    assert.equal(vaultExportFileName(header), "career-vault-export.chvault");
  }
});

test("each export error code has a plain message, unknown codes a safe fallback", () => {
  const codes = ["VAULT_EXPORT_NOT_CONFIGURED", "VAULT_EXPORT_INVALID", "VAULT_EXPORT_SKILL_UNKNOWN", "VAULT_EXPORT_TOO_LARGE", "VAULT_EXPORT_CHANGED", "VAULT_EXPORT_UNAVAILABLE", "VAULT_EXPORT_CANCELED"];
  const messages = new Set(codes.map((code) => vaultExportErrorMessage({ payload: { error_code: code } })));
  assert.equal(messages.size, codes.length);
  assert.equal(vaultExportErrorMessage(new Error("network")), "The export did not finish. No file was saved.");
  assert.equal(vaultExportErrorMessage({ payload: { error_code: "UNKNOWN" } }), "The export did not finish. No file was saved.");
});

test("export-file import errors map to plain messages", () => {
  assert.equal(vaultImportErrorMessage("VAULT_BUNDLE_PROTECTION_UNSUPPORTED"), "This file is protected. Update Career Hub to import it.");
  for (const code of ["VAULT_BUNDLE_DAMAGED", "VAULT_BUNDLE_INVALID", "VAULT_BUNDLE_VERSION_UNSUPPORTED", "VAULT_BUNDLE_LIMIT", "VAULT_SETUP_SKILL_UNKNOWN", "VAULT_SETUP_CATALOG_INVALID", "VAULT_SETUP_ANSWERS_INVALID", "VAULT_SETUP_FILE_CONFLICT"]) {
    assert.ok(vaultImportErrorMessage(code).length > 20, code);
  }
  assert.equal(vaultImportErrorMessage("VAULT_YAML_INVALID"), "");
});

test("bundle preview reports create, retain and replace counts", () => {
  const plan = { plan_id: `sha256:${"a".repeat(64)}`, create: ["a", "b"], retain: ["c"], replace: ["d", "e"] };
  assert.equal(validVaultBundlePlan(plan), true);
  assert.equal(vaultBundlePreviewText(plan), "2 files to create; 1 identical files retained; 2 default skill files replaced. Confirm to continue.");
  assert.equal(validVaultBundlePlan({ ...plan, replace: undefined }), false);
  assert.equal(validVaultBundlePlan({ ...plan, plan_id: "sha256:short" }), false);
});

test("the picker checks only name and size; the bytes stay opaque", () => {
  assert.equal(checkVaultBundleFile({ name: "career-vault-export-2026-11-18.chvault", size: 10 }), "");
  assert.equal(checkVaultBundleFile({ name: "Copy.CHVAULT", size: 10 }), "");
  assert.match(checkVaultBundleFile({ name: "profile.yaml", size: 10 }), /\.chvault/);
  assert.match(checkVaultBundleFile({ name: "a.chvault", size: MAX_VAULT_EXPORT_BYTES + 1 }), /too large/);
  assert.match(checkVaultBundleFile({ name: "a.chvault", size: 0 }), /too large/);
  assert.match(checkVaultBundleFile(null), /Choose/);
  assert.doesNotMatch(exportSource, /JSON\.parse|\.text\(\)|format_version/, "the UI never parses the export file");
});

test("Settings section sits before Local backup, hidden until the capability says so", () => {
  const section = html.indexOf('id="vault-export-settings"');
  assert.ok(section > 0 && section < html.indexOf('id="desktop-backup-settings"'));
  const markup = html.slice(html.lastIndexOf("<section", section), html.indexOf("</section>", section));
  assert.match(markup, /^<section class="settings-section" id="vault-export-settings" aria-labelledby="vault-export-title" hidden>/);
  assert.match(markup, /This file is not encrypted\. It contains your contact details and work-authorization facts\. Keep it somewhere private and delete it after importing\./);
  assert.match(markup, /profile, education, experience, projects, skills, custom skills and saved short answers/);
  assert.match(markup, /Jobs, applications, documents, and email and extension settings are not included\./);
  assert.match(markup, /class="secondary-button compact-button" id="vault-export-start"[^>]*>Export Career Vault</);
  assert.match(markup, /<code id="vault-export-file" translate="no" hidden>/);
  assert.match(exportSource, /capabilities\?\.vault_export !== true/);
});

test("setup offers a portable archive and a legacy eight-file picker", () => {
  assert.match(html, /<input id="vault-setup-bundle" type="file" accept="\.chvault"/);
  assert.doesNotMatch(html, /custom skill catalogs and credentials are not imported/);
  assert.match(profile, /contract_version: 3, bundle: await readVaultBundleFile\(file\)/);
  assert.match(profile, /if \(bundleInput\.files\.length > 0\) \{\s*input\.value = ""/);
  assert.match(profile, /if \(input\.files\.length > 0\) bundleInput\.value = ""/);
  assert.match(profile, /if \(exportFile\) await loadSkillsRegistry\(/);
  assert.match(profile, /delete the export file/);
});

test("every new visible string has a Russian translation", () => {
  const strings = [
    ...[...exportSource.matchAll(/^\s+VAULT_[A-Z_]+: "([^"]+)",$/gm)].map((match) => match[1]),
    "Career Vault export", "Nothing exported yet.", "Export Career Vault", "Preparing the export file…",
    "Export file ready. If you saved it, keep it private and delete it after importing.", "The export did not finish. No file was saved.",
    "Career Vault export file", "Check the export file before confirming its import.",
    "The export file does not contain a valid Career Vault.", "Choose a Career Vault export file.", "Choose a file that ends in .chvault.",
    "This file is not encrypted. It contains your contact details and work-authorization facts. Keep it somewhere private and delete it after importing.",
    "Career Vault imported with your custom skills. Review your profile, then delete the export file: it is not encrypted.",
  ];
  assert.ok(strings.length > 25);
  for (const text of strings) assert.ok(locale.includes(`    ${JSON.stringify(text)}: "`), text);
  assert.match(locale, /files to create; \(\\d\+\) identical files retained; \(\\d\+\) default skill files replaced/);
});
