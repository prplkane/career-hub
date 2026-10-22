(function initializeTheme() {
  "use strict";

  const storageKey = "careerHub.theme";
  const preferences = new Set(["system", "light", "dark"]);
  const colorSchemeQuery = window.matchMedia("(prefers-color-scheme: dark)");

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
