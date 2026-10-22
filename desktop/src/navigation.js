"use strict";

const LOOPBACK_HOST = "127.0.0.1";
const MAX_EXTERNAL_URL_LENGTH = 8_192;

function unbracketedHostname(hostname) {
  return hostname.startsWith("[") && hostname.endsWith("]")
    ? hostname.slice(1, -1)
    : hostname;
}

function isBlockedLocalHostname(hostname) {
  const normalized = unbracketedHostname(hostname.toLocaleLowerCase());
  if (
    normalized === "localhost" ||
    normalized.endsWith(".localhost") ||
    normalized === "0.0.0.0" ||
    normalized === "::" ||
    normalized === "::1" ||
    normalized.startsWith("127.")
  ) {
    return true;
  }

  const mappedIpv4 = /^::ffff:([0-9a-f]{1,4}):([0-9a-f]{1,4})$/.exec(
    normalized,
  );
  if (mappedIpv4 === null) return false;
  const address = (
    (Number.parseInt(mappedIpv4[1], 16) << 16) |
    Number.parseInt(mappedIpv4[2], 16)
  ) >>> 0;
  return address === 0 || address >>> 24 === 127;
}

function normalizeOwnedOrigin(candidate) {
  let target;
  try {
    target = new URL(candidate);
  } catch (_error) {
    return null;
  }
  if (
    target.protocol !== "http:" ||
    target.hostname !== LOOPBACK_HOST ||
    target.port === "" ||
    target.username !== "" ||
    target.password !== "" ||
    target.pathname !== "/" ||
    target.search !== "" ||
    target.hash !== ""
  ) {
    return null;
  }
  const port = Number(target.port);
  if (!Number.isInteger(port) || port < 1 || port > 65_535) return null;
  return target.origin;
}

function isOwnedAppUrl(candidate, ownedOrigin) {
  const normalizedOrigin = normalizeOwnedOrigin(ownedOrigin);
  if (normalizedOrigin === null) return false;
  try {
    const target = new URL(candidate);
    return (
      target.origin === normalizedOrigin &&
      target.protocol === "http:" &&
      target.username === "" &&
      target.password === ""
    );
  } catch (_error) {
    return false;
  }
}

function isAllowedPreviewWindowRequest(candidate, ownerUrl, ownedOrigin) {
  return candidate === "about:blank" && isOwnedAppUrl(ownerUrl, ownedOrigin);
}

function isAllowedPreviewNavigation(candidate, ownedOrigin) {
  if (candidate === "about:blank") return true;
  try {
    const target = new URL(candidate);
    return (
      target.protocol === "blob:" &&
      target.origin === normalizeOwnedOrigin(ownedOrigin)
    );
  } catch (_error) {
    return false;
  }
}

function isSafeExternalUrl(candidate) {
  if (
    typeof candidate !== "string" ||
    candidate.length === 0 ||
    candidate.length > MAX_EXTERNAL_URL_LENGTH ||
    /[\0\r\n]/.test(candidate)
  ) {
    return false;
  }
  let target;
  try {
    target = new URL(candidate);
  } catch (_error) {
    return false;
  }

  if (target.protocol === "http:" || target.protocol === "https:") {
    const hostname = target.hostname.toLocaleLowerCase();
    return Boolean(
      hostname &&
      target.username === "" &&
      target.password === "" &&
      !isBlockedLocalHostname(hostname),
    );
  }

  if (target.protocol === "mailto:") {
    if (target.search !== "" || target.hash !== "") return false;
    let address;
    try {
      address = decodeURIComponent(target.pathname);
    } catch (_error) {
      return false;
    }
    return (
      address.length <= 320 &&
      /^[^@\s<>]+@[^@\s<>]+$/.test(address)
    );
  }

  if (target.protocol === "tel:") {
    if (target.search !== "" || target.hash !== "") return false;
    let number;
    try {
      number = decodeURIComponent(target.pathname);
    } catch (_error) {
      return false;
    }
    return /^[+0-9(). -]{3,64}$/.test(number);
  }
  return false;
}

module.exports = {
  isAllowedPreviewNavigation,
  isAllowedPreviewWindowRequest,
  isOwnedAppUrl,
  isSafeExternalUrl,
  normalizeOwnedOrigin,
};
