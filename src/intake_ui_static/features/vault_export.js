// Career Vault export (Settings) and the export-file half of Vault setup.
// The export file is opaque bytes here: the UI never parses it, so a later
// protected container needs no UI change.
import { api } from "../ui/api.js";

export const VAULT_EXPORT_PATH = "/api/vault/export";
export const VAULT_EXPORT_EXTENSION = ".chvault";
// Matches the backend's vaultbundle.MaxBundleBytes.
export const MAX_VAULT_EXPORT_BYTES = 16 * 1024 * 1024;
const FALLBACK_NAME = "career-vault-export.chvault";
const NAME_PATTERN = /^career-vault-export-\d{4}-\d{2}-\d{2}\.chvault$/;

const EXPORT_MESSAGES = Object.freeze({
  VAULT_EXPORT_NOT_CONFIGURED: "Set up your Career Vault before exporting it.",
  VAULT_EXPORT_INVALID: "Your Career Vault did not pass the checks an import runs, so no file was saved. Review your records and skills, then try again.",
  VAULT_EXPORT_SKILL_UNKNOWN: "Some experience, project or certification records link to skills that no longer exist, so no file was saved. Open those records, check their skills, save, then export again.",
  VAULT_EXPORT_TOO_LARGE: "Your Career Vault is too large for one export file. No file was saved.",
  VAULT_EXPORT_CHANGED: "Career Vault files changed during the export. No file was saved. Try again.",
  VAULT_EXPORT_UNAVAILABLE: "Career Hub could not read your Career Vault safely. No file was saved. Try again.",
  VAULT_EXPORT_CANCELED: "The export took too long and was stopped. No file was saved. Try again.",
});

const IMPORT_MESSAGES = Object.freeze({
  VAULT_BUNDLE_PROTECTION_UNSUPPORTED: "This file is protected. Update Career Hub to import it.",
  VAULT_BUNDLE_VERSION_UNSUPPORTED: "This file was made by a newer Career Hub. Update Career Hub to import it.",
  VAULT_BUNDLE_DAMAGED: "This export file is damaged or incomplete. Export it again from the original Career Hub.",
  VAULT_BUNDLE_INVALID: "This is not a Career Vault export file.",
  VAULT_BUNDLE_LIMIT: "This export file is too large to import.",
  VAULT_SETUP_SKILL_UNKNOWN: "Some records link to skills this file does not include. Export again from the original Career Hub.",
  VAULT_SETUP_CATALOG_INVALID: "The custom skills in this file do not fit this Career Hub's skill catalog.",
  VAULT_SETUP_ANSWERS_INVALID: "The saved short answers in this file are not valid.",
  VAULT_SETUP_FILE_CONFLICT: "This Data Home already has different Career Vault files or custom skills. Existing data is never overwritten.",
});

export function vaultExportErrorMessage(error) {
  const code = error?.payload?.error_code || "";
  return EXPORT_MESSAGES[code] || "The export did not finish. No file was saved.";
}

// A specific message for an export-file setup failure, or "" for the caller's
// generic text.
export function vaultImportErrorMessage(code) {
  return IMPORT_MESSAGES[code] || "";
}

export function vaultExportFileName(disposition) {
  const name = String(disposition || "").match(/filename="([^"]+)"/i)?.[1] || "";
  return NAME_PATTERN.test(name) ? name : FALLBACK_NAME;
}

export function vaultBundlePreviewText(plan) {
  const replace = Array.isArray(plan?.replace) ? plan.replace.length : 0;
  return `${plan.create.length} files to create; ${plan.retain.length} identical files retained; ${replace} default skill files replaced. Confirm to continue.`;
}

export function validVaultBundlePlan(plan) {
  return /^sha256:[a-f0-9]{64}$/.test(plan?.plan_id || "")
    && Array.isArray(plan.create) && Array.isArray(plan.retain) && Array.isArray(plan.replace);
}

// Checks only the name and size; the bytes stay opaque and the backend decides.
export function checkVaultBundleFile(file) {
  if (!file) return "Choose a Career Vault export file.";
  if (!String(file.name || "").toLowerCase().endsWith(VAULT_EXPORT_EXTENSION)) return "Choose a file that ends in .chvault.";
  if (!(file.size > 0) || file.size > MAX_VAULT_EXPORT_BYTES) return "This export file is too large to import.";
  return "";
}

export function readVaultBundleFile(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",", 2)[1] || "");
    reader.onerror = () => reject(new Error("The selected file could not be read."));
    reader.onabort = () => reject(new Error("Reading the selected file was canceled."));
    reader.readAsDataURL(file);
  });
}

function saveBlob(blob, filename) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

export async function exportCareerVault({ client = api, save = saveBlob, signal } = {}) {
  const response = await client.raw(VAULT_EXPORT_PATH, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ contract_version: 1 }),
    signal,
  });
  const blob = await response.blob();
  const filename = vaultExportFileName(response.headers.get("content-disposition"));
  save(blob, filename);
  return filename;
}

export async function initVaultExport({ client = api, root = document.querySelector("#vault-export-settings") } = {}) {
  if (!root) return;
  const button = root.querySelector("#vault-export-start");
  const status = root.querySelector("#vault-export-status");
  const saved = root.querySelector("#vault-export-file");
  let busy = false;
  button.addEventListener("click", async () => {
    if (busy) return;
    busy = true;
    button.disabled = true;
    root.setAttribute("aria-busy", "true");
    saved.hidden = true;
    status.textContent = "Preparing the export file…";
    try {
      saved.textContent = await exportCareerVault({ client, signal: AbortSignal.timeout(65_000) });
      saved.hidden = false;
      // The page cannot see whether the Save dialog was confirmed or canceled.
      status.textContent = "Export file ready. If you saved it, keep it private and delete it after importing.";
    } catch (error) {
      status.textContent = vaultExportErrorMessage(error);
    } finally {
      busy = false;
      button.disabled = false;
      root.setAttribute("aria-busy", "false");
    }
  });
  try {
    await client.sessionToken();
  } catch {
    // Without a session the section stays hidden; nothing can be exported.
  }
  root.hidden = client.capabilities?.vault_export !== true;
}
