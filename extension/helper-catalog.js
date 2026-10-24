"use strict";

/**
 * The application-helper field catalog shared by the service worker
 * (importScripts) and the side panel (script tag).
 *
 * Each row is [id, label, source, value_kind, reviewed_fill]. Rows with
 * reviewed_fill false are one-field Copy only. The injected page helper in
 * application-form-helper.js keeps its own copy of the ids and kinds because
 * the WebExtensions scripting API serializes that function body without this
 * file's scope; the Go backend owns the canonical list in
 * backend/internal/applications/applicationhelper/catalog.go.
 */
((scope) => {
  const rows = Object.freeze([
    ["full_name", "Full name", "Profile · Name", "text", true],
    ["given_name", "Given name", "Profile · Given name", "text", true],
    ["family_name", "Family name", "Profile · Family name", "text", true],
    ["preferred_name", "Preferred name", "Profile · Preferred name", "text", true],
    ["email", "Email", "Profile · Contact · Email", "text", true],
    ["phone", "Phone", "Profile · Contact · Phone", "text", true],
    ["address", "Full address", "Profile · Contact · Full address", "text", true],
    ["address_line1", "Address line 1", "Profile · Contact · Address line 1", "text", true],
    ["address_line2", "Address line 2", "Profile · Contact · Address line 2", "text", true],
    ["city", "City", "Profile · Contact · City", "text", true],
    ["region", "State / province / region", "Profile · Contact · Region", "text", true],
    ["postal_code", "Postal code", "Profile · Contact · Postal code", "text", true],
    ["country", "Country", "Profile · Contact · Country", "text", true],
    ["current_location", "Current location", "Profile · Current location", "text", true],
    ["linkedin", "LinkedIn", "Profile · Contact · LinkedIn", "text", true],
    ["github", "GitHub", "Profile · Contact · GitHub", "text", true],
    ["website", "Website / portfolio", "Profile · Contact · Website", "text", true],
    ["major", "Major", "Profile · Major", "text", true],
    ["current_status", "Current status", "Eligibility · Current status", "text", true],
    ["work_authorization", "Work authorization statement", "Eligibility · Authorization statement", "text", true],
    ["requires_sponsorship_now", "Sponsorship needed now", "Eligibility · Sponsorship needed now", "boolean", true],
    ["requires_future_sponsorship", "Future sponsorship needed", "Eligibility · Future sponsorship needed", "boolean", true],
    ["stem_opt_eligible", "STEM OPT eligible", "Eligibility · STEM OPT eligible", "boolean", true],
    ["us_gender_identity", "US gender identity", "US · Gender", "text", false],
    ["us_eeo_gender", "US EEO gender", "US · Gender", "text", false],
    ["us_hispanic_latino", "US Hispanic or Latino", "US · Hispanic or Latino", "text", false],
    ["us_veteran_status", "US veteran status", "US · Veteran status", "text", false],
    ["us_disability_status", "US disability status", "US · Disability status", "text", false],
  ]);
  const catalog = new Map(rows.map(([id, label, source, kind]) => [id, Object.freeze([label, source, kind])]));
  const prefillFieldIds = new Set(rows.filter((row) => row[4]).map((row) => row[0]));
  scope.CareerHubHelperCatalog = Object.freeze({ catalog, prefillFieldIds });
})(globalThis);
