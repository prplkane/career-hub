// Since ADR SEC-250 the UI takes its capability token only from the desktop
// host's preload (window.careerHubDesktop.session); no HTTP route returns it.
// Tests stand in for the host the way Electron's preload does.
export function installDesktopSession(token) {
  const previous = globalThis.window;
  globalThis.window = {
    ...(previous || {}),
    careerHubDesktop: { ...(previous?.careerHubDesktop || {}), session: { token: async () => token } },
  };
  return () => { globalThis.window = previous; };
}
