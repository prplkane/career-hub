import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

const source = await readFile(new URL("../src/intake_ui_static/locale.js", import.meta.url), "utf8");

function launch(languages, snapshot = {}, stored = null, getSystemLanguage) {
  const window = {
    addEventListener() {},
    localStorage: { getItem: () => stored },
    careerHubDesktop: { preferences: { snapshot, getSystemLanguage } },
  };
  const document = { documentElement: { dataset: {} }, addEventListener() {} };
  vm.runInNewContext(source, { window, document, navigator: { language: languages[0], languages } });
  return window.CareerHubLocale;
}

test("System follows the primary language and defaults to English for unsupported languages", () => {
  assert.equal(launch(["en-US", "ru"]).locale, "en");
  assert.equal(launch(["ru-KG", "en"]).locale, "ru");
  assert.equal(launch(["ky-KG", "ru"]).locale, "en");
  assert.equal(launch(["en"], { systemLanguage: "ru-RU" }).locale, "ru");
  assert.equal(launch(["ru"], { systemLanguage: "en-GB" }).locale, "en");
});

test("the saved desktop choice takes precedence across new origins, including a return to System", () => {
  assert.equal(launch(["ru"], { systemLanguage: "ru-RU", "careerHub.locale": "en" }).locale, "en");
  assert.equal(launch(["en"], { systemLanguage: "en-US", "careerHub.locale": "ru" }).locale, "ru");
  assert.equal(launch(["ru"], { systemLanguage: "en-US", "careerHub.locale": "system" }, "ru").locale, "en");
  assert.equal(launch(["en"], {}, "ru").locale, "ru");
});

test("returning to System re-reads the current OS setting", () => {
  const interfaceLanguage = launch(["en"], { systemLanguage: "en-US" }, null, () => "ru-RU");
  assert.equal(interfaceLanguage.locale, "en");
  interfaceLanguage.applyPreference("en");
  assert.equal(interfaceLanguage.locale, "en");
  interfaceLanguage.applyPreference("system");
  assert.equal(interfaceLanguage.locale, "ru");
});
