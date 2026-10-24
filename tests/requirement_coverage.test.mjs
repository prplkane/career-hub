import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import vm from "node:vm";

const source = await readFile(new URL("../src/intake_ui_static/features/jobs.js", import.meta.url), "utf8");
const start = source.indexOf("function requirementConclusions(");
const end = source.indexOf("\nexport function renderAssessmentSummary(", start);
const conclusions = vm.runInNewContext(source.slice(start, end) + "\nrequirementConclusions", {
  conclusionHtml: (item) => item,
  requirementEvidence: () => [],
  sourceEvidence: (items) => items || [],
});

test("partial requirement coverage remains a warning with the exact unparsed heading", () => {
  const heading = { line: 3, text: "Must haves:", rule: "unrecognized_heading" };
  const assessment = { requirement_gaps: [{ category: "coverage", status: "requirement_basis_incomplete", message: "Some sections were not recognized." }] };
  const job = { digest: { analysis_coverage: { no_requirement_sections: false, unrecognized_headings: [heading] } } };
  for (const compact of [false, true]) {
    const [item] = conclusions(assessment, job, compact, new Map());
    assert.equal(item.status, "Incomplete requirement basis");
    assert.equal(item.tone, "warning");
    assert.equal(item.detail, assessment.requirement_gaps[0].message);
    assert.deepEqual(item.evidence, [heading]);
  }
});

test("missing requirement basis stays distinct from partial and complete coverage", () => {
  const [missing] = conclusions({ requirement_gaps: [{ category: "coverage", status: "requirement_basis_missing" }] }, {}, false, new Map());
  assert.equal(missing.status, "No requirement basis");
  const [complete] = conclusions({ requirement_gaps: [] }, {}, false, new Map());
  assert.equal(complete.status, "No open gap");
  assert.equal(complete.tone, "success");
});
