"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { isTrustedUpdateSender } = require("../src/update_ipc");

function fixture() {
  const mainFrame = { url: "http://127.0.0.1:8766/#profile" };
  const webContents = { mainFrame };
  const mainWindow = {
    isDestroyed: () => false,
    webContents,
  };
  return {
    event: { sender: webContents, senderFrame: mainFrame },
    isOwnedAppUrl: (url, origin) => url.startsWith(`${origin}/`),
    mainWindow,
    ownedOrigin: "http://127.0.0.1:8766",
  };
}

test("update IPC accepts only the owned Career Hub main frame", () => {
  const valid = fixture();
  assert.equal(isTrustedUpdateSender(valid), true);
  assert.equal(
    isTrustedUpdateSender({ ...valid, ownedOrigin: "http://127.0.0.1:9000" }),
    false,
  );
  assert.equal(
    isTrustedUpdateSender({
      ...valid,
      event: { ...valid.event, senderFrame: { url: valid.event.senderFrame.url } },
    }),
    false,
  );
  assert.equal(
    isTrustedUpdateSender({
      ...valid,
      mainWindow: { ...valid.mainWindow, isDestroyed: () => true },
    }),
    false,
  );
});
