"use strict";

function isTrustedUpdateSender({ event, isOwnedAppUrl, mainWindow, ownedOrigin }) {
  return Boolean(
    mainWindow &&
    typeof mainWindow.isDestroyed === "function" &&
    !mainWindow.isDestroyed() &&
    event &&
    event.sender === mainWindow.webContents &&
    event.senderFrame &&
    event.senderFrame === event.sender.mainFrame &&
    typeof isOwnedAppUrl === "function" &&
    isOwnedAppUrl(event.senderFrame.url, ownedOrigin),
  );
}

module.exports = { isTrustedUpdateSender };
