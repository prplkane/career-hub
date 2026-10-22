const MONTH_NAMES = [
  "Jan", "Feb", "Mar", "Apr", "May", "Jun",
  "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
];
const FULL_MONTH_NAMES = [
  "January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December",
];
const CURRENT_MARKERS = new Set(["present", "current", "now", "ongoing"]);

function calendarMonth(year, month, day = "1") {
  const parsed = new Date(Date.UTC(Number(year), Number(month) - 1, Number(day)));
  if (
    parsed.getUTCFullYear() !== Number(year)
    || parsed.getUTCMonth() !== Number(month) - 1
    || parsed.getUTCDate() !== Number(day)
  ) return "";
  return String(year).padStart(4, "0") + "-" + String(month).padStart(2, "0");
}

function namedMonth(value) {
  const token = value.toLowerCase();
  const index = MONTH_NAMES.findIndex((name, itemIndex) => (
    name.toLowerCase() === token
    || FULL_MONTH_NAMES[itemIndex].toLowerCase() === token
  ));
  return index < 0 ? "" : String(index + 1);
}

function normalizeMonth(value) {
  if (typeof value !== "string") return "";
  const text = value.trim();
  if (/^(?:19|20)\d{2}-(?:0[1-9]|1[0-2])$/.test(text)) return text;
  const isoDate = /^(?<year>(?:19|20)\d{2})-(?<month>0[1-9]|1[0-2])-(?<day>0[1-9]|[12]\d|3[01])$/.exec(text);
  if (isoDate) return calendarMonth(isoDate.groups.year, isoDate.groups.month, isoDate.groups.day);
  const named = /^(?<month>[A-Za-z]+)\.?(?:\s+|[/-]\s*|,\s*)(?<year>(?:19|20)\d{2})$/.exec(text);
  if (named) {
    const month = namedMonth(named.groups.month);
    if (month) return calendarMonth(named.groups.year, month);
  }
  const numericMonthYear = /^(?<month>0?[1-9]|1[0-2])[./-](?<year>(?:19|20)\d{2})$/.exec(text);
  if (numericMonthYear) return calendarMonth(numericMonthYear.groups.year, numericMonthYear.groups.month);
  const dottedDate = /^(?<day>0?[1-9]|[12]\d|3[01])\.(?<month>0?[1-9]|1[0-2])\.(?<year>(?:19|20)\d{2})(?:[T\s].*)?$/.exec(text);
  if (dottedDate) return calendarMonth(dottedDate.groups.year, dottedDate.groups.month, dottedDate.groups.day);
  const slashDate = /^(?<month>0?[1-9]|1[0-2])\/(?<day>0?[1-9]|[12]\d|3[01])\/(?<year>(?:19|20)\d{2})(?:[T\s].*)?$/.exec(text);
  if (slashDate) return calendarMonth(slashDate.groups.year, slashDate.groups.month, slashDate.groups.day);
  const yearFirstDate = /^(?<year>(?:19|20)\d{2})[./](?<month>0?[1-9]|1[0-2])[./](?<day>0?[1-9]|[12]\d|3[01])(?:[T\s].*)?$/.exec(text);
  if (yearFirstDate) return calendarMonth(yearFirstDate.groups.year, yearFirstDate.groups.month, yearFirstDate.groups.day);
  const textMonthDay = /^(?<month>[A-Za-z]+)\.?\s+(?<day>\d{1,2}),?\s+(?<year>(?:19|20)\d{2})$/.exec(text);
  if (textMonthDay) {
    const month = namedMonth(textMonthDay.groups.month);
    if (month) return calendarMonth(textMonthDay.groups.year, month, textMonthDay.groups.day);
  }
  const dayTextMonth = /^(?<day>\d{1,2})\s+(?<month>[A-Za-z]+)\.?[,]?\s+(?<year>(?:19|20)\d{2})$/.exec(text);
  if (dayTextMonth) {
    const month = namedMonth(dayTextMonth.groups.month);
    if (month) return calendarMonth(dayTextMonth.groups.year, month, dayTextMonth.groups.day);
  }
  if (/^(?:19|20)\d{2}$/.test(text)) return text + "-01";
  const timestamp = /^(?<year>(?:19|20)\d{2})-(?<month>0[1-9]|1[0-2])-(?<day>0[1-9]|[12]\d|3[01])(?:T|\s)/.exec(text);
  return timestamp ? calendarMonth(timestamp.groups.year, timestamp.groups.month, timestamp.groups.day) : "";
}

function standaloneMonth(value) {
  const match = /^(?<month>0?[1-9]|1[0-2]|[A-Za-z]+)\.?$/.exec(value.trim());
  if (!match) return "";
  const token = match.groups.month;
  if (/^\d+$/.test(token)) return String(Number(token));
  return namedMonth(token);
}


function parseLegacyRange(value) {
  if (typeof value !== "string" || !value.trim()) return null;
  const parts = value.trim().split(/\s+(?:-|–|—|to)\s+|\s*(?:–|—)\s*/i, 2);
  let start = normalizeMonth(parts[0]);
  if (parts.length === 1) return start ? { start, end: null, ongoing: false } : null;
  const endLabel = parts[1].trim();
  if (CURRENT_MARKERS.has(endLabel.toLowerCase())) {
    return start ? { start, end: null, ongoing: true } : null;
  }
  let end = normalizeMonth(endLabel);
  const startToken = start ? "" : standaloneMonth(parts[0]);
  const endToken = end ? "" : standaloneMonth(endLabel);
  if (!start && startToken && end) {
    const [endYear, endMonth] = end.split("-").map(Number);
    const startYear = Number(startToken) <= endMonth ? endYear : endYear - 1;
    start = calendarMonth(startYear, startToken);
  } else if (!end && endToken && start) {
    const [startYear, startMonth] = start.split("-").map(Number);
    const endYear = Number(endToken) >= startMonth ? startYear : startYear + 1;
    end = calendarMonth(endYear, endToken);
  }
  return end && end >= start ? { start, end, ongoing: false } : null;
}

function formatMonth(value) {
  const normalized = normalizeMonth(value);
  if (!normalized) return "";
  const [year, month] = normalized.split("-");
  return `${MONTH_NAMES[Number(month) - 1]} ${year}`;
}

export function formatMonthRange(value) {
  if (!value?.start) return "";
  const start = formatMonth(value.start);
  if (value.ongoing) return `${start} - Present`;
  return value.end ? `${start} - ${formatMonth(value.end)}` : start;
}

function emitChange(root) {
  root.dispatchEvent(new Event("input", { bubbles: true }));
  root.dispatchEvent(new Event("change", { bubbles: true }));
}

/** Native month-calendar range with round-trip support for legacy strings. */
export function createMonthRangePicker(root, { label, required = false } = {}) {
  root.innerHTML = `<fieldset class="calendar-range-picker"><legend>${label || "Dates"}</legend>
    <label><span>Start month</span><input type="month" data-date-start${required ? " required" : ""}></label>
    <label><span>End month</span><input type="month" data-date-end></label>
    <label class="calendar-current"><input type="checkbox" data-date-ongoing> <span>Current / ongoing</span></label>
    <p class="calendar-range-note" data-date-note></p>
  </fieldset>`;
  const start = root.querySelector("[data-date-start]");
  const end = root.querySelector("[data-date-end]");
  const ongoing = root.querySelector("[data-date-ongoing]");
  const note = root.querySelector("[data-date-note]");
  let legacy = "";

  function sync({ notify = true } = {}) {
    end.disabled = ongoing.checked;
    if (ongoing.checked) end.value = "";
    if (start.value && end.value && end.value < start.value) end.setCustomValidity("End month cannot be earlier than start month.");
    else end.setCustomValidity("");
    note.textContent = start.value ? formatMonthRange(value().date_range) : legacy ? `Legacy value retained: ${legacy}` : "";
    if (notify) emitChange(root);
  }
  function value() {
    const date_range = start.value ? { start: start.value, end: ongoing.checked ? null : (end.value || null), ongoing: ongoing.checked } : null;
    return { dates: date_range ? formatMonthRange(date_range) : legacy, date_range };
  }
  function setValue({ dates = "", date_range = null } = {}) {
    const parsed = date_range?.start ? date_range : parseLegacyRange(dates);
    legacy = parsed ? "" : (dates || "");
    start.value = normalizeMonth(parsed?.start || "");
    end.value = normalizeMonth(parsed?.end || "");
    ongoing.checked = parsed?.ongoing === true;
    sync({ notify: false });
  }
  [start, end, ongoing].forEach((input) => input.addEventListener("input", () => sync()));
  ongoing.addEventListener("change", () => sync());
  return { setValue, value, focus: () => start.focus() };
}

/** One native month calendar for graduation and certification completion. */
export function createMonthPicker(root, { label, required = false } = {}) {
  root.innerHTML = `<label class="calendar-month-picker"><span>${label || "Month"}</span><input type="month" data-month-input${required ? " required" : ""}><small data-month-note></small></label>`;
  const input = root.querySelector("[data-month-input]");
  const note = root.querySelector("[data-month-note]");
  let legacy = "";
  function value() {
    return { display: input.value ? formatMonth(input.value) : legacy, month: input.value || null };
  }
  function setValue(display = "", month = "") {
    const normalized = normalizeMonth(month || display);
    input.value = normalized;
    legacy = normalized ? "" : (display || "");
    note.textContent = legacy ? `Legacy value retained: ${legacy}` : "";
  }
  input.addEventListener("input", () => {
    legacy = "";
    note.textContent = "";
    emitChange(root);
  });
  return { setValue, value, focus: () => input.focus() };
}
