// Saves a UI choice to the desktop's preference file as well as browser
// storage, because the page's origin (and its storage) changes at every
// launch. theme.js copies the saved choices back in before first paint.
// In a plain browser there is no desktop bridge and this does nothing.
export function saveDesktopPreference(key, value) {
  try {
    window.careerHubDesktop?.preferences?.set?.(key, value)?.catch?.(() => {});
  } catch {
    // The choice still lasts for this launch through browser storage.
  }
}
