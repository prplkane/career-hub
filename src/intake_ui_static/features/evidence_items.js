export function normalizeEvidenceItem(value) {
  return String(value || "").replace(/\s+/g, " ").trim();
}

export function normalizedEvidenceItems(values) {
  const seen = new Set();
  return (values || []).reduce((items, value) => {
    const normalized = normalizeEvidenceItem(value);
    const key = normalized.toLocaleLowerCase();
    if (normalized && !seen.has(key)) {
      seen.add(key);
      items.push(normalized);
    }
    return items;
  }, []);
}

export function evidenceItemsFromInput(value) {
  return String(value || "")
    .split(/\r?\n/)
    .map(normalizeEvidenceItem)
    .filter(Boolean);
}

export function moveEvidenceItem(values, index, direction) {
  const items = [...(values || [])];
  const targetIndex = direction === "up" ? index - 1 : index + 1;
  if (!Number.isInteger(index) || index < 0 || targetIndex < 0 || targetIndex >= items.length) {
    return items;
  }
  [items[index], items[targetIndex]] = [items[targetIndex], items[index]];
  return items;
}

export function removeEvidenceItem(values, index) {
  const items = [...(values || [])];
  if (!Number.isInteger(index) || index < 0 || index >= items.length) return items;
  items.splice(index, 1);
  return items;
}
