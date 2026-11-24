(function initializeTheme() {
  "use strict";

  const storageKey = "careerHub.theme";
  const preferences = new Set(["system", "light", "dark"]);
  const colorSchemeQuery = window.matchMedia("(prefers-color-scheme: dark)");
  const desktopPreferences = window.careerHubDesktop?.preferences;

  // The desktop app saves UI choices in its own file, because this page's
  // origin and storage change at every launch. This script runs first, so copy
  // them into storage before anything reads it or paints. A plain browser has
  // no snapshot and keeps using its own storage.
  try {
    const saved = desktopPreferences?.snapshot;
    if (saved && typeof saved === "object") {
      for (const [key, value] of Object.entries(saved)) {
        if (key.startsWith("careerHub.") && typeof value === "string" && window.localStorage.getItem(key) !== value) {
          window.localStorage.setItem(key, value);
        }
      }
    }
  } catch (_error) {
    // Storage unavailable: the page starts from its defaults.
  }

  function readPreference() {
    try {
      const storedPreference = window.localStorage.getItem(storageKey);
      return preferences.has(storedPreference) ? storedPreference : "system";
    } catch (_error) {
      return "system";
    }
  }

  function resolveTheme(preference) {
    if (preference === "system") {
      return colorSchemeQuery.matches ? "dark" : "light";
    }
    return preference;
  }

  function applyTheme(preference) {
    const safePreference = preferences.has(preference) ? preference : "system";
    const resolvedTheme = resolveTheme(safePreference);
    document.documentElement.dataset.themePreference = safePreference;
    document.documentElement.dataset.theme = resolvedTheme;
    document.documentElement.style.colorScheme = resolvedTheme;
  }

  function storePreference(preference) {
    try {
      window.localStorage.setItem(storageKey, preference);
    } catch (_error) {
      // Theme selection still works for this page when local storage is unavailable.
    }
    try {
      desktopPreferences?.set?.(storageKey, preference)?.catch?.(() => {});
    } catch (_error) {
      // Without the desktop file the choice lasts for this launch only.
    }
  }

  applyTheme(readPreference());

  document.addEventListener("DOMContentLoaded", () => {
    const preferenceSelect = document.getElementById("theme-preference");
    if (!preferenceSelect) {
      return;
    }
    preferenceSelect.value = document.documentElement.dataset.themePreference || "system";
    preferenceSelect.addEventListener("change", () => {
      const preference = preferences.has(preferenceSelect.value)
        ? preferenceSelect.value
        : "system";
      storePreference(preference);
      applyTheme(preference);
    });
  });

  colorSchemeQuery.addEventListener("change", () => {
    if (document.documentElement.dataset.themePreference === "system") {
      applyTheme("system");
    }
  });
})();
