# Career Hub testing strategy

Reviewed against primary documentation on **2026-10-13**. This is a project
testing policy, not a claim that the industry has one mandatory October 2026
test distribution. Its purpose is to catch mistakes cheaply and explain which
part of a user workflow failed.

## What current guidance agrees on

| Practice | Application here |
| --- | --- |
| Test behavior and observable outcomes | Fill a form, save it, reload it, and check the saved value. Prefer roles/labels to incidental CSS classes. |
| Isolate test state | Each browser scenario gets its own synthetic Data Home, native process and browser context. |
| Wait for conditions | Wait for the expected response or visible state, rather than sleeping for a guessed duration. |
| Control dependencies | Use the real local backend; inject one specific failed response when testing UI recovery. Keep third-party providers outside routine tests. |
| Match the test level to the risk | Test validation rules cheaply; exercise persistence with the real storage owner; keep a small browser suite for wiring and user-visible outcomes. |

These practices follow [Playwright's best practices](https://playwright.dev/docs/best-practices)
and [Testing Library's guiding principles](https://testing-library.com/docs/guiding-principles/).
Testing Library is a source of guidance here; this change does not install it.
The [practical test pyramid](https://martinfowler.com/articles/practical-test-pyramid.html)
explains the cost of different levels and the value of smaller tests. Use that
cost model without imposing a universal ratio of unit to integration tests.

Other useful techniques are available when their risk justifies them:

- Property tests and fuzzing: check invariants across many inputs, especially
  parsers, codecs and source spans. Go provides [native fuzzing](https://go.dev/doc/tutorial/fuzz).
- Contract tests: check request/response shapes, revisions and error codes across
  the browser, extension, agent and backend boundaries.
- Visual and accessibility checks: check layout, contrast, keyboard operation,
  focus and names. A successful click alone does not prove accessibility.
- Mutation testing: deliberately change a rule and see whether the tests catch
  it. Evaluate this later for selected important modules; it is not a daily gate.

## Where this repository starts

At the starting commit `6ed08e8`, `backend/` contains 656 Go test files and
`tests/` contains 53 top-level files. Counts are an inventory, **not coverage**.
The backend already has substantial contract, recovery and lifetime testing;
the desktop also has its own Node tests.

The original CI ran full Go and Python development-tool tests, desktop tests,
UI state tests and native browser smokes on each PR update. Several other rendered
scripts explicitly require the pinned retired Python reference export. Their
presence must not be counted as current Go browser coverage. Prioritize
porting valuable scenarios, not removing useful historical evidence blindly.

The existing UI CI command selected only `*.test.mjs`, omitting three existing
`*.test.js` files. `npm run test:fast` includes `.mjs`, `.js`, `.cjs` and the new
failure-journal checks. The CI UI step now uses this command.

## Where checks run

Development tests and ordinary app safeguards execute at different times:

| Kind | Runs where/when | Example and result |
| --- | --- | --- |
| Focused code test | Test runner, after a test command | Simulate an aborted request; assert client state is released; print pass/fail. |
| Integration test | Test runner with isolated backend/storage fixtures | Exercise a write and read; assert saved data and contract outcomes. |
| Browser workflow test | Test runner controlling Chromium and a synthetic Go Home | Fill Profile, save/reload or inject a failed response; assert visible state and persistence. |
| Startup check | Normal application launch | Verify packaged resources and native-service readiness; return a startup result or error. |
| Validation/error handling | Normal app use, when an operation occurs | Reject invalid input or display a save failure while keeping the form available. |

Startup checks and error handling are application code. The development tests
verify that code separately; they do not run on each launch or Save click.
Test output is for developers/agents; runtime errors are shown by the app to
the user. A browser test can deliberately cause a save failure to verify that
the app's error handling works.

The agent invokes its selected commands before reporting a behavior change
complete, without needing an extra user prompt. A user can request testing
earlier. CI runs on pull requests and pushes to main. The optional pre-push hook
runs local checks before uploading commits; it never runs on app startup or
ordinary commits. Setup is in [local development](../guides/local_development.md#local-checks-before-push).

## Local and GitHub responsibilities

The decision on 2026-10-14 moved the full Windows suite out of routine CI.
The agreed addition on 2026-10-20 runs portable Go checks on Linux for backend
and shared changes. Native integration and the full Windows Go suite remain
local or explicitly dispatched. Existing checks are retained across these triggers.

| Location/trigger | Required work |
| --- | --- |
| Local, while editing | Agent-selected regression and owning tests; affected user flow before declaring completion. |
| Local, pre-push hook | Privacy scan of outgoing commit snapshots, Gitleaks on outgoing history, then path-selected deterministic checks. |
| GitHub, every PR/main update | Public-tree privacy gate, Gitleaks and Trivy dependency/configuration/secret checks. Documentation still gets these. |
| GitHub, code changes | Fast Python development-tool/repository tests and Node UI/harness tests on Linux. |
| GitHub, backend/shared changes | Go formatting, `go vet` for Linux and Windows, reachable-vulnerability scan and the portable Go suite on Linux. |
| GitHub, application/shared/build changes | Windows desktop tests, configuration check, native build and the existing empty-profile startup smoke. |
| Major/sensitive update or release | Applicable local broad/package qualification; manually dispatch `Full qualification` when hosted confirmation is required. |

Linux here is a GitHub-hosted machine that runs the Go tests eligible for Linux.
The Windows `go vet` pass checks Windows code without executing Windows tests.
Routine Windows CI retains the build and startup smoke. Full Windows Go tests,
native supervisor integration and detailed browser/recovery checks run locally
or in the manually dispatched Windows workflow.
Trivy remains automatic, so dependency security does not rely only on the hook.
The opt-in native supervisor integration cases run after a fresh sidecar build
and startup-fixture compilation, serially, through
`npm run check:local -- --profile native`. Broad local checks, affected backend
or desktop selections and manual hosted qualification include this step.
Routine CI and quick local desktop tests report these native cases as skipped;
those skips are not integration coverage. Qualification overrides inherited
binary selectors and supplies the exact built executable's SHA-256.

Local and manual full Go runs limit package concurrency to two (`go test -p 2`);
the portable Linux CI job keeps Go's default package concurrency.
Several packages start native children and large synthetic filesystem fixtures;
running all packages at the machine's CPU count can compete for those resources.
This bounds resource use while preserving assertions and per-test deadlines.
A passing run at this setting does not establish the cause of an earlier timeout.
Known vulnerabilities, deprecated dependencies and broken builds are different
signals: scanner coverage is not proof that every dependency is maintained.
Review installation deprecation warnings and Dependabot updates separately.

The selector is deliberately small: documentation uses a narrow Markdown
allowlist, UI selects its workflows, backend includes its consumers, and shared
dependencies/build/CI/unknown paths select all local groups. Python tooling has
its own portable group. This is a minimum, not proof of dependency coverage;
agents must add a missing affected-flow check. Small sensitive changes still
require Broad qualification regardless of file count.

CI uses job conditions, preserving the existing required-check name, rather
than skipping the entire workflow. PR selection uses the whole PR merge diff:
a documentation follow-up on a PR containing code still selects code checks.
Local selection considers every outgoing commit, including reverted changes.
There is no verification cache yet, so the hook reruns its selected checks even
after a manual pass. Avoid needless pushes; measure before adding cache complexity.

Hooks are local and bypassable. A green routine CI run proves only its smaller
set of checks; it cannot prove that someone ran the local full suite. Record
local receipts in the PR and require broader qualification by risk before merge.
Installed package/real-account qualification remains explicit, outside the hook.

## Three test tiers

Coding agents must select and record checks through
`AGENTS.md` and the operational
[Test Selection procedure](workflow_rules.md#test-selection). The task template,
review checklist and small-model playbook use that same procedure. This is
instruction-level integration: agents choose focused commands explicitly. The
pre-push selector adds a deterministic minimum; it does not replace that judgment.

For rapid prototypes, the [command triggers](#prototype-cadence-and-cost)
uses narrow checks while iterating, then the owning tests and affected user flow
before handoff. Broader budgets are ceilings, not time to fill. Sensitive changes
keep their qualification requirements. Passing output stays compact; detailed
evidence is read only when needed, reducing avoidable model context overhead.

| Tier | When | What to run | Intended feedback |
| --- | --- | --- | --- |
| Fast code tests | Every relevant small change | `npm run test:fast`; affected Go package and desktop tests as applicable | Seconds with warm tooling; precise rule/contract failures |
| Deterministic browser workflows | A form, route, persistence or loading change | `npm run test:workflows`, filtered to the relevant workflow while developing | Real user actions and local backend, with named steps and evidence |
| Broad qualification and bounded exploration | Major updates, migration, release, security/storage/lifecycle changes | Applicable full suites and platform gates; bounded exploration when selected | Wider interactions, recovery and platform evidence |

These are selection rules, not substitutes for required checks. A small change
to encryption or deletion can require broad qualification. A wording change
usually needs only its relevant checks. Agent exploration is an additional
source of scenarios and evidence; deterministic regressions remain the gate.

Do not add fixed global coverage percentages in this first slice. Inspect
uncovered changed branches and important failure paths. Coverage can show
which code ran, but cannot show whether the assertions would detect a bug.
Track flakiness, suite duration, escaped defects and recurring missing scenarios
when there is enough history to make those measures useful.

## First executable browser slice

`tests/workflows/profile.test.mjs` covers:

1. Empty setup cannot proceed; preview creates no profile; editing the name
   invalidates the old confirmation.
2. An edited profile is saved through the native API, survives browser reload,
   and later unsaved edits are discarded by Cancel without changing the file.
3. An empty required name is rejected by browser validation without a PUT or
   canonical file change.
4. A controlled HTTP 500 before the backend write exposes its error code,
   preserves input, leaves the file unchanged, and permits an explicit retry
   against the real backend followed by reload.

The fourth test injects a response at one boundary. It proves **UI recovery from
that response**, not disk-full handling or backend rollback after a partial
write. Those need backend fault tests. Browser reload proves persisted reads;
process restart and installed Electron behavior are separate existing gates.

By default, the fixture builds a disposable sidecar from the current worktree
once per file. Set `CAREER_HUB_SIDECAR_BINARY` to reuse a binary freshly built
from the source under test; relative paths resolve from the repository root.
The caller is responsible for its provenance. The fixture checks that the path
is an accessible executable file, not that it matches current source. An invalid
explicit path fails setup rather than silently falling back to a build. Each test starts a
fresh service on a random loopback port and creates its own empty Home. There
is no option to provide a private Home or an existing application URL. The
browser context blocks requests outside its own service origin. Processes run
with hidden windows and stop through the native EOF shutdown contract; forced
shutdown is a failure.

The reviewed harness also closes contexts after partial browser setup, retains
traces through native/browser cleanup failures, and bounds the wait after forced
termination. Cleanup or artifact failures keep the original assertion as the
primary cause and identify the additional failed steps. Helper regressions use
fake processes/browser resources; the four Profile scenarios still use real Go
and Chromium.
Network failure evidence also records elapsed milliseconds, the active step,
and whether the event happened during the scenario, artifact capture or cleanup.
This helps distinguish a failed reload from connection errors after shutdown;
the existing expected-error assertions still apply to scenario execution.

This first slice deliberately keeps the current pinned Playwright library and
Node's [built-in test runner](https://nodejs.org/api/test.html). There is no new
dependency. Tests run serially, without automatic retries. If the suite grows
enough to benefit from richer fixtures/reporters, assess Playwright Test then;
do not migrate the whole testing architecture now.

## Run and diagnose

From the worktree, with the repo's pinned Node and Go tooling:

```powershell
npm ci --ignore-scripts
npx playwright install chromium
npm run test:fast
npm run test:workflows
```

The browser installation is a one-time prerequisite, not a step inside a test.
When building locally, if Go is not on PATH, set `CAREER_HUB_GO_EXECUTABLE` to its absolute path.
Prebuilt mode needs no Go invocation; unset `CAREER_HUB_SIDECAR_BINARY` to resume
building current source. No installed or user-selected Data Home is reused.
Use `PLAYWRIGHT_BROWSERS_PATH` only when browsers live in a nondefault location.
First-time Go compilation may take longer than a warm workflow run.

Select one case during development:

```powershell
node --test --test-name-pattern="failed save" tests/workflows/profile.test.mjs
```

Each test prints its evidence directory **relative to OS temp**, avoiding the
personal username in an absolute path. Resolve it locally with
`node -p "require('node:path').join(require('node:os').tmpdir(), '<printed-relative-path>')"`
in the same execution environment. Paste the relative receipt into tasks/PRs;
keep the resolved absolute path local. The directory contains:

- `result.json`: scenario, result, named steps, first failed step, reason, failed
  request routes/statuses, browser errors and artifact errors.
- `screen.png`: final state, including failure state when available.
- `trace.zip`: retained on failure, with DOM/action/network evidence.
- `home/`: exclusively synthetic state for local diagnosis.

Open a failed trace locally with
`npx playwright show-trace <absolute-path-to-trace.zip>`.
[Trace Viewer](https://playwright.dev/docs/trace-viewer) supports inspecting
actions and their browser/network context. The JSON request summary excludes
headers, bodies and query strings. Traces can contain synthetic request bodies
and tokens; share only after review and remove temporary runs when finished.
This implementation does not upload them to a hosted service.

A typical failure reads:

```text
[Profile: save survives reload and cancel discards later edits]
step "reload reads the persisted value" failed: ...
```

Read evidence in this order:

1. **Setup failure**: check missing Go/browser tooling, build errors and native
   startup. This does not prove that a product workflow failed.
2. **Failed action/response**: check which operation ran and its HTTP status.
3. **Failed outcome**: compare expected rendered/persisted state to actual state.
4. **Trace and screenshot**: inspect timing, DOM and the specific request.

The failing step localizes a symptom; it does not magically establish root
cause. For example, a reload failure might come from an unsaved write, a bad
read, or stale rendering. The file assertion and HTTP response narrow that
investigation. Expected HTTP failures are captured too; their presence alone
does not make a negative-path scenario fail. The suite checks the exact declared
failed requests and rejects unexpected console errors and uncaught page errors.
The injected 500's browser resource-error message is the only console exception
in that scenario.

## Prototype cadence and cost

Use explicit test-command triggers, not a vague elapsed-time checkpoint:

- **While iterating:** run the narrow regression or relevant syntax check to
  get quick feedback. Accumulate related, low-risk tweaks to one behavior.
- **Before the agent reports a behavior change complete:** invoke the owning
  tests and selected affected flow for that task or declared subtask, including
  a relevant rejection/recovery edge. Do not wait for a user test request.
  Focused work does not need an unrelated browser suite. Update the test plan
  if accumulated edits broaden the scope.
- **When the user requests tests now:** invoke the relevant commands on the
  current state and report partial work or failures honestly. A progress update
  or a requested pause is not a completion claim and does not require finishing
  an entire test phase first.
- **Before merge/release or a major/sensitive change:** retain the required CI
  checks and applicable broader qualification. A prototype label is not an
  exception for credentials, data loss, schema, ownership or lifecycle risk.

Before editing, name the observable behavior, command and expected result.
For example: "Profile failed save keeps input and permits retry" ->
`node --test --test-name-pattern="failed save" tests/workflows/profile.test.mjs`
-> that scenario passes. If relevant code changes afterward, rerun the affected
check before claiming completion; an unchanged previously verified state does
not need a duplicate run.

Tests execute when their command is invoked by an agent, developer or CI.
These instructions are not an app-launch hook or a background test scheduler.
Normal startup checks and runtime error handling are separate product behavior;
see [execution layers](testing_strategy.md#where-checks-run).

The 30-minute broad-session budget is a maximum, not a target or a requirement
to spend that long testing. Finish as soon as the selected checks provide the
needed evidence. Record an incomplete phase if the limit is reached.

Deterministic test programs make no AI calls. Agent work choosing, authoring,
reading and debugging tests consumes model context; elapsed test runtime is a
separate cost. Keep passing output to command, exit/result, useful counts and
an evidence path. Save verbose logs locally and inspect only relevant failures;
do not repeatedly load complete logs, traces or screenshots from passing runs.
Reuse stable fixtures and meaningful regressions, avoid tests for every helper
or cosmetic change, and do not add arbitrary coverage targets.

Measure recurring feedback delays before adding more gates. First-time test
authoring and environment setup can cost more than repeat execution; that
investment should protect important behavior across later edits. Tests verify
behavior; small maintainable changes and code review also protect code quality.

## Focused checks for small fixes

1. Reproduce the defect. When practical, run a regression that fails on the
   original behavior before fixing it. If the original state is unavailable,
   record that limitation rather than inventing a failing baseline.
2. Run the narrow regression while iterating, then its owning test file/package
   and affected contract consumers. Use file/package ownership and call sites;
   a changed-path match alone is not a complete dependency analysis.
3. Add a relevant rejection/recovery edge for changed failure behavior. Check
   observable outcomes rather than only that a function or button was called.
4. Add the affected browser scenario for the Workflow tier. The current suite
   covers Profile; a Profile pass does not qualify Jobs, Resumes or Applications.
   When coverage is missing, create a bounded current-runtime regression or
   perform and record targeted manual validation with the remaining gap.
5. After checks pass, stop repeating them unless code, fixtures, environment or
   unresolved evidence changed. Record the result and move toward handoff.

Put Node runner options before test paths; appending flags after npm's existing
test-path glob does not reliably select a subset. Never replace a missing
current-runtime test with a retired Python reference script and call it parity.

When a repo skill or mapped agent service changes, inspect its
`.agents/skills/<skill>/references/contract.yaml`, execute the affected mapped
tests through their owning runner/package, and run
`python -m scripts.agent_workflow_audit --project-root . --changed --pretty`.
The audit checks mappings and compatibility; it does not run the tests or
prove runtime behavior. `review_required` calls for semantic inspection, not
automatic rewriting of skill instructions.

## Controlled broader testing

Before a Broad phase, extend the plan with:

- The commit/diff, affected workflows, consumers and invariants.
- Exact suites, order, prerequisites and applicable platform/package gates.
- An overall session budget and command deadlines. Default local deterministic
  broad-session budget: 30 minutes; keep tighter existing test/build limits.
- Synthetic fixture and artifact locations, expected skips and unavailable
  qualification environments.
- Whether bounded agent exploration adds useful coverage, and its charter if
  selected. Record a reason when deterministic evidence is sufficient.

Run focused checks first so a local defect does not consume the entire broad
budget. The applicable full-suite inventory includes root `npm run test:fast`,
`npm run test:workflows`, `npm --prefix desktop test`,
`npm --prefix desktop run check`, extension compatibility, independent Python
tests (`python -m unittest discover -s tests -q`), and Go regression from
`backend/` (`go test ./cmd/... ./internal/...`). Choose the relevant suites;
a backend-only change does not automatically need every extension check.
`npm run check:broad` runs the standard broad local set with a 30-minute total
deadline and per-command limits. The manual `Full qualification` Windows job
also has a 30-minute deadline. Routine Windows CI has a 15-minute deadline;
these are safety ceilings, not timing targets or promised costs.

Native desktop smokes require a freshly built, verified sidecar and installed
locked Electron/Chromium tooling; follow [local development](../guides/local_development.md).
Use `npm run smoke:native-desktop` with the synthetic empty-profile mode and
`npm run smoke:competency` where applicable. Installed packaging, migration,
Windows credentials, crash recovery and external-reference gates have their
own prerequisites: follow their existing qualification documents. Do not
enable real-account or installed-app mutations through this general test plan.

If a limit is reached or prerequisites are missing, preserve the command,
revision, evidence and required continuation. Mark the phase incomplete;
passing earlier checks does not make an omitted qualification pass. Revise a
budget only with an explicit recorded reason; never silently raise timeouts
to hide a failure. Keep external commands bounded and follow AGENTS.md's one
bounded follow-up-wait rule.

For selected agent exploration, use the [charter and limits](testing_strategy.md#controlled-agentic-testing-for-major-changes):
one session, 20 minutes, 40 browser actions, and at most one replay per suspected
defect unless the charter explicitly changes those limits. Preserve expected
outcomes; do not weaken assertions, skip failures or accept observed broken
behavior as the new baseline. Convert confirmed bugs into deterministic
regressions and report unexplored scope. Exploration cannot replace required
deterministic or platform qualification.

## How to design a useful regression test

Use **Arrange -> Act -> Assert**: create explicit starting data, perform one
meaningful action, then check an independent expected outcome. The expected
outcome is the test's **oracle**.

For a save workflow, check three things: the UI tells the truth, the native
response is correct, and a later read sees the persisted value. Checking only
that a button was clicked is insufficient. Checking only a success toast can
miss an implementation that displays success without writing anything.

For every bug, write down the broken behavior before fixing it. Add the
smallest regression at the layer that owns it and verify that it fails for
the original defect. Use a browser regression when wiring or presentation is
part of the bug. Avoid testing every internal helper merely because it exists.

Choose a few relevant edges rather than multiplying every permutation:

| Risk | Candidate cases |
| --- | --- |
| Validation | Empty/whitespace input, limits, Unicode, malformed source |
| Persistence | Save/reload, cancel, duplicate action, restart |
| Async work | Delay, abort, stale response, selected record changed mid-request |
| Failure recovery | Structured rejection, connection loss, explicit retry, unknown write outcome |
| Authority | Changed revision, stale preview, confirmation absent, wrong record |

Do not infer that every failed write is safe to retry. The new Profile fixture
returns a 500 **before** a write. An uncertain post-write outcome needs an
owner-specific recovery/idempotency test; never invent rollback from an error.

## Controlled agentic testing for major changes

[Playwright documents planner, generator and healer agents](https://playwright.dev/docs/test-agents).
These capabilities make discovery and test authoring easier, but a tool's
ability to repair or skip a failing test does not authorize accepting that
change. The following limits are **Career Hub policy choices**, not claims of
universal industry requirements:

1. Define a charter before a run: commit/worktree, feature, allowed routes,
   synthetic fixture, expected invariants, allowed files and artifact location.
2. Default budget: one exploration session, at most 20 minutes and 40 browser
   actions, and at most one replay per suspected defect. A different budget
   requires an explicit charter change. Stop at the first budget reached.
3. Use synthetic isolated state. No real mailbox, private Vault, external
   submission, credential recovery, install/update activation or paid API call
   through this testing charter. Such qualification needs its own authorized
   scope and fixtures.
4. Freeze expected behavior before exploration. Agents may propose selectors
   or scenarios; they may not weaken assertions, skip a failure, replace
   expected results with observed results, or raise timeouts just to get green.
5. Classify a failure as product, test, environment or unresolved, with a
   reproduction and evidence. A repair requires review of why the original
   expectation or locator was wrong. Unresolved failures remain visible.
6. Convert a confirmed product bug into a deterministic regression. Demonstrate
   failure before the fix and success afterward. Keep discovery and acceptance
   decisions separately recorded.
7. Report explored scope, untested scope, bugs, artifacts, consumed budget and
   required next action. A passing agent exploration is not full coverage.
8. For long external tools, follow AGENTS.md: one bounded follow-up wait, then
   preserve a continuation checkpoint if no new result arrives.

Coding agents follow this policy through `AGENTS.md` and the test-selection
procedure. Exploration budgets are instruction-level limits, not an automatic
budget-enforcement service. This workflow adds no AI calls or installed agents.

## Next increments

First extend the same fixture to job intake (save, duplicate refusal and stale
JD revision), then application stage/submitted-resume tracking and resume
import approval. Add scenarios by observed defects and user risk. Keep each
increment independently reviewable.

Browser workflows run through agent selection, affected pre-push checks and
manual hosted qualification. Failure artifacts remain local by default; do not
upload synthetic Homes or traces without reviewing their contents. Record actual
verification in CH-146/CH-148; merely configuring a command is not a test pass.
