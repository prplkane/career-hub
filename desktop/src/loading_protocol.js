"use strict";

const fs = require("node:fs/promises");
const path = require("node:path");

const LOADING_ORIGIN = "career-hub://app";
const LOADING_SCHEME = Object.freeze({
  scheme: "career-hub",
  privileges: Object.freeze({
    secure: true,
    standard: true,
  }),
});
const LOADING_STATES = new Set(["backup", "restore", "error", "ready", "starting"]);
const ERROR_CODE_PATTERN = /^[A-Z][A-Z0-9_]{0,127}$/;
const LOADING_CSP = [
  "default-src 'none'",
  "script-src 'self'",
  "style-src 'self'",
  "base-uri 'none'",
  "form-action 'none'",
  "frame-ancestors 'none'",
].join("; ");
const ASSETS = Object.freeze({
  "/loading.css": Object.freeze({
    contentType: "text/css; charset=utf-8",
    filename: "loading.css",
  }),
  "/loading.html": Object.freeze({
    contentType: "text/html; charset=utf-8",
    filename: "loading.html",
  }),
  "/loading.js": Object.freeze({
    contentType: "text/javascript; charset=utf-8",
    filename: "loading.js",
  }),
});

function loadingUrl(state, errorCode = "") {
  if (!LOADING_STATES.has(state)) {
    throw new TypeError("Loading state is invalid");
  }
  if (state === "error") {
    if (typeof errorCode !== "string" || !ERROR_CODE_PATTERN.test(errorCode)) {
      throw new TypeError("Loading error code is invalid");
    }
  } else if (errorCode !== "") {
    throw new TypeError("Loading error code is only valid for the error state");
  }

  const target = new URL("/loading.html", LOADING_ORIGIN);
  target.searchParams.set("state", state);
  if (errorCode) target.searchParams.set("error_code", errorCode);
  return target.toString();
}

function parseTrustedLoadingUrl(candidate) {
  let target;
  try {
    target = new URL(candidate);
  } catch (_error) {
    return null;
  }
  if (
    target.protocol !== "career-hub:" ||
    target.hostname !== "app" ||
    target.port !== "" ||
    target.username !== "" ||
    target.password !== "" ||
    target.pathname !== "/loading.html" ||
    target.hash !== ""
  ) {
    return null;
  }

  const keys = [...target.searchParams.keys()];
  const allowedKeys = new Set(["error_code", "state"]);
  if (
    keys.some((key) => !allowedKeys.has(key)) ||
    target.searchParams.getAll("state").length !== 1 ||
    target.searchParams.getAll("error_code").length > 1
  ) {
    return null;
  }
  const state = target.searchParams.get("state");
  const errorCode = target.searchParams.get("error_code") || "";
  if (!LOADING_STATES.has(state)) return null;
  if (state === "error") {
    if (!ERROR_CODE_PATTERN.test(errorCode)) return null;
  } else if (errorCode !== "") {
    return null;
  }
  return Object.freeze({ errorCode, state });
}

function isTrustedLoadingUrl(candidate) {
  return parseTrustedLoadingUrl(candidate) !== null;
}

function response(status, body = null, headers = {}) {
  return new Response(body, {
    headers: {
      "Cache-Control": "no-store",
      "X-Content-Type-Options": "nosniff",
      ...headers,
    },
    status,
  });
}

function createLoadingProtocolHandler(loadingRoot, readFile = fs.readFile) {
  if (typeof loadingRoot !== "string" || !path.isAbsolute(loadingRoot)) {
    throw new TypeError("Loading asset root must be absolute");
  }
  return async (request) => {
    if (!request || (request.method !== "GET" && request.method !== "HEAD")) {
      return response(405, null, { Allow: "GET, HEAD" });
    }

    let target;
    try {
      target = new URL(request.url);
    } catch (_error) {
      return response(400);
    }
    const asset = ASSETS[target.pathname];
    const isLoadingDocument = target.pathname === "/loading.html";
    if (
      target.protocol !== "career-hub:" ||
      target.hostname !== "app" ||
      target.port !== "" ||
      target.username !== "" ||
      target.password !== "" ||
      target.hash !== "" ||
      !asset ||
      (isLoadingDocument
        ? !isTrustedLoadingUrl(target.toString())
        : target.search !== "")
    ) {
      return response(404);
    }

    try {
      const body = await readFile(path.join(loadingRoot, asset.filename));
      const headers = {
        "Content-Length": String(body.byteLength),
        "Content-Type": asset.contentType,
      };
      if (isLoadingDocument) {
        headers["Content-Security-Policy"] = LOADING_CSP;
      }
      return response(200, request.method === "HEAD" ? null : body, headers);
    } catch (_error) {
      return response(500);
    }
  };
}

function registerLoadingProtocol(electronProtocol, loadingRoot) {
  if (!electronProtocol || typeof electronProtocol.handle !== "function") {
    throw new TypeError("Electron protocol.handle API is required");
  }
  electronProtocol.handle(
    LOADING_SCHEME.scheme,
    createLoadingProtocolHandler(loadingRoot),
  );
}

module.exports = {
  LOADING_ORIGIN,
  LOADING_SCHEME,
  LOADING_CSP,
  createLoadingProtocolHandler,
  isTrustedLoadingUrl,
  loadingUrl,
  parseTrustedLoadingUrl,
  registerLoadingProtocol,
};
