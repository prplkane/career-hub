/** Reusable, framework-free UI primitives used by the local browser app. */

const HTML_ESCAPE_CHARACTERS = Object.freeze({
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&quot;",
  "'": "&#039;",
});

export function escapeHtml(value) {
  return String(value ?? "").replace(
    /[&<>"']/g,
    (character) => HTML_ESCAPE_CHARACTERS[character],
  );
}

export function formatBytes(value) {
  if (!Number.isFinite(value)) return "-";
  if (value < 1024) return `${value} B`;
  return `${(value / 1024).toFixed(1)} KB`;
}

export function formatTime(value) {
  if (!value) return "-";
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

export function statusTone(status) {
  const normalized = String(status || "").toLocaleLowerCase();
  if (["error", "invalid", "incompatible", "rejected", "withdrawn", "closed"].includes(normalized)) {
    return "danger";
  }
  if (["warning", "success_with_warnings", "draft", "preparing", "manual_review", "review_recommended"].includes(normalized)) {
    return "warning";
  }
  if (["info", "not_stated", "unclear"].includes(normalized)) return "info";
  return "success";
}

export function statusClass(status) {
  const tone = statusTone(status);
  if (tone === "danger") return "status-error";
  if (tone === "warning") return "status-warning";
  return "status-success";
}

export function formatRequestError(error) {
  const payload = error?.payload || {};
  const parts = [];
  if (payload.error_code) parts.push(`${payload.error_code}:`);
  parts.push(payload.summary || payload.error || error?.message || "Request failed.");
  if (payload.technical_message && payload.technical_message !== payload.error) {
    parts.push(`Details: ${payload.technical_message}`);
  }
  if (payload.suggestion) parts.push(`Next: ${payload.suggestion}`);
  if (payload.run_id) parts.push(`Run: ${payload.run_id}`);
  return parts.filter(Boolean).join(" ");
}

export function renderStatusBadge({ label, tone, title = "", className = "" } = {}) {
  const resolvedTone = tone || statusTone(label);
  const titleAttribute = title ? ` title="${escapeHtml(title)}"` : "";
  return `<span class="status-badge status-badge--${escapeHtml(resolvedTone)} ${escapeHtml(className)}"${titleAttribute}>${escapeHtml(label || "Not stated")}</span>`;
}

export function renderEmptyState({ title = "Nothing here yet", message = "", action = null, compact = false } = {}) {
  const actionHtml = action
    ? `<button type="button" class="secondary-button compact-button" ${renderAttributes(action.attributes || {})}>${escapeHtml(action.label || "Continue")}</button>`
    : "";
  return `<div class="empty-state-card${compact ? " empty-state-card--compact" : ""}">
    <strong>${escapeHtml(title)}</strong>
    ${message ? `<span>${escapeHtml(message)}</span>` : ""}
    ${actionHtml}
  </div>`;
}

/** Reusable progress placeholder with a small, old-desktop-style hourglass. */
export function renderLoadingState({ label = "Loading…", compact = false } = {}) {
  return `<div class="hourglass-loader${compact ? " hourglass-loader--compact" : ""}" role="status">
    <span class="hourglass-loader__icon" aria-hidden="true"><i></i></span>
    <span class="hourglass-loader__label">${escapeHtml(label)}</span>
  </div>`;
}

function renderAttributes(attributes) {
  return Object.entries(attributes)
    .filter(([, value]) => value !== false && value !== null && value !== undefined)
    .map(([name, value]) => value === true
      ? ` ${escapeHtml(name)}`
      : ` ${escapeHtml(name)}="${escapeHtml(value)}"`)
    .join("");
}

export function renderActionBar({ label = "Actions", summary = "", actions = [], className = "" } = {}) {
  const actionsHtml = actions.map((action) => {
    const classes = [action.variant === "primary" ? "primary-button" : "secondary-button", action.className]
      .filter(Boolean)
      .join(" ");
    return `<button type="button" class="${escapeHtml(classes)}"${renderAttributes({
      ...action.attributes,
      disabled: action.disabled,
    })}>${escapeHtml(action.label)}</button>`;
  }).join("");
  return `<section class="action-bar ${escapeHtml(className)}" aria-label="${escapeHtml(label)}">
    <div class="action-bar__summary">${summary}</div>
    <div class="action-bar__actions" role="group" aria-label="${escapeHtml(label)}">${actionsHtml}</div>
  </section>`;
}

export function createToastManager(root) {
  let dismissTimer = null;

  function dismiss() {
    if (dismissTimer !== null) window.clearTimeout(dismissTimer);
    dismissTimer = null;
    root?.replaceChildren();
  }

  function show({ message, tone = "success", timeout = 5000 } = {}) {
    if (!root || !message) return;
    dismiss();
    const toast = document.createElement("div");
    toast.className = `toast toast--${tone}`;
    toast.setAttribute("role", tone === "danger" ? "alert" : "status");

    const messageElement = document.createElement("span");
    messageElement.textContent = message;
    toast.append(messageElement);

    const closeButton = document.createElement("button");
    closeButton.type = "button";
    closeButton.className = "icon-button toast__close";
    closeButton.setAttribute("aria-label", "Dismiss notification");
    closeButton.textContent = "\u00D7";
    closeButton.addEventListener("click", dismiss);
    toast.append(closeButton);
    root.append(toast);

    if (timeout > 0) dismissTimer = window.setTimeout(dismiss, timeout);
  }

  return { show, dismiss };
}

/**
 * Accessible searchable single-select combobox. It intentionally keeps focus
 * on the input and uses aria-activedescendant for keyboard navigation.
 */
export function createSearchableCombobox(root, {
  inputId,
  labelledBy,
  placeholder = "Search",
  emptyMessage = "No matching options.",
  onChange = () => {},
} = {}) {
  if (!root) throw new Error("A combobox root element is required.");
  const listboxId = `${inputId}-listbox`;
  const state = {
    disabled: false,
    filtering: false,
    highlightedIndex: -1,
    open: false,
    options: [],
    value: "",
  };

  root.classList.add("searchable-combobox");
  root.replaceChildren();
  const control = document.createElement("div");
  control.className = "searchable-combobox__control";
  const input = document.createElement("input");
  input.id = inputId;
  input.type = "search";
  input.autocomplete = "off";
  input.placeholder = placeholder;
  input.setAttribute("role", "combobox");
  input.setAttribute("aria-autocomplete", "list");
  input.setAttribute("aria-controls", listboxId);
  input.setAttribute("aria-expanded", "false");
  if (labelledBy) input.setAttribute("aria-labelledby", labelledBy);
  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "searchable-combobox__toggle";
  toggle.setAttribute("aria-label", "Show options");
  toggle.textContent = "\u2304";
  control.append(input, toggle);
  const listbox = document.createElement("ul");
  listbox.id = listboxId;
  listbox.className = "searchable-combobox__listbox";
  listbox.setAttribute("role", "listbox");
  listbox.hidden = true;
  root.append(control, listbox);

  function selectedOption() {
    return state.options.find((option) => option.value === state.value) || null;
  }

  function visibleOptions() {
    if (!state.open) return [];
    const term = state.filtering ? input.value.trim().toLocaleLowerCase() : "";
    if (!term) return state.options;
    return state.options.filter((option) => option.label.toLocaleLowerCase().includes(term));
  }

  function syncInputToSelection() {
    input.value = selectedOption()?.label || "";
    state.filtering = false;
  }

  function renderOptions() {
    const visible = visibleOptions();
    listbox.replaceChildren();
    listbox.hidden = !state.open;
    input.setAttribute("aria-expanded", String(state.open));
    toggle.setAttribute("aria-expanded", String(state.open));
    if (!state.open) {
      input.removeAttribute("aria-activedescendant");
      return;
    }
    if (!visible.length) {
      const empty = document.createElement("li");
      empty.className = "searchable-combobox__empty";
      empty.textContent = emptyMessage;
      empty.setAttribute("role", "presentation");
      listbox.append(empty);
      input.removeAttribute("aria-activedescendant");
      return;
    }
    if (state.highlightedIndex < 0 || state.highlightedIndex >= visible.length) {
      state.highlightedIndex = Math.max(0, visible.findIndex((option) => option.value === state.value));
    }
    visible.forEach((option, index) => {
      const item = document.createElement("li");
      item.id = `${listboxId}-option-${index}`;
      item.className = "searchable-combobox__option";
      if (index === state.highlightedIndex) item.classList.add("is-active");
      item.setAttribute("role", "option");
      item.setAttribute("aria-selected", String(option.value === state.value));
      item.textContent = option.label;
      item.addEventListener("pointerdown", (event) => event.preventDefault());
      item.addEventListener("click", () => commit(option));
      listbox.append(item);
    });
    input.setAttribute("aria-activedescendant", `${listboxId}-option-${state.highlightedIndex}`);
  }

  function open({ preserveFilter = false } = {}) {
    if (state.disabled) return;
    state.open = true;
    if (!preserveFilter) state.filtering = false;
    state.highlightedIndex = -1;
    renderOptions();
  }

  function close({ restoreSelection = false } = {}) {
    state.open = false;
    state.highlightedIndex = -1;
    if (restoreSelection) syncInputToSelection();
    renderOptions();
  }

  function commit(option) {
    if (!option) return;
    const changed = state.value !== option.value;
    state.value = option.value;
    syncInputToSelection();
    close();
    if (changed) onChange(option.value, option);
  }

  input.addEventListener("focus", () => open());
  input.addEventListener("input", () => {
    state.filtering = true;
    open({ preserveFilter: true });
    state.highlightedIndex = 0;
    renderOptions();
  });
  input.addEventListener("keydown", (event) => {
    const visible = visibleOptions();
    if (event.key === "ArrowDown") {
      event.preventDefault();
      if (!state.open) open();
      else {
        state.highlightedIndex = Math.min(state.highlightedIndex + 1, Math.max(visible.length - 1, 0));
        renderOptions();
      }
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      if (!state.open) open();
      else {
        state.highlightedIndex = Math.max(state.highlightedIndex - 1, 0);
        renderOptions();
      }
    } else if (event.key === "Home" && state.open) {
      event.preventDefault();
      state.highlightedIndex = 0;
      renderOptions();
    } else if (event.key === "End" && state.open) {
      event.preventDefault();
      state.highlightedIndex = Math.max(visible.length - 1, 0);
      renderOptions();
    } else if (event.key === "Enter" && state.open) {
      event.preventDefault();
      commit(visible[state.highlightedIndex]);
    } else if (event.key === "Escape" && state.open) {
      event.preventDefault();
      event.stopPropagation();
      close({ restoreSelection: true });
    } else if (event.key === "Tab") {
      close({ restoreSelection: true });
    }
  });
  toggle.addEventListener("click", () => {
    if (state.open) close({ restoreSelection: true });
    else {
      input.focus();
      open();
    }
  });
  document.addEventListener("pointerdown", (event) => {
    if (!root.contains(event.target)) close({ restoreSelection: true });
  });

  return {
    input,
    setDisabled(disabled) {
      state.disabled = Boolean(disabled);
      input.disabled = state.disabled;
      toggle.disabled = state.disabled;
      if (state.disabled) close({ restoreSelection: true });
    },
    setOptions(options) {
      state.options = (options || []).map((option) => ({
        value: String(option.value),
        label: String(option.label),
      }));
      if (!selectedOption()) state.value = "";
      if (!state.open) syncInputToSelection();
      renderOptions();
    },
    setValue(value) {
      state.value = String(value || "");
      syncInputToSelection();
      renderOptions();
    },
    value() {
      return state.value;
    },
  };
}
