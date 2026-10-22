"use strict";

// This function is injected only after an explicit side-panel action. Keep it
// self-contained: the WebExtensions scripting API serializes the function body without this
// file's outer scope. Review and Fill intentionally share this one matcher so
// their safety decisions cannot drift.

function runCareerHubApplicationForm(action, payload, maximumControls) {
  const contractVersion = 2;
  const fieldKinds = new Map([
    ["full_name", "text"],
    ["given_name", "text"],
    ["family_name", "text"],
    ["preferred_name", "text"],
    ["email", "text"],
    ["phone", "text"],
    ["address", "text"],
    ["address_line1", "text"],
    ["address_line2", "text"],
    ["city", "text"],
    ["region", "text"],
    ["postal_code", "text"],
    ["country", "text"],
    ["current_location", "text"],
    ["linkedin", "text"],
    ["github", "text"],
    ["website", "text"],
    ["major", "text"],
    ["current_status", "text"],
    ["work_authorization", "text"],
    ["requires_sponsorship_now", "boolean"],
    ["requires_future_sponsorship", "boolean"],
    ["stem_opt_eligible", "boolean"],
  ]);
  const supportedFieldIds = new Set(fieldKinds.keys());
  const eligibilityFieldIds = new Set([
    "current_status",
    "work_authorization",
    "requires_sponsorship_now",
    "requires_future_sponsorship",
    "stem_opt_eligible",
  ]);
  const allowedTextInputTypes = new Set(["text", "email", "tel", "url"]);
  const alwaysExcludedPattern = /\b(password|passcode|one[ -]?time|otp|pin|social security|ssn|tax id|credit card|debit card|card number|cvv|cvc|bank|routing|salary|compensation|date of birth|birth date|dob|age|race|ethnicity|gender|sex|pronoun|disability|veteran|military|security clearance|background check|resume|cover letter|upload|attachment|signature|why (?:do|are|would)|describe|explain|years? of experience|referr(?:al|ed)|hear about|terms|consent)\b/i;
  const eligibilityPattern = /\b(citizen|citizenship|visa|immigration|sponsor|sponsorship|work authori[sz]ation|stem opt)\b/i;

  const normalize = (value, maximum = 180) => {
    if (typeof value !== "string") return "";
    return value
      .normalize("NFKC")
      .replace(/([a-z])([A-Z])/g, "$1 $2")
      .replace(/[_\-.]+/g, " ")
      .replace(/\s+/g, " ")
      .trim()
      .slice(0, maximum);
  };
  const folded = (value) => normalize(value).toLowerCase();
  const autocompleteToken = (element) => {
    const tokens = String(element.autocomplete || "")
      .normalize("NFKC")
      .trim()
      .toLowerCase()
      .split(/\s+/)
      .filter(Boolean);
    return (tokens.at(-1) || "").slice(0, 80);
  };
  const textWithoutControls = (element) => {
    if (!(element instanceof HTMLElement)) return "";
    const clone = element.cloneNode(true);
    clone.querySelectorAll("input, select, textarea, button, script, style").forEach(
      (control) => control.remove(),
    );
    return normalize(clone.textContent || "");
  };
  const hash = (value, seed) => {
    let result = seed >>> 0;
    for (let index = 0; index < value.length; index += 1) {
      result ^= value.charCodeAt(index);
      result = Math.imul(result, 16777619) >>> 0;
    }
    return result.toString(16).padStart(8, "0");
  };
  const visible = (element) => {
    if (!(element instanceof HTMLElement)) return false;
    if (
      element.hidden
      || element.disabled
      || element.readOnly
      || element.getAttribute("aria-hidden")?.toLowerCase() === "true"
      || element.closest('[hidden], [aria-hidden="true"]')
    ) {
      return false;
    }
    let style;
    try {
      style = globalThis.getComputedStyle(element);
    } catch {
      return false;
    }
    return !(
      style.display === "none"
      || style.visibility === "hidden"
      || style.visibility === "collapse"
      || style.opacity === "0"
      || element.getClientRects().length === 0
    );
  };
  const referencedText = (ids) => normalize(
    ids
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 6)
      .map((id) => textWithoutControls(document.getElementById(id)))
      .join(" "),
  );
  const labelFor = (element) => {
    const candidates = [];
    const ariaLabel = normalize(element.getAttribute("aria-label") || "");
    if (ariaLabel) candidates.push(ariaLabel);
    const labelledBy = element.getAttribute("aria-labelledby") || "";
    if (labelledBy) candidates.push(referencedText(labelledBy));
    for (const label of Array.from(element.labels || []).slice(0, 3)) {
      candidates.push(textWithoutControls(label));
    }
    candidates.push(normalize(element.placeholder || ""));
    candidates.push(normalize(element.name || ""));
    candidates.push(normalize(element.id || ""));
    return candidates.find(Boolean) || "Unlabelled field";
  };
  const formKeyFor = (element) => {
    const root = element.form || element.closest("form, [role='form']");
    if (!root) return "document";
    const roots = Array.from(document.querySelectorAll("form, [role='form']")).slice(0, 64);
    const ordinal = roots.indexOf(root);
    const signature = [
      ordinal >= 0 ? String(ordinal) : "unknown",
      folded(root.getAttribute("aria-label") || ""),
      folded(root.getAttribute("aria-labelledby") || ""),
      folded(root.getAttribute("name") || ""),
      folded(root.id || ""),
    ].join("|");
    return `form:${hash(signature, 2166136261)}${hash(signature, 3339675911)}`;
  };
  const radioGroupLabel = (elements) => {
    const first = elements[0];
    const roleGroup = first.closest('[role="radiogroup"]');
    if (roleGroup) {
      const ariaLabel = normalize(roleGroup.getAttribute("aria-label") || "");
      if (ariaLabel) return ariaLabel;
      const labelledBy = roleGroup.getAttribute("aria-labelledby") || "";
      if (labelledBy) {
        const label = referencedText(labelledBy);
        if (label) return label;
      }
    }
    const fieldset = first.closest("fieldset");
    const legend = fieldset?.querySelector(":scope > legend");
    const legendText = normalize(legend?.textContent || "");
    if (legendText) return legendText;
    const sharedLabel = normalize(first.getAttribute("data-question") || "");
    return sharedLabel || "Unlabelled choice";
  };
  const descriptorFor = (element, type = null, label = null, options = []) => {
    const controlLabel = label || labelFor(element);
    const autocomplete = autocompleteToken(element);
    const name = folded(element.name || "");
    const id = folded(element.id || "");
    const placeholder = folded(element.placeholder || "");
    const controlType = type || (
      element instanceof HTMLSelectElement
        ? "select"
        : element instanceof HTMLTextAreaElement
          ? "textarea"
          : folded(element.type || "text") || "text"
    );
    return {
      autocomplete,
      form_key: formKeyFor(element),
      id,
      label: controlLabel,
      name,
      options,
      placeholder,
      text: [folded(controlLabel), autocomplete, name, id, placeholder]
        .filter(Boolean)
        .join(" "),
      type: controlType,
    };
  };
  const targetKey = (descriptor) => {
    const signature = [
      descriptor.form_key,
      descriptor.type,
      descriptor.label.toLowerCase(),
      descriptor.name,
      descriptor.id,
      descriptor.autocomplete,
      descriptor.placeholder,
      descriptor.options.map((option) => option.normalized).join(";"),
    ].join("|");
    return `${hash(signature, 2166136261)}${hash(signature, 3339675911)}`;
  };
  const score = (fieldId, descriptor) => {
    const text = descriptor.text;
    const autocomplete = descriptor.autocomplete;
    const kind = descriptor.type;
    if (alwaysExcludedPattern.test(text)) return 0;
    if (eligibilityPattern.test(text) && !eligibilityFieldIds.has(fieldId)) return 0;
    if (fieldKinds.get(fieldId) === "boolean" && !["select", "radio"].includes(kind)) return 0;
    if (["current_status", "work_authorization"].includes(fieldId) && ["select", "radio"].includes(kind)) return 0;
    if (fieldId === "full_name") {
      if (autocomplete === "name") return 160;
      if (/\b(first|given|last|family|middle|preferred|nick) name\b/.test(text)) return 0;
      if (/\b(full|legal|your|applicant) name\b/.test(text)) return 140;
      return /^(name|applicant name)$/.test(folded(descriptor.label)) ? 120 : 0;
    }
    if (fieldId === "given_name") {
      if (autocomplete === "given-name") return 165;
      return /\b(first|given) name\b/.test(text) ? 145 : 0;
    }
    if (fieldId === "family_name") {
      if (autocomplete === "family-name") return 165;
      return /\b(last|family|sur)name\b/.test(text) ? 145 : 0;
    }
    if (fieldId === "preferred_name") {
      if (autocomplete === "nickname") return 165;
      return /\b(preferred|nick) ?name\b/.test(text) ? 145 : 0;
    }
    if (fieldId === "email") {
      if (autocomplete === "email") return 160;
      if (kind === "email") return 150;
      return /\be[ -]?mail(?: address)?\b/.test(text) ? 130 : 0;
    }
    if (fieldId === "phone") {
      if (autocomplete === "tel" || autocomplete.startsWith("tel-")) return 160;
      if (kind === "tel") return 150;
      return /\b(phone|mobile|telephone)(?: number)?\b/.test(text) ? 130 : 0;
    }
    if (fieldId === "address") {
      if (autocomplete === "street-address") return 165;
      if (/\baddress line [12]\b/.test(text)) return 0;
      if (/\b(full|mailing|home|street) address\b/.test(text)) return 135;
      return /^(address|required address)$/.test(folded(descriptor.label)) ? 115 : 0;
    }
    if (fieldId === "address_line1") {
      if (autocomplete === "address-line1") return 165;
      return /\b(address line 1|street address 1|address 1)\b/.test(text) ? 145 : 0;
    }
    if (fieldId === "address_line2") {
      if (autocomplete === "address-line2") return 165;
      return /\b(address line 2|street address 2|address 2|apartment|apt|suite|unit)\b/.test(text) ? 145 : 0;
    }
    if (fieldId === "city") {
      if (autocomplete === "address-level2") return 165;
      return /^(city|town|city or town)$/.test(folded(descriptor.label)) ? 135 : 0;
    }
    if (fieldId === "region") {
      if (autocomplete === "address-level1") return 165;
      return /^(state|province|region|state province|state or province)$/.test(folded(descriptor.label)) ? 135 : 0;
    }
    if (fieldId === "postal_code") {
      if (autocomplete === "postal-code") return 165;
      return /\b(zip|postal)(?: code)?\b/.test(text) ? 140 : 0;
    }
    if (fieldId === "country") {
      if (["country", "country-name"].includes(autocomplete)) return 165;
      return /^(country|country region|country or region)$/.test(folded(descriptor.label)) ? 135 : 0;
    }
    if (fieldId === "current_location") {
      if (/\b(desired|preferred|job|office|work) location\b/.test(text)) return 0;
      if (/\bcurrent location\b/.test(text)) return 145;
      return /^(location|your location)$/.test(folded(descriptor.label)) ? 115 : 0;
    }
    if (fieldId === "linkedin") {
      return /\blinked\s*in(?: profile| url)?\b/.test(text) ? 150 : 0;
    }
    if (fieldId === "github") {
      return /\bgithub(?: profile| url)?\b/.test(text) ? 150 : 0;
    }
    if (fieldId === "website") {
      if (/\b(company|employer) (?:web ?site|site)\b/.test(text)) return 0;
      return /\b(portfolio|personal web ?site|personal site|web ?site)(?: url)?\b/.test(text) ? 130 : 0;
    }
    if (fieldId === "major") {
      return /\b(major|field of study|area of study|academic concentration)\b/.test(text) ? 135 : 0;
    }
    if (fieldId === "current_status") {
      return /\b(current )?(immigration|visa) status\b/.test(text) ? 150 : 0;
    }
    if (fieldId === "work_authorization") {
      return /\bwork authori[sz]ation (?:details|statement|status)\b/.test(text) ? 150 : 0;
    }
    if (fieldId === "requires_sponsorship_now") {
      if (!/\bsponsor(?:ship)?\b/.test(text)) return 0;
      const now = /\b(now|currently|at this time)\b/.test(text);
      const future = /\b(future|later|eventually)\b/.test(text);
      if (now && future) return 160;
      if (now) return 155;
      return future ? 0 : 125;
    }
    if (fieldId === "requires_future_sponsorship") {
      if (!/\bsponsor(?:ship)?\b/.test(text)) return 0;
      const now = /\b(now|currently|at this time)\b/.test(text);
      const future = /\b(future|later|eventually)\b/.test(text);
      if (now && future) return 160;
      return future ? 155 : 0;
    }
    if (fieldId === "stem_opt_eligible") {
      return /\bstem opt(?: eligible| eligibility)?\b/.test(text) ? 155 : 0;
    }
    return 0;
  };

  const booleanMeaning = (value) => {
    const normalized = folded(value);
    if (["yes", "y", "true"].includes(normalized)) return true;
    if (["no", "n", "false"].includes(normalized)) return false;
    return null;
  };
  const choiceFor = (control, valueKind, value) => {
    if (control.descriptor.type === "select") {
      const matches = control.descriptor.options.filter((option) => (
        valueKind === "boolean"
          ? booleanMeaning(option.normalized) === value
          : option.normalized === folded(value)
      ));
      return matches.length === 1 ? matches[0] : null;
    }
    if (control.descriptor.type === "radio") {
      const matches = control.descriptor.options.filter((option) => (
        valueKind === "boolean"
          ? booleanMeaning(option.normalized) === value
          : option.normalized === folded(value)
      ));
      return matches.length === 1 ? matches[0] : null;
    }
    return valueKind === "text" && typeof value === "string"
      ? { value }
      : null;
  };
  const canRepresentKind = (control, valueKind) => {
    if (["select", "radio"].includes(control.descriptor.type)) {
      if (valueKind === "boolean") {
        const meanings = new Set(
          control.descriptor.options
            .map((option) => booleanMeaning(option.normalized))
            .filter((value) => typeof value === "boolean"),
        );
        return meanings.has(true) && meanings.has(false);
      }
      return control.descriptor.options.some((option) => option.value !== "");
    }
    return valueKind === "text";
  };
  const empty = (control) => {
    if (control.descriptor.type === "radio") {
      return !control.elements.some((element) => element.checked);
    }
    return String(control.element.value || "").trim() === "";
  };

  const limit = Number.isInteger(maximumControls) && maximumControls > 0
    ? Math.min(maximumControls, 500)
    : 240;
  const rawLimit = Math.min(Math.max(limit * 8, limit), 2000);
  const controls = [];
  const radioGroups = new Map();
  let excluded = 0;
  let rawExamined = 0;
  for (const element of document.querySelectorAll("input, select, textarea")) {
    if (rawExamined >= rawLimit || controls.length >= limit) break;
    rawExamined += 1;
    if (!visible(element)) {
      excluded = Math.min(limit, excluded + 1);
      continue;
    }
    if (element instanceof HTMLInputElement && folded(element.type) === "radio") {
      const name = folded(element.name || "");
      if (!name) {
        excluded = Math.min(limit, excluded + 1);
        continue;
      }
      const key = `${formKeyFor(element)}|${name}`;
      const group = radioGroups.get(key) || [];
      group.push(element);
      radioGroups.set(key, group);
      continue;
    }
    if (
      element instanceof HTMLInputElement
      && !allowedTextInputTypes.has(folded(element.type || "text") || "text")
    ) {
      excluded = Math.min(limit, excluded + 1);
      continue;
    }
    const options = element instanceof HTMLSelectElement
      ? Array.from(element.options).slice(0, 80).map((option) => ({
        element: option,
        normalized: folded(option.textContent || option.value || ""),
        value: option.value,
      })).filter((option) => option.normalized)
      : [];
    const descriptor = descriptorFor(element, null, null, options);
    if (alwaysExcludedPattern.test(descriptor.text)) {
      excluded = Math.min(limit, excluded + 1);
      continue;
    }
    controls.push({ descriptor, element, elements: [element] });
  }
  for (const elements of radioGroups.values()) {
    if (controls.length >= limit) break;
    const label = radioGroupLabel(elements);
    const options = elements.map((element) => ({
      element,
      normalized: folded(labelFor(element) || element.value || ""),
      value: element.value,
    })).filter((option) => option.normalized);
    const descriptor = descriptorFor(elements[0], "radio", label, options);
    if (alwaysExcludedPattern.test(descriptor.text) || options.length < 2) {
      excluded = Math.min(limit, excluded + elements.length);
      continue;
    }
    controls.push({ descriptor, element: elements[0], elements });
  }

  if (action === "inspect") {
    const enabled = new Set(Array.isArray(payload) ? payload : []);
    const groups = new Map();
    let crossFieldAmbiguous = 0;
    for (const control of controls) {
      const { descriptor } = control;
      const scores = Array.from(supportedFieldIds)
        .map((fieldId) => ({ fieldId, score: score(fieldId, descriptor) }))
        .filter((entry) => entry.score > 0)
        .sort((left, right) => right.score - left.score || left.fieldId.localeCompare(right.fieldId));
      if (!scores.length) continue;
      if (scores.length > 1 && scores[0].score === scores[1].score) {
        if (scores.some((entry) => enabled.has(entry.fieldId))) crossFieldAmbiguous += 1;
        continue;
      }
      const winner = scores[0];
      if (!enabled.has(winner.fieldId)) continue;
      const valueKind = fieldKinds.get(winner.fieldId);
      if (!canRepresentKind(control, valueKind)) continue;
      const group = groups.get(descriptor.form_key) || {
        candidatesByField: new Map(),
        formKey: descriptor.form_key,
      };
      const candidates = group.candidatesByField.get(winner.fieldId) || [];
      candidates.push({
        control,
        descriptor,
        empty: empty(control),
        score: winner.score,
        target_key: targetKey(descriptor),
      });
      group.candidatesByField.set(winner.fieldId, candidates);
      groups.set(descriptor.form_key, group);
    }

    const rankedGroups = Array.from(groups.values())
      .map((group) => ({ ...group, fieldCount: group.candidatesByField.size }))
      .sort((left, right) => right.fieldCount - left.fieldCount || left.formKey.localeCompare(right.formKey));
    const mappings = [];
    let ambiguous = crossFieldAmbiguous;
    let nonEmpty = 0;
    if (rankedGroups.length) {
      const topGroups = rankedGroups.filter((group) => group.fieldCount === rankedGroups[0].fieldCount);
      if (topGroups.length > 1) {
        const matchedFields = new Set(
          topGroups.flatMap((group) => Array.from(group.candidatesByField.keys())),
        );
        ambiguous = Math.min(
          limit,
          ambiguous + topGroups.reduce(
            (count, group) => count + Array.from(group.candidatesByField.values())
              .reduce((subtotal, candidates) => subtotal + candidates.length, 0),
            0,
          ),
        );
        for (const fieldId of Array.from(enabled).filter((value) => matchedFields.has(value))) {
          mappings.push({
            field_id: fieldId,
            target_label: `${topGroups.length} possible forms`,
            target_kind: "multiple",
            empty: false,
            selectable: false,
            skip_reason: "ambiguous",
            target_key: "",
          });
        }
      } else {
        const candidatesByField = topGroups[0].candidatesByField;
        for (const fieldId of Array.from(enabled).filter((value) => supportedFieldIds.has(value))) {
          const candidates = (candidatesByField.get(fieldId) || [])
            .sort((left, right) => right.score - left.score || left.target_key.localeCompare(right.target_key));
          if (!candidates.length) continue;
          if (
            candidates.length > 1
            && (
              candidates[0].score - candidates[1].score < 15
              || candidates[0].target_key === candidates[1].target_key
            )
          ) {
            ambiguous = Math.min(limit, ambiguous + candidates.length);
            mappings.push({
              field_id: fieldId,
              target_label: `${candidates.length} possible fields`,
              target_kind: "multiple",
              empty: false,
              selectable: false,
              skip_reason: "ambiguous",
              target_key: "",
            });
            continue;
          }
          const candidate = candidates[0];
          if (!candidate.empty) nonEmpty += 1;
          mappings.push({
            field_id: fieldId,
            target_label: candidate.descriptor.label,
            target_kind: candidate.descriptor.type,
            empty: candidate.empty,
            selectable: candidate.empty,
            skip_reason: candidate.empty ? "" : "non_empty",
            target_key: candidate.target_key,
          });
        }
      }
    }
    return {
      ok: true,
      contract_version: contractVersion,
      controls_examined: controls.length,
      mappings,
      skipped: { ambiguous, excluded, non_empty: nonEmpty },
    };
  }

  if (action !== "fill") {
    return { ok: false, error_code: "APPLICATION_HELPER_SELECTION_INVALID" };
  }
  const instructions = payload;
  if (
    !Array.isArray(instructions)
    || instructions.length === 0
    || instructions.length > supportedFieldIds.size
  ) {
    return { ok: false, error_code: "APPLICATION_HELPER_SELECTION_INVALID" };
  }
  const prepared = [];
  const seenMappings = new Set();
  const seenTargets = new Set();
  for (const instruction of instructions) {
    const expectedKind = fieldKinds.get(instruction?.field_id);
    const validValue = expectedKind === "text"
      ? typeof instruction?.value === "string"
        && Boolean(instruction.value.trim())
        && instruction.value.length <= 2048
        && !instruction.value.includes("\0")
      : expectedKind === "boolean" && typeof instruction?.value === "boolean";
    if (
      !instruction
      || typeof instruction !== "object"
      || Array.isArray(instruction)
      || Object.keys(instruction).length !== 5
      || typeof instruction.mapping_id !== "string"
      || seenMappings.has(instruction.mapping_id)
      || !supportedFieldIds.has(instruction.field_id)
      || instruction.value_kind !== expectedKind
      || typeof instruction.target_key !== "string"
      || !/^[a-f0-9]{16}$/.test(instruction.target_key)
      || seenTargets.has(instruction.target_key)
      || !validValue
    ) {
      return { ok: false, error_code: "APPLICATION_HELPER_SELECTION_INVALID" };
    }
    seenMappings.add(instruction.mapping_id);
    seenTargets.add(instruction.target_key);
    const matches = controls.filter(
      ({ descriptor }) => targetKey(descriptor) === instruction.target_key,
    );
    if (matches.length !== 1 || !empty(matches[0])) {
      return { ok: false, error_code: "APPLICATION_HELPER_FORM_CHANGED" };
    }
    const target = matches[0];
    const competingScores = Array.from(supportedFieldIds)
      .map((fieldId) => ({ fieldId, score: score(fieldId, target.descriptor) }))
      .filter((entry) => entry.score > 0)
      .sort((left, right) => right.score - left.score || left.fieldId.localeCompare(right.fieldId));
    if (
      !competingScores.length
      || competingScores[0].fieldId !== instruction.field_id
      || (competingScores[1] && competingScores[1].score === competingScores[0].score)
    ) {
      return { ok: false, error_code: "APPLICATION_HELPER_FORM_CHANGED" };
    }
    const choice = choiceFor(target, instruction.value_kind, instruction.value);
    if (!choice) return { ok: false, error_code: "APPLICATION_HELPER_FORM_CHANGED" };
    prepared.push({ ...instruction, choice, target });
  }

  const inputValueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
  const textareaValueSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
  const selectValueSetter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set;
  const checkedSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "checked")?.set;
  const originals = prepared.map((item) => ({
    item,
    checked: item.target.elements.map((element) => element.checked),
    value: item.target.element.value,
  }));
  const setPreparedValue = (item) => {
    const { target } = item;
    if (target.descriptor.type === "radio") {
      if (typeof checkedSetter === "function") checkedSetter.call(item.choice.element, true);
      else item.choice.element.checked = true;
      return;
    }
    if (target.descriptor.type === "select") {
      if (typeof selectValueSetter === "function") selectValueSetter.call(target.element, item.choice.value);
      else target.element.value = item.choice.value;
      return;
    }
    const setter = target.element instanceof HTMLTextAreaElement
      ? textareaValueSetter
      : inputValueSetter;
    if (typeof setter === "function") setter.call(target.element, item.value);
    else target.element.value = item.value;
  };
  const restoreOriginal = (original) => {
    const { item } = original;
    if (item.target.descriptor.type === "radio") {
      item.target.elements.forEach((element, index) => {
        if (typeof checkedSetter === "function") checkedSetter.call(element, original.checked[index]);
        else element.checked = original.checked[index];
      });
      return;
    }
    const setter = item.target.descriptor.type === "select"
      ? selectValueSetter
      : item.target.element instanceof HTMLTextAreaElement
        ? textareaValueSetter
        : inputValueSetter;
    if (typeof setter === "function") setter.call(item.target.element, original.value);
    else item.target.element.value = original.value;
  };
  try {
    prepared.forEach(setPreparedValue);
    const valid = prepared.every((item) => {
      if (item.target.descriptor.type === "radio") return item.choice.element.checked;
      if (item.target.descriptor.type === "select") return item.target.element.value === item.choice.value;
      return item.target.element.value === item.value;
    });
    if (!valid) throw new Error("fill did not stick");
  } catch {
    originals.forEach(restoreOriginal);
    return { ok: false, error_code: "APPLICATION_HELPER_FORM_CHANGED" };
  }
  for (const item of prepared) {
    const eventTarget = item.target.descriptor.type === "radio"
      ? item.choice.element
      : item.target.element;
    eventTarget.dispatchEvent(new Event("input", { bubbles: true }));
    eventTarget.dispatchEvent(new Event("change", { bubbles: true }));
  }
  return {
    ok: true,
    filled_mapping_ids: prepared.map((item) => item.mapping_id),
  };
}
