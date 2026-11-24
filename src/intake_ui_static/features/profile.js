// Profile view: Career Vault setup, personal details, eligibility facts and hard-blocker policy.
import { elements, requestJson, state } from "../app_context.js";
import { api } from "../ui/api.js";
import { escapeHtml, formatRequestError, renderLoadingState } from "../ui/primitives.js";
import { pollLogs } from "./diagnostics.js";
import { getVaultSnapshot, vaultLines } from "./vault_shared.js";
import { loadSkillsRegistry } from "./skills_hub.js";
import {
  checkVaultBundleFile, readVaultBundleFile, validVaultBundlePlan, vaultBundlePreviewText, vaultImportErrorMessage,
} from "./vault_export.js";

// Bootstrap hooks supplied by app.js through initProfile().
let ensureViewData = null;

function setProfileWorkAuthorization(value) {
  const authorization = String(value || "").trim();
  const knownOption = [...elements.profileWorkAuthorization.options]
    .some((option) => option.value === authorization && authorization !== "Other");
  elements.profileWorkAuthorization.value = knownOption ? authorization : (authorization ? "Other" : "");
  elements.profileWorkAuthorizationOther.value = knownOption ? "" : authorization;
  syncProfileWorkAuthorizationOtherField();
}

function syncProfileWorkAuthorizationOtherField() {
  const isOther = elements.profileWorkAuthorization.value === "Other";
  elements.profileWorkAuthorizationOtherField.hidden = !isOther;
  elements.profileWorkAuthorizationOther.disabled = !isOther || !state.profileEditing;
  elements.profileWorkAuthorizationOther.required = isOther;
}

function profileWorkAuthorizationValue() {
  return elements.profileWorkAuthorization.value === "Other"
    ? elements.profileWorkAuthorizationOther.value.trim()
    : elements.profileWorkAuthorization.value;
}

function renderProfileRoleTargets(record = state.profileRecord) {
  const roles = record?.role_targets || vaultLines(elements.profileRoleTargets.value, true);
  elements.profileRoleTargetTags.innerHTML = roles.length
    ? roles.map((role) => `<span class="tag" translate="no">${escapeHtml(role)}</span>`).join("")
    : '<span class="muted-text">No target roles recorded.</span>';
}

// A disabled input cannot wrap, so a long read-only value is clipped; the full
// text stays reachable as the field's title.
function syncReadonlyValueTitles() {
  elements.profileForm.querySelectorAll("input, textarea").forEach((control) => {
    if (control.type === "checkbox" || control.type === "radio") return;
    if (control.disabled && control.value) control.title = control.value;
    else control.removeAttribute("title");
  });
}

function setProfileEditing(editing) {
  state.profileEditing = Boolean(editing);
  elements.profileEditorShell.classList.toggle("is-readonly", !state.profileEditing);
  elements.editProfileButton.disabled = state.profileEditing;
  elements.editProfileButton.setAttribute("aria-pressed", String(state.profileEditing));
  elements.editProfileButton.textContent = state.profileEditing ? "Editing" : "Edit profile";
  elements.profileForm.querySelectorAll("input, select, textarea").forEach((control) => {
    if (control.closest(".eligibility-profile-section, .profile-policy-card")) return;
    control.disabled = !state.profileEditing;
  });
  elements.cancelProfileButton.hidden = !state.profileEditing;
  elements.saveProfileButton.hidden = !state.profileEditing;
  syncProfileWorkAuthorizationOtherField();
  renderEligibilityProfile();
  renderHardBlockerPolicy();
  renderProfileRoleTargets();
}

function populateProfileForm(record) {
  const contact = record?.contact || {};
  const allowsUnknownFacts = record?.vault_schema_version === 2;
  [elements.profileMajor, elements.profileTargetLocation, elements.profileWorkAuthorization]
    .forEach((control) => { control.required = !allowsUnknownFacts; });
  elements.profileEditorTitle.translate = !record?.name;
  elements.profileEditorTitle.textContent = record?.name || "Candidate profile";
  elements.profileName.value = record?.name || "";
  elements.profileGivenName.value = record?.given_name || "";
  elements.profileFamilyName.value = record?.family_name || "";
  elements.profilePreferredName.value = record?.preferred_name || "";
  // Import or setup read these parts from Name; any Profile save clears the mark.
  elements.profileNamePartsNote.hidden = record?.name_parts_from_name !== true;
  elements.profileMajor.value = record?.major || "";
  elements.profileCurrentLocation.value = record?.current_location || "";
  elements.profileTargetLocation.value = record?.target_location || "";
  elements.profileLocationFlexibility.value = record?.location_flexibility || "";
  setProfileWorkAuthorization(record?.work_authorization);
  elements.profileEmail.value = contact.email || "";
  elements.profilePhone.value = contact.phone || "";
  elements.profileAddress.value = contact.address || "";
  elements.profileAddressLine1.value = contact.address_line1 || "";
  elements.profileAddressLine2.value = contact.address_line2 || "";
  elements.profileCity.value = contact.city || "";
  elements.profileRegion.value = contact.region || "";
  elements.profilePostalCode.value = contact.postal_code || "";
  elements.profileCountry.value = contact.country || "";
  elements.profileLinkedin.value = contact.linkedin || "";
  elements.profileGithub.value = contact.github || "";
  elements.profileWebsite.value = contact.website || "";
  elements.profileRoleTargets.value = (record?.role_targets || []).join("\n");
  state.profileFormDirty = false;
  renderProfileRoleTargets(record);
}

const vaultSetupImport = { initialized: false, ready: false, busy: false, mode: "create", request: null, plan: null };
const vaultSetupNames = ["profile.yaml", "education.yaml", "visa_profile.yaml", "experience_blocks.yaml", "project_blocks.yaml", "skill_blocks.yaml", "proof_blocks.yaml", "style_rules.yaml"];

export async function ensureVaultSetup() {
  if (api.capabilities.vault_setup_import !== true) return true;
  const panel = document.querySelector("#vault-setup-panel");
  const input = document.querySelector("#vault-setup-files");
  const preview = document.querySelector("#vault-setup-preview");
  const apply = document.querySelector("#vault-setup-apply");
  const message = document.querySelector("#vault-setup-message");
  const name = document.querySelector("#vault-setup-name");
  const create = document.querySelector("#vault-setup-create");
  const bundleInput = document.querySelector("#vault-setup-bundle");
  const bundleEnabled = api.capabilities.vault_export === true;
  const createEnabled = api.capabilities.vault_setup_empty === true;
  const chooseCreate = document.querySelector("#vault-choose-create");
  const chooseImport = document.querySelector("#vault-choose-import");
  const retry = document.querySelector("#vault-setup-retry");
  const profileHeader = document.querySelector("#profile-header");
  const legacy = document.querySelector("#vault-setup-legacy");
  if (!createEnabled) vaultSetupImport.mode = "import";
  chooseCreate.hidden = !createEnabled;
  document.querySelector("#vault-setup-bundle-field").hidden = !bundleEnabled;
  document.querySelector("#vault-setup-bundle-help").hidden = !bundleEnabled;
  if (!bundleEnabled) legacy.open = true;
  const controls = () => {
    const creating = vaultSetupImport.mode === "create";
    const locked = vaultSetupImport.busy || !vaultSetupImport.ready;
    chooseCreate.disabled = locked;
    chooseImport.disabled = locked;
    chooseCreate.setAttribute("aria-pressed", String(creating));
    chooseImport.setAttribute("aria-pressed", String(!creating));
    document.querySelector("#vault-setup-new").hidden = !creating;
    document.querySelector("#vault-setup-import").hidden = creating;
    input.disabled = locked;
    bundleInput.disabled = locked || !bundleEnabled;
    document.querySelector("#vault-setup-filename").textContent = bundleInput.files[0]?.name || "";
    document.querySelector("#vault-setup-filenames").textContent = Array.from(input.files, (file) => file.name).join(", ");
    name.disabled = locked;
    create.hidden = !creating || Boolean(vaultSetupImport.plan);
    create.disabled = locked || !name.value.trim() || !createEnabled;
    preview.hidden = creating || Boolean(vaultSetupImport.plan);
    preview.disabled = locked || (input.files.length === 0 && bundleInput.files.length === 0);
    apply.hidden = !vaultSetupImport.plan;
    apply.disabled = locked || !vaultSetupImport.plan;
    panel.setAttribute("aria-busy", String(vaultSetupImport.busy));
  };
  if (!vaultSetupImport.initialized) {
    vaultSetupImport.initialized = true;
    const choose = (mode) => {
      if (mode !== vaultSetupImport.mode) {
        vaultSetupImport.mode = mode;
        vaultSetupImport.request = null;
        vaultSetupImport.plan = null;
        message.textContent = "";
        controls();
      }
      (mode === "create" ? name : bundleEnabled ? bundleInput : input).focus();
    };
    chooseCreate.addEventListener("click", () => choose("create"));
    chooseImport.addEventListener("click", () => choose("import"));
    retry.addEventListener("click", () => { void ensureViewData("profile", { force: true }); });
    input.addEventListener("change", () => {
      vaultSetupImport.request = null; vaultSetupImport.plan = null;
      if (input.files.length > 0) bundleInput.value = "";
      message.textContent = "Check all eight files before confirming their import.";
      controls();
    });
    // One source at a time: choosing an export file clears the eight-file pick.
    bundleInput.addEventListener("change", () => {
      vaultSetupImport.request = null; vaultSetupImport.plan = null;
      if (bundleInput.files.length > 0) {
        input.value = "";
        legacy.open = false;
      }
      message.textContent = "Check the export file before confirming its import.";
      controls();
    });
    name.addEventListener("input", () => {
      vaultSetupImport.request = null; vaultSetupImport.plan = null;
      message.textContent = "";
      controls();
    });
    const operation = async (isApply, newProfile = false) => {
      if (!vaultSetupImport.ready || vaultSetupImport.busy || isApply && !vaultSetupImport.plan) return;
      vaultSetupImport.busy = true; controls();
      message.innerHTML = renderLoadingState({ label: isApply ? "Setting up Career Vault…" : "Checking setup…", compact: true });
      try {
        if (!isApply) {
          vaultSetupImport.plan = null; vaultSetupImport.request = null;
          let request;
          if (newProfile) {
            if (api.capabilities.vault_setup_empty !== true || !name.value.trim()) throw new Error("Enter your name to create a profile.");
            request = { contract_version: 2, profile_name: name.value };
          } else if (bundleEnabled && bundleInput.files.length > 0) {
            const [file] = bundleInput.files;
            const problem = checkVaultBundleFile(file);
            if (problem) throw new Error(problem);
            request = { contract_version: 3, bundle: await readVaultBundleFile(file) };
          } else {
            const selected = Array.from(input.files);
            if (selected.length !== 8 || new Set(selected.map((file) => file.name)).size !== 8 || selected.some((file) => !vaultSetupNames.includes(file.name) || file.size > 2 * 1024 * 1024)) {
              throw new Error("Select each of the eight listed YAML files once, at most 2 MB per file.");
            }
            const files = {};
            for (const file of selected) {
              files[file.name] = await new Promise((resolve, reject) => {
                const reader = new FileReader();
                reader.onload = () => resolve(String(reader.result).split(",", 2)[1]);
                reader.onerror = () => reject(new Error("A selected file could not be read."));
                reader.onabort = () => reject(new Error("Reading the selected file was canceled."));
                reader.readAsDataURL(file);
              });
            }
            request = { contract_version: 1, files };
          }
          const plan = await api.json("/api/vault/setup/preview", "POST", request, { signal: AbortSignal.timeout(65000) });
          const exportFile = request.contract_version === 3;
          if (!/^sha256:[a-f0-9]{64}$/.test(plan?.plan_id) || !Array.isArray(plan.create) || !Array.isArray(plan.retain) || exportFile && !validVaultBundlePlan(plan)) throw new Error("The import preview could not be verified.");
          vaultSetupImport.request = request; vaultSetupImport.plan = plan;
          apply.textContent = newProfile ? "Create profile" : "Confirm import";
          message.textContent = exportFile
            ? vaultBundlePreviewText(plan)
            : newProfile && !plan.retain.length
              ? "Your profile is ready to create. You can edit your name and add details later."
              : `${plan.create.length} files to create; ${plan.retain.length} identical files retained. No existing file will be replaced. Confirm to continue.`;
        } else {
          const createdNew = vaultSetupImport.request.contract_version === 2;
          const exportFile = vaultSetupImport.request.contract_version === 3;
          await api.json("/api/vault/setup/import", "POST", { ...vaultSetupImport.request, confirmed_plan: vaultSetupImport.plan.plan_id }, { signal: AbortSignal.timeout(65000) });
          vaultSetupImport.request = null; vaultSetupImport.plan = null; input.value = ""; bundleInput.value = "";
          await ensureViewData("profile", { force: true });
          // Custom skills arrived with the file; the cached registry is stale.
          if (exportFile) await loadSkillsRegistry({ keepMessage: true });
          document.querySelector("#vault-setup-next").hidden = !createdNew;
          elements.profileMessage.textContent = createdNew ? "Profile created. Add your details when you are ready; unknown facts stay unconfirmed."
            : exportFile ? "Career Vault imported with your custom skills. Review your profile, then delete the export file: it is not encrypted."
              : "Career Vault imported. Review your profile before using it.";
        }
      } catch (error) {
        const exportFile = vaultSetupImport.request?.contract_version === 3 || !newProfile && bundleEnabled && bundleInput.files.length > 0;
        vaultSetupImport.plan = null; vaultSetupImport.request = null;
        const code = error?.payload?.error_code || "";
        const hint = (exportFile && vaultImportErrorMessage(code)) || (code === "VAULT_SETUP_FILE_CONFLICT"
          ? "This setup differs from data already in this Home. Use the original profile name or matching import files; existing files cannot be overwritten."
          : error?.status === 422
            ? exportFile
              ? "The export file does not contain a valid Career Vault."
              : "The selected files do not form a valid Career Vault. Check their YAML fields and cross-file references."
            : code || error.message);
        if (isApply) {
          message.textContent = `Setup was not confirmed complete. Some files may already be saved; check the same name or files again before retrying. ${code}`;
        } else {
          // The hint is its own text node so a translated message matches whole.
          const detail = document.createElement("span");
          detail.textContent = hint;
          message.replaceChildren("Setup check failed. Existing files were not replaced. ", detail);
        }
      } finally {
        vaultSetupImport.busy = false;
        controls();
        if (!isApply && vaultSetupImport.plan && state.activeView === "profile") apply.focus();
      }
    };
    preview.addEventListener("click", () => operation(false));
    document.querySelector("#vault-setup-new").addEventListener("submit", (event) => {
      event.preventDefault();
      if (!create.disabled && !create.hidden) void operation(false, true);
    });
    apply.addEventListener("click", () => operation(true));
  }
  // Never flash an editable empty profile while its storage state is unknown.
  vaultSetupImport.ready = false;
  retry.hidden = true;
  controls();
  profileHeader.hidden = true;
  elements.profileEditorShell.hidden = true;
  elements.editProfileButton.hidden = true;
  try {
    const setup = await api.request("/api/vault/setup", { signal: AbortSignal.timeout(15000) });
    if (setup.state === "configured") {
      profileHeader.hidden = false;
      panel.hidden = true; elements.profileEditorShell.hidden = false; elements.editProfileButton.hidden = false;
      return true;
    }
    if (!["not_configured", "incomplete"].includes(setup.state)) throw new Error("Unexpected Vault setup state.");
    vaultSetupImport.ready = true;
    document.querySelector("#vault-setup-recovery").hidden = setup.state !== "incomplete";
    if (!vaultSetupImport.plan && !vaultSetupImport.busy) message.textContent = "";
  } catch (error) {
    retry.hidden = false;
    message.textContent = `Existing Career Vault files could not be validated. Nothing was reset. ${error?.payload?.error_code || "Check the local connection and try again."}`;
  }
  panel.hidden = false;
  controls();
  return false;
}

export async function loadVaultProfile({ keepMessage = false, refresh = false } = {}) {
  const payload = (await getVaultSnapshot({ refresh })).profile;
  state.profileRecord = payload.record || {};
  state.profileRevision = payload.revision;
  populateProfileForm(state.profileRecord);
  setProfileEditing(false);
  if (!keepMessage) elements.profileMessage.textContent = "";
}

function optionalBooleanControlValue(control) {
  if (control.value === "") return null;
  return control.value === "true";
}

function setOptionalBooleanControl(control, value) {
  control.value = typeof value === "boolean" ? String(value) : "";
}

function populateEligibilityProfile(snapshot) {
  const record = snapshot?.record || {};
  const allowsUnknownFacts = snapshot?.vault_schema_version === 2;
  [elements.eligibilitySponsorshipFuture, elements.eligibilityStemOpt].forEach((control) => {
    control.querySelector('option[value=""]').disabled = !allowsUnknownFacts;
  });
  elements.eligibilityCurrentStatus.value = record.current_status || "";
  elements.eligibilityWorkAuthorization.value = record.work_authorization || "";
  setOptionalBooleanControl(
    elements.eligibilitySponsorshipNow,
    record.requires_sponsorship_now,
  );
  setOptionalBooleanControl(
    elements.eligibilitySponsorshipFuture,
    record.requires_future_sponsorship,
  );
  setOptionalBooleanControl(
    elements.eligibilityCitizenshipConflict,
    record.citizenship_required_blocker,
  );
  setOptionalBooleanControl(
    elements.eligibilityClearanceConflict,
    record.clearance_required_blocker,
  );
  setOptionalBooleanControl(elements.eligibilityStemOpt, allowsUnknownFacts ? record.stem_opt_eligible : record.stem_opt_eligible === true);
  state.eligibilityProfileDirty = false;
}

function renderEligibilityProfile() {
  const busy = state.eligibilityProfileLoading || state.eligibilityProfileSaving || !state.profileEditing;
  // Empty read-only facts must not look like the example visa in an input hint.
  elements.eligibilityCurrentStatus.placeholder = state.profileEditing ? "For example, F-1 OPT" : "Not confirmed";
  elements.eligibilityWorkAuthorization.placeholder = state.profileEditing ? "Authorized to work in the United States under…" : "Not confirmed";
  [
    elements.eligibilityCurrentStatus,
    elements.eligibilityWorkAuthorization,
    elements.eligibilitySponsorshipNow,
    elements.eligibilitySponsorshipFuture,
    elements.eligibilityCitizenshipConflict,
    elements.eligibilityClearanceConflict,
    elements.eligibilityStemOpt,
  ].forEach((input) => { input.disabled = busy; });
  elements.saveEligibilityProfileButton.disabled = busy || !state.eligibilityProfileDirty;
  elements.saveEligibilityProfileButton.textContent = state.eligibilityProfileSaving
    ? "Saving…"
    : "Save eligibility facts";
  // Eligibility facts load after the profile form, so the titles follow every render.
  syncReadonlyValueTitles();
}

export async function loadEligibilityProfile({ keepMessage = false } = {}) {
  state.eligibilityProfileLoading = true;
  renderEligibilityProfile();
  try {
    state.eligibilityProfile = await api.getVaultEditor("eligibility");
    populateEligibilityProfile(state.eligibilityProfile);
    if (!keepMessage) {
      elements.eligibilityProfileMessage.className = "form-message";
      elements.eligibilityProfileMessage.textContent = "";
    }
  } catch (error) {
    elements.eligibilityProfileMessage.className = "form-message error";
    elements.eligibilityProfileMessage.textContent = formatRequestError(error);
  } finally {
    state.eligibilityProfileLoading = false;
    renderEligibilityProfile();
  }
}

function markEligibilityProfileDirty() {
  if (state.eligibilityProfileLoading || state.eligibilityProfileSaving) return;
  state.eligibilityProfileDirty = true;
  elements.eligibilityProfileMessage.className = "form-message";
  elements.eligibilityProfileMessage.textContent = "Unsaved eligibility change.";
  renderEligibilityProfile();
}

async function saveEligibilityProfile() {
  if (
    !state.eligibilityProfile
    || !state.eligibilityProfileDirty
    || state.eligibilityProfileSaving
  ) return;
  if (state.eligibilityProfile.vault_schema_version !== 2 && (
    !elements.eligibilityCurrentStatus.value.trim()
    || !elements.eligibilityWorkAuthorization.value.trim()
    || elements.eligibilitySponsorshipFuture.value === ""
    || elements.eligibilityStemOpt.value === ""
  )) {
    elements.eligibilityProfileMessage.className = "form-message error";
    elements.eligibilityProfileMessage.textContent = "This legacy profile requires status, authorization, future sponsorship and STEM OPT facts.";
    return;
  }
  state.eligibilityProfileSaving = true;
  elements.eligibilityProfileMessage.className = "form-message";
  elements.eligibilityProfileMessage.textContent = "Saving confirmed eligibility facts…";
  renderEligibilityProfile();
  try {
    state.eligibilityProfile = await api.updateEligibilityProfile(
      {
        current_status: elements.eligibilityCurrentStatus.value,
        work_authorization: elements.eligibilityWorkAuthorization.value,
        requires_sponsorship_now: optionalBooleanControlValue(elements.eligibilitySponsorshipNow),
        requires_future_sponsorship: optionalBooleanControlValue(elements.eligibilitySponsorshipFuture),
        stem_opt_eligible: optionalBooleanControlValue(elements.eligibilityStemOpt),
        citizenship_required_blocker: optionalBooleanControlValue(elements.eligibilityCitizenshipConflict),
        clearance_required_blocker: optionalBooleanControlValue(elements.eligibilityClearanceConflict),
      },
      state.eligibilityProfile.revision,
    );
    populateEligibilityProfile(state.eligibilityProfile);
    elements.eligibilityProfileMessage.className = "form-message success";
    elements.eligibilityProfileMessage.textContent = "Eligibility facts saved. Existing assessments refresh when opened.";
  } catch (error) {
    elements.eligibilityProfileMessage.className = "form-message error";
    elements.eligibilityProfileMessage.textContent = formatRequestError(error);
  } finally {
    state.eligibilityProfileSaving = false;
    renderEligibilityProfile();
  }
}

function populateHardBlockerPolicy(snapshot) {
  const classes = snapshot?.classes || {};
  elements.hardBlockerWorkAuthorization.checked = classes.citizenship_work_authorization !== false;
  elements.hardBlockerClearance.checked = classes.active_clearance !== false;
  elements.hardBlockerExperience.checked = classes.minimum_experience_years !== false;
  state.hardBlockerPolicyDirty = false;
}

function renderHardBlockerPolicy() {
  const busy = state.hardBlockerPolicyLoading || state.hardBlockerPolicySaving || !state.profileEditing;
  [
    elements.hardBlockerWorkAuthorization,
    elements.hardBlockerClearance,
    elements.hardBlockerExperience,
  ].forEach((input) => { input.disabled = busy; });
  elements.saveHardBlockerPolicyButton.disabled = busy || !state.hardBlockerPolicyDirty;
  elements.saveHardBlockerPolicyButton.textContent = state.hardBlockerPolicySaving
    ? "Saving…"
    : "Save policy";
}

export async function loadHardBlockerPolicy({ keepMessage = false } = {}) {
  state.hardBlockerPolicyLoading = true;
  renderHardBlockerPolicy();
  try {
    state.hardBlockerPolicy = await api.getHardBlockerPolicy();
    populateHardBlockerPolicy(state.hardBlockerPolicy);
    if (!keepMessage) {
      elements.hardBlockerPolicyMessage.className = "form-message profile-policy-message";
      elements.hardBlockerPolicyMessage.textContent = "";
    }
  } catch (error) {
    elements.hardBlockerPolicyMessage.className = "form-message profile-policy-message error";
    elements.hardBlockerPolicyMessage.textContent = formatRequestError(error);
  } finally {
    state.hardBlockerPolicyLoading = false;
    renderHardBlockerPolicy();
  }
}

function markHardBlockerPolicyDirty() {
  if (state.hardBlockerPolicyLoading || state.hardBlockerPolicySaving) return;
  state.hardBlockerPolicyDirty = true;
  elements.hardBlockerPolicyMessage.className = "form-message profile-policy-message";
  elements.hardBlockerPolicyMessage.textContent = "Unsaved policy change.";
  renderHardBlockerPolicy();
}

async function saveHardBlockerPolicy() {
  if (
    !state.hardBlockerPolicy
    || !state.hardBlockerPolicyDirty
    || state.hardBlockerPolicySaving
  ) return;
  state.hardBlockerPolicySaving = true;
  elements.hardBlockerPolicyMessage.className = "form-message profile-policy-message";
  elements.hardBlockerPolicyMessage.textContent = "Saving local assessment policy…";
  renderHardBlockerPolicy();
  try {
    state.hardBlockerPolicy = await api.updateHardBlockerPolicy(
      {
        citizenship_work_authorization: elements.hardBlockerWorkAuthorization.checked,
        active_clearance: elements.hardBlockerClearance.checked,
        minimum_experience_years: elements.hardBlockerExperience.checked,
      },
      state.hardBlockerPolicy.revision,
    );
    populateHardBlockerPolicy(state.hardBlockerPolicy);
    elements.hardBlockerPolicyMessage.className = "form-message profile-policy-message success";
    elements.hardBlockerPolicyMessage.textContent = "Policy saved. Existing assessments refresh when opened.";
  } catch (error) {
    elements.hardBlockerPolicyMessage.className = "form-message profile-policy-message error";
    elements.hardBlockerPolicyMessage.textContent = formatRequestError(error);
  } finally {
    state.hardBlockerPolicySaving = false;
    renderHardBlockerPolicy();
  }
}

async function cancelProfileEdit() {
  if (!state.profileFormDirty) {
    setProfileEditing(false);
    elements.profileMessage.textContent = "";
    return;
  }
  if (!window.confirm("Discard unsaved profile changes?")) return;
  try {
    await loadVaultProfile({ keepMessage: true, refresh: true });
    elements.profileMessage.className = "form-message";
    elements.profileMessage.textContent = "Unsaved changes discarded.";
  } catch (error) {
    elements.profileMessage.className = "form-message error";
    elements.profileMessage.textContent = formatRequestError(error);
  }
}

async function saveVaultProfile(event) {
  event.preventDefault();
  const record = {
    name: elements.profileName.value,
    given_name: elements.profileGivenName.value,
    family_name: elements.profileFamilyName.value,
    preferred_name: elements.profilePreferredName.value,
    major: elements.profileMajor.value,
    current_location: elements.profileCurrentLocation.value,
    target_location: elements.profileTargetLocation.value,
    location_flexibility: elements.profileLocationFlexibility.value,
    work_authorization: profileWorkAuthorizationValue(),
    email: elements.profileEmail.value,
    phone: elements.profilePhone.value,
    address: elements.profileAddress.value,
    address_line1: elements.profileAddressLine1.value,
    address_line2: elements.profileAddressLine2.value,
    city: elements.profileCity.value,
    region: elements.profileRegion.value,
    postal_code: elements.profilePostalCode.value,
    country: elements.profileCountry.value,
    linkedin: elements.profileLinkedin.value,
    github: elements.profileGithub.value,
    website: elements.profileWebsite.value,
    role_targets: vaultLines(elements.profileRoleTargets.value, true),
  };
  elements.saveProfileButton.disabled = true;
  elements.saveProfileButton.textContent = "Saving...";
  elements.profileMessage.className = "form-message";
  elements.profileMessage.textContent = "Validating canonical Career Vault...";
  try {
    const result = await requestJson("/api/vault/profile", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ expected_revision: state.profileRevision, record }),
    });
    state.profileRecord = result.record;
    state.profileRevision = result.revision;
    populateProfileForm(result.record);
    setProfileEditing(false);
    elements.profileMessage.textContent = "Profile saved.";
  } catch (error) {
    elements.profileMessage.className = "form-message error";
    elements.profileMessage.textContent = formatRequestError(error);
  } finally {
    elements.saveProfileButton.disabled = false;
    elements.saveProfileButton.textContent = "Save profile";
    await pollLogs();
  }
}

export function initProfile(deps = {}) {
  ({ ensureViewData } = deps);
  elements.cancelProfileButton.addEventListener("click", cancelProfileEdit);
  elements.editProfileButton.addEventListener("click", () => {
    setProfileEditing(true);
    elements.profileName.focus();
  });
  elements.profileForm.addEventListener("submit", saveVaultProfile);
  elements.profileForm.addEventListener("input", (event) => {
    if (!event.target.closest(".profile-policy-card, .eligibility-profile-section")) {
      state.profileFormDirty = true;
    }
  });
  elements.profileForm.addEventListener("change", (event) => {
    if (!event.target.closest(".profile-policy-card, .eligibility-profile-section")) {
      state.profileFormDirty = true;
    }
  });
  elements.profileWorkAuthorization.addEventListener("change", syncProfileWorkAuthorizationOtherField);
  [
    elements.eligibilityCurrentStatus,
    elements.eligibilityWorkAuthorization,
    elements.eligibilitySponsorshipNow,
    elements.eligibilitySponsorshipFuture,
    elements.eligibilityCitizenshipConflict,
    elements.eligibilityClearanceConflict,
    elements.eligibilityStemOpt,
  ].forEach((input) => input.addEventListener("change", markEligibilityProfileDirty));
  elements.eligibilityCurrentStatus.addEventListener("input", markEligibilityProfileDirty);
  elements.eligibilityWorkAuthorization.addEventListener("input", markEligibilityProfileDirty);
  elements.saveEligibilityProfileButton.addEventListener("click", saveEligibilityProfile);
  [
    elements.hardBlockerWorkAuthorization,
    elements.hardBlockerClearance,
    elements.hardBlockerExperience,
  ].forEach((input) => input.addEventListener("change", markHardBlockerPolicyDirty));
  elements.saveHardBlockerPolicyButton.addEventListener("click", saveHardBlockerPolicy);
}
