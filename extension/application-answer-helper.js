"use strict";

/**
 * Inspect or fill explicitly reusable application questions in the active page.
 *
 * This function is injected only after a side-panel action. It is intentionally
 * self-contained because the WebExtensions scripting API serializes the function body into the
 * page's main frame. It never submits a form or clicks a page action.
 */
function runCareerHubReusableAnswers(mode, payload, maxControls) {
  const CONTRACT_VERSION = 2;
  const MAX_RAW_CONTROLS = 2000;
  const MAX_CANDIDATES = 30;
  const QUESTION_LIMIT = 300;
  const ANSWER_LIMIT = 500;
  const ANSWER_ID = /^answer_[a-f0-9]{32}$/;
  const forbiddenQuestion = new RegExp(
    "\\b(?:password|passcode|one[ -]?time|otp|pin|social security|ssn|tax id|" +
    "credit card|debit card|card number|cvv|cvc|bank|routing|salary|" +
    "compensation|date of birth|birth date|dob|age|race|ethnicity|gender|" +
    "sex|pronoun|disability|veteran|military|marital|religion|politic(?:s|al)?|" +
    "medical|health|citizen|citizenship|visa|immigration|sponsor|" +
    "sponsorship|work authori[sz]ation|security clearance|background check|" +
    "resume|cover letter|upload|attachment|signature|consent|terms|agree|" +
    "why|describe|explain|tell us|motivation|experience|qualification|skill|" +
    "availability|start date|first name|last name|full name|preferred name|" +
    "email|phone|address|street|city|state|province|postal|zip|country|" +
    "location|linkedin|github|website|portfolio|major|school|university|" +
    "current status|stem opt|this company|this employer|this organization|" +
    "current company|position|role|job)\\b",
    "i",
  );

  function exactKeys(value, keys) {
    return Boolean(
      value
      && typeof value === "object"
      && !Array.isArray(value)
      && Object.keys(value).length === keys.length
      && keys.every((key) => Object.hasOwn(value, key))
    );
  }

  function compactText(value, limit) {
    if (typeof value !== "string" || value.includes("\0")) return "";
    const normalized = value.normalize("NFKC").replace(/\s+/g, " ").trim();
    return normalized.length <= limit ? normalized : "";
  }

  function normalizedQuestion(value) {
    let display = compactText(value, QUESTION_LIMIT);
    display = display.replace(/\s*(?:\*|\(required\))\s*$/i, "").trim();
    if (
      display.length < 4
      || !/[a-z]/i.test(display)
      || forbiddenQuestion.test(display)
    ) {
      return null;
    }
    return {
      display,
      canonical: display.toLocaleLowerCase("en-US").replace(/[ ?!:.]+$/g, ""),
    };
  }

  function textWithoutControls(node) {
    if (!(node instanceof Element)) return "";
    const clone = node.cloneNode(true);
    clone.querySelectorAll("input, select, textarea, button, script, style").forEach(
      (control) => control.remove(),
    );
    return compactText(clone.textContent || "", QUESTION_LIMIT);
  }

  function labelledText(control) {
    const labelledBy = compactText(control.getAttribute("aria-labelledby") || "", 300);
    if (labelledBy) {
      const text = labelledBy
        .split(/\s+/)
        .map((id) => document.getElementById(id))
        .filter(Boolean)
        .map((node) => textWithoutControls(node))
        .filter(Boolean)
        .join(" ");
      if (text) return compactText(text, QUESTION_LIMIT);
    }
    const ariaLabel = compactText(control.getAttribute("aria-label") || "", QUESTION_LIMIT);
    if (ariaLabel) return ariaLabel;
    const labels = Array.from(control.labels || [])
      .map((label) => textWithoutControls(label))
      .filter(Boolean);
    if (labels.length === 1) return labels[0];
    const wrapping = control.closest("label");
    return wrapping ? textWithoutControls(wrapping) : "";
  }

  function radioQuestion(radios) {
    if (!radios.length) return "";
    const fieldsets = new Set(radios.map((radio) => radio.closest("fieldset")));
    if (fieldsets.size === 1) {
      const fieldset = fieldsets.values().next().value;
      const legend = fieldset?.querySelector(":scope > legend");
      const legendText = legend ? textWithoutControls(legend) : "";
      if (legendText) return legendText;
    }
    const groups = new Set(radios.map((radio) => radio.closest('[role="radiogroup"]')));
    if (groups.size === 1) {
      const group = groups.values().next().value;
      if (group) {
        const labelledBy = compactText(group.getAttribute("aria-labelledby") || "", 300);
        if (labelledBy) {
          const text = labelledBy
            .split(/\s+/)
            .map((id) => document.getElementById(id))
            .filter(Boolean)
            .map((node) => textWithoutControls(node))
            .filter(Boolean)
            .join(" ");
          if (text) return compactText(text, QUESTION_LIMIT);
        }
        const aria = compactText(group.getAttribute("aria-label") || "", QUESTION_LIMIT);
        if (aria) return aria;
      }
    }
    return "";
  }

  function isVisible(control) {
    if (!control.isConnected || control.disabled || control.hidden) return false;
    if (control.closest('[hidden], [aria-hidden="true"], [inert]')) return false;
    const style = getComputedStyle(control);
    if (style.display === "none" || style.visibility === "hidden") return false;
    const rect = control.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  }

  function ownerOf(control) {
    return control.form || control.closest("form") || document.body;
  }

  function hash16(value) {
    let first = 0x811c9dc5;
    let second = 0x9e3779b9;
    for (let index = 0; index < value.length; index += 1) {
      const code = value.charCodeAt(index);
      first ^= code;
      first = Math.imul(first, 0x01000193) >>> 0;
      second ^= code + index;
      second = Math.imul(second, 0x85ebca6b) >>> 0;
    }
    return first.toString(16).padStart(8, "0")
      + second.toString(16).padStart(8, "0");
  }

  function optionLabel(control) {
    return compactText(labelledText(control) || control.value || "", ANSWER_LIMIT);
  }

  function entryKey(entry) {
    const primary = entry.controls[0];
    const index = Array.prototype.indexOf.call(
      document.querySelectorAll("input, select"),
      primary,
    );
    return hash16([
      entry.kind,
      entry.question.canonical,
      primary.id || "",
      primary.getAttribute("name") || "",
      String(index),
    ].join("\u001f"));
  }

  function buildEntries() {
    const raw = Array.from(document.querySelectorAll("input, select"));
    if (raw.length > MAX_RAW_CONTROLS) {
      return { error_code: "APPLICATION_ANSWER_PAGE_TOO_COMPLEX" };
    }
    const visible = raw.filter((control) => {
      if (!isVisible(control)) return false;
      if (control instanceof HTMLSelectElement) return !control.multiple;
      const type = (control.getAttribute("type") || "text").toLowerCase();
      return type === "text" || type === "radio";
    });
    if (visible.length > maxControls) {
      return { error_code: "APPLICATION_ANSWER_PAGE_TOO_COMPLEX" };
    }

    const entries = [];
    const radioGroups = new Map();
    for (const control of visible) {
      if (control instanceof HTMLInputElement && control.type === "radio") {
        const name = compactText(control.name || "", 180);
        if (!name) continue;
        const owner = ownerOf(control);
        let ownerGroups = radioGroups.get(owner);
        if (!ownerGroups) {
          ownerGroups = new Map();
          radioGroups.set(owner, ownerGroups);
        }
        const group = ownerGroups.get(name) || [];
        group.push(control);
        ownerGroups.set(name, group);
        continue;
      }
      const question = normalizedQuestion(labelledText(control));
      if (!question) continue;
      entries.push({
        kind: control instanceof HTMLSelectElement ? "select" : "text",
        controls: [control],
        owner: ownerOf(control),
        question,
      });
    }
    for (const [owner, groups] of radioGroups.entries()) {
      for (const radios of groups.values()) {
        if (radios.length < 2) continue;
        const question = normalizedQuestion(radioQuestion(radios));
        if (!question) continue;
        entries.push({ kind: "radio", controls: radios, owner, question });
      }
    }
    for (const entry of entries) entry.target_key = entryKey(entry);
    return { entries, controls_examined: visible.length };
  }

  function isEmpty(entry) {
    if (entry.kind === "text") return !entry.controls[0].value.trim();
    if (entry.kind === "select") return !entry.controls[0].value.trim();
    return !entry.controls.some((radio) => radio.checked);
  }

  function yesNoValue(value) {
    const normalized = compactText(value, ANSWER_LIMIT).toLocaleLowerCase("en-US");
    if (["yes", "y", "true"].includes(normalized)) return true;
    if (["no", "n", "false"].includes(normalized)) return false;
    return null;
  }

  function choices(entry) {
    if (entry.kind === "select") {
      return Array.from(entry.controls[0].options)
        .filter((option) => !option.disabled && compactText(option.value || "", ANSWER_LIMIT))
        .map((option) => ({ control: option, label: compactText(option.textContent || option.value, ANSWER_LIMIT) }));
    }
    if (entry.kind === "radio") {
      return entry.controls.map((radio) => ({ control: radio, label: optionLabel(radio) }));
    }
    return [];
  }

  function compatibleWithoutValue(entry, valueKind) {
    if (valueKind === "text") return entry.kind === "text";
    if (valueKind === "choice") return ["select", "radio"].includes(entry.kind);
    if (valueKind !== "boolean" || !["select", "radio"].includes(entry.kind)) return false;
    const represented = new Set(choices(entry).map((choice) => yesNoValue(choice.label)));
    return represented.has(true) && represented.has(false);
  }

  function currentValue(entry) {
    if (entry.kind === "text") {
      const value = compactText(entry.controls[0].value, ANSWER_LIMIT);
      return value ? { value_kind: "text", value } : null;
    }
    if (entry.kind === "select") {
      const control = entry.controls[0];
      if (!control.value.trim()) return null;
      const option = control.options[control.selectedIndex];
      const value = compactText(option?.textContent || control.value, ANSWER_LIMIT);
      return value ? { value_kind: "choice", value } : null;
    }
    const selected = entry.controls.find((radio) => radio.checked);
    if (!selected) return null;
    const label = optionLabel(selected);
    const boolean = yesNoValue(label);
    return boolean === null
      ? (label ? { value_kind: "choice", value: label } : null)
      : { value_kind: "boolean", value: boolean };
  }

  function strongestOwner(entries, score) {
    const scores = new Map();
    for (const entry of entries) {
      const points = score(entry);
      if (points > 0) scores.set(entry.owner, (scores.get(entry.owner) || 0) + points);
    }
    if (!scores.size) return null;
    const best = Math.max(...scores.values());
    const winners = Array.from(scores.entries()).filter(([, value]) => value === best);
    return winners.length === 1 ? winners[0][0] : undefined;
  }

  function inspectSaved(entries, descriptors, controlsExamined) {
    if (!Array.isArray(descriptors) || descriptors.length > 200) {
      return { ok: false, error_code: "APPLICATION_ANSWER_REQUEST_INVALID" };
    }
    const byQuestion = new Map();
    for (const descriptor of descriptors) {
      if (
        !exactKeys(descriptor, ["id", "question", "value_kind"])
        || !ANSWER_ID.test(descriptor.id)
        || !["text", "choice", "boolean"].includes(descriptor.value_kind)
      ) {
        return { ok: false, error_code: "APPLICATION_ANSWER_REQUEST_INVALID" };
      }
      const question = normalizedQuestion(descriptor.question);
      if (!question || byQuestion.has(question.canonical)) {
        return { ok: false, error_code: "APPLICATION_ANSWER_REQUEST_INVALID" };
      }
      byQuestion.set(question.canonical, { ...descriptor, question });
    }
    const owner = strongestOwner(entries, (entry) => (
      byQuestion.has(entry.question.canonical) ? 1 : 0
    ));
    if (owner === undefined) {
      return { ok: false, error_code: "APPLICATION_ANSWER_FORM_AMBIGUOUS" };
    }
    const scoped = owner ? entries.filter((entry) => entry.owner === owner) : [];
    const mappings = [];
    let ambiguous = 0;
    let nonEmpty = 0;
    for (const descriptor of byQuestion.values()) {
      if (mappings.length === MAX_CANDIDATES) break;
      const matches = scoped.filter(
        (entry) => entry.question.canonical === descriptor.question.canonical,
      );
      if (matches.length > 1) {
        ambiguous += 1;
        mappings.push({
          answer_id: descriptor.id,
          question: descriptor.question.display,
          target_label: "Multiple matching controls",
          target_kind: "multiple",
          empty: false,
          selectable: false,
          skip_reason: "ambiguous",
          target_key: "",
        });
        continue;
      }
      if (matches.length !== 1) continue;
      const entry = matches[0];
      const empty = isEmpty(entry);
      const compatible = compatibleWithoutValue(entry, descriptor.value_kind);
      if (!empty) nonEmpty += 1;
      mappings.push({
        answer_id: descriptor.id,
        question: descriptor.question.display,
        target_label: entry.question.display,
        target_kind: entry.kind,
        empty,
        selectable: empty && compatible,
        skip_reason: empty ? (compatible ? "" : "incompatible") : "non_empty",
        target_key: entry.target_key,
      });
    }
    return {
      ok: true,
      contract_version: CONTRACT_VERSION,
      controls_examined: controlsExamined,
      mappings,
      skipped: { ambiguous, non_empty: nonEmpty },
    };
  }

  function completedCandidates(entries, existingQuestions, controlsExamined) {
    if (!Array.isArray(existingQuestions) || existingQuestions.length > 200) {
      return { ok: false, error_code: "APPLICATION_ANSWER_REQUEST_INVALID" };
    }
    const existing = new Set();
    for (const value of existingQuestions) {
      const question = normalizedQuestion(value);
      if (!question) return { ok: false, error_code: "APPLICATION_ANSWER_REQUEST_INVALID" };
      existing.add(question.canonical);
    }
    const owner = strongestOwner(entries, (entry) => (
      !existing.has(entry.question.canonical) && currentValue(entry) ? 1 : 0
    ));
    if (owner === undefined) {
      return { ok: false, error_code: "APPLICATION_ANSWER_FORM_AMBIGUOUS" };
    }
    const scoped = owner ? entries.filter((entry) => entry.owner === owner) : [];
    const counts = new Map();
    for (const entry of scoped) {
      counts.set(entry.question.canonical, (counts.get(entry.question.canonical) || 0) + 1);
    }
    const candidates = [];
    for (const entry of scoped) {
      if (
        existing.has(entry.question.canonical)
        || counts.get(entry.question.canonical) !== 1
      ) continue;
      const answer = currentValue(entry);
      if (!answer) continue;
      candidates.push({
        candidate_id: `candidate_${candidates.length + 1}`,
        question: entry.question.display,
        target_kind: entry.kind,
        target_key: entry.target_key,
        ...answer,
      });
      if (candidates.length === MAX_CANDIDATES) break;
    }
    return {
      ok: true,
      contract_version: CONTRACT_VERSION,
      controls_examined: controlsExamined,
      candidates,
    };
  }

  function readCompleted(entries, descriptor) {
    if (
      !exactKeys(descriptor, ["question", "target_key", "value_kind"])
      || typeof descriptor.target_key !== "string"
      || !/^[a-f0-9]{16}$/.test(descriptor.target_key)
      || !["text", "choice", "boolean"].includes(descriptor.value_kind)
    ) {
      return { ok: false, error_code: "APPLICATION_ANSWER_REQUEST_INVALID" };
    }
    const question = normalizedQuestion(descriptor.question);
    if (!question) return { ok: false, error_code: "APPLICATION_ANSWER_REQUEST_INVALID" };
    const matches = entries.filter((entry) => (
      entry.question.canonical === question.canonical
      && entry.target_key === descriptor.target_key
    ));
    if (matches.length !== 1) {
      return { ok: false, error_code: "APPLICATION_ANSWER_FORM_CHANGED" };
    }
    const answer = currentValue(matches[0]);
    if (!answer || answer.value_kind !== descriptor.value_kind) {
      return { ok: false, error_code: "APPLICATION_ANSWER_FORM_CHANGED" };
    }
    return {
      ok: true,
      contract_version: CONTRACT_VERSION,
      candidate: {
        question: question.display,
        target_key: descriptor.target_key,
        ...answer,
      },
    };
  }

  function matchingChoice(entry, valueKind, value) {
    const available = choices(entry);
    if (valueKind === "boolean") {
      const matches = available.filter((choice) => yesNoValue(choice.label) === value);
      return matches.length === 1 ? matches[0].control : null;
    }
    if (valueKind !== "choice" || typeof value !== "string") return null;
    const expected = compactText(value, ANSWER_LIMIT).toLocaleLowerCase("en-US");
    const matches = available.filter((choice) => (
      choice.label.toLocaleLowerCase("en-US") === expected
      || compactText(choice.control.value || "", ANSWER_LIMIT).toLocaleLowerCase("en-US") === expected
    ));
    return matches.length === 1 ? matches[0].control : null;
  }

  function setNative(control, property, value) {
    const prototype = control instanceof HTMLSelectElement
      ? HTMLSelectElement.prototype
      : HTMLInputElement.prototype;
    const setter = Object.getOwnPropertyDescriptor(prototype, property)?.set;
    if (setter) setter.call(control, value);
    else control[property] = value;
  }

  function fillSaved(entries, instructions) {
    if (!Array.isArray(instructions) || !instructions.length || instructions.length > 30) {
      return { ok: false, error_code: "APPLICATION_ANSWER_REQUEST_INVALID" };
    }
    const changes = [];
    const filled = [];
    try {
      for (const instruction of instructions) {
        if (
          !exactKeys(instruction, [
            "mapping_id", "answer_id", "question", "target_key", "value_kind", "value",
          ])
          || !/^answer_mapping_(?:[1-9]|[12]\d|30)$/.test(instruction.mapping_id)
          || !ANSWER_ID.test(instruction.answer_id)
          || !/^[a-f0-9]{16}$/.test(instruction.target_key)
        ) throw new Error("invalid");
        const question = normalizedQuestion(instruction.question);
        const matches = entries.filter((entry) => (
          question
          && entry.question.canonical === question.canonical
          && entry.target_key === instruction.target_key
        ));
        if (matches.length !== 1 || !isEmpty(matches[0])) throw new Error("changed");
        const entry = matches[0];
        if (entry.kind === "text") {
          const value = compactText(instruction.value, ANSWER_LIMIT);
          if (instruction.value_kind !== "text" || !value) throw new Error("invalid");
          const control = entry.controls[0];
          changes.push({ control, property: "value", value: control.value });
          setNative(control, "value", value);
          control.dispatchEvent(new Event("input", { bubbles: true }));
          control.dispatchEvent(new Event("change", { bubbles: true }));
          if (control.value !== value) throw new Error("changed");
        } else {
          const choice = matchingChoice(entry, instruction.value_kind, instruction.value);
          if (!choice) throw new Error("invalid");
          if (entry.kind === "select") {
            const control = entry.controls[0];
            changes.push({ control, property: "value", value: control.value });
            setNative(control, "value", choice.value);
            control.dispatchEvent(new Event("input", { bubbles: true }));
            control.dispatchEvent(new Event("change", { bubbles: true }));
            if (control.value !== choice.value) throw new Error("changed");
          } else {
            for (const control of entry.controls) {
              changes.push({ control, property: "checked", value: control.checked });
              setNative(control, "checked", control === choice);
            }
            choice.dispatchEvent(new Event("input", { bubbles: true }));
            choice.dispatchEvent(new Event("change", { bubbles: true }));
            if (!choice.checked) throw new Error("changed");
          }
        }
        filled.push(instruction.mapping_id);
      }
    } catch (error) {
      for (const change of changes.reverse()) {
        setNative(change.control, change.property, change.value);
        change.control.dispatchEvent(new Event("input", { bubbles: true }));
        change.control.dispatchEvent(new Event("change", { bubbles: true }));
      }
      return {
        ok: false,
        error_code: error.message === "invalid"
          ? "APPLICATION_ANSWER_SELECTION_INVALID"
          : "APPLICATION_ANSWER_FORM_CHANGED",
      };
    }
    return { ok: true, contract_version: CONTRACT_VERSION, filled_mapping_ids: filled };
  }

  if (!Number.isInteger(maxControls) || maxControls < 1 || maxControls > 240) {
    return { ok: false, error_code: "APPLICATION_ANSWER_REQUEST_INVALID" };
  }
  const built = buildEntries();
  if (built.error_code) return { ok: false, error_code: built.error_code };
  if (mode === "inspect_saved") {
    return inspectSaved(built.entries, payload, built.controls_examined);
  }
  if (mode === "find_completed") {
    return completedCandidates(built.entries, payload, built.controls_examined);
  }
  if (mode === "read_completed") return readCompleted(built.entries, payload);
  if (mode === "fill_saved") return fillSaved(built.entries, payload);
  return { ok: false, error_code: "APPLICATION_ANSWER_REQUEST_INVALID" };
}
