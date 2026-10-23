# UI stylesheet architecture

`../styles.css` is the only stylesheet linked by `index.html`. It is an
import-only manifest; its order is part of the UI contract because the local
app intentionally has no build step.

The visual language is the signature design system's **Ink & paper** palette:
warm white paper, ink-blue sidebar and actions, green only for success, and a
"Night ink" dark theme. `tokens.css` is the single source for these values,
and the browser extension derives its own stylesheet from the same tokens.

## Layers

`styles.css` declares `@layer tokens, legacy;`.

- `tokens` holds `tokens.css` only.
- `legacy` holds every other module, in manifest order, so their relative
  order and specificity behave exactly as before.

Between layers, specificity is ignored and the later layer always wins. So
`tokens.css` must contain **only custom properties**. A real rule placed there
loses to any rule in `legacy`. For example, a plain `* { scrollbar-color }`
would beat a dark-theme scrollbar rule. Element defaults belong in
`foundation.css`.

## Ownership

| Module | Owns |
| --- | --- |
| `tokens.css` | Every colour, font, size, radius, and shadow value (light and dark) |
| `foundation.css` | Element defaults, base layout, the app shell, and root text rendering |
| `components.css` | Reusable controls, feedback primitives, motion, and shell refinements |
| `themes.css` | Dark-theme overrides for components that do not yet use tokens alone |
| `skills.css` | Skills Registry, skills hub, and competency explorer |
| `diagnostics-settings.css` | Diagnostics, document blocks, the settings popover, and the application-helper dialog |
| `help.css` | In-app setup guide, FAQ, and integration status |
| `resume-review.css` | Resume intake, extraction review, and resume-library foundations |
| `resume-planning.css` | Career Vault evidence selection for a job-scoped resume plan |
| `materials.css` | Resume and cover-letter editors, review, and export states |
| `jobs.css` | Job intake, Job Workspace, assessment, and posting details |
| `applications.css` | Saved-application ledger and application detail drawer |
| `insights.css` | Demand and Career Vault evidence insights |
| `discovery.css` | Opportunity feed and discovery review |
| `application-updates.css` | Email outcomes, sender routing, and manual email import |
| `career-vault.css` | Profile, experience, projects, and education editors |
| `contacts.css` | Saved contacts and contact-confirmation workflow |
| `connectivity.css` | Global offline, reconnect, and internet-action availability feedback |

## Rules

1. **Values come from tokens.** Use `var(--...)` for every colour, font
   size (`--text-xs` to `--text-xl`), radius (`--radius-sm`, `--radius`,
   `--radius-lg`), and shadow. Add a token to `tokens.css` before using a
   new value. Mark a deliberate exception with a `design-allow` comment that
   gives the reason.
2. Put a selector in the narrowest owning feature module. Responsive and
   feature-specific dark rules stay beside that feature.
3. Add reusable primitives to `components.css` and element defaults to
   `foundation.css`. Do not add feature rules to either file.
4. Do not add rules directly to `styles.css`, nest `@import`, or rely on one
   feature module overriding another.
5. **Adding or removing a module** means updating `styles.css`, the CSS list in
   `backend/internal/sidecar/httpapi/static.go`, and this table. Then run
   `go test ./internal/sidecar/httpapi/` from `backend/` and
   `python scripts/css_audit.py --check`.
6. Do not reorder the manifest or move rules between modules unless a
   computed-style snapshot (`node tests/css_snapshot.cjs`) shows the change is
   intended. Moving a rule earlier changes the result when another rule
   matches the same element with exactly equal specificity. Shrinking an
   `:is()` list lowers the specificity of the selectors that remain.

## Guards

- `tests/test_css_budget.py` is a **ratchet** over `scripts/css_budget.json`.
  It covers colour literals, `!important`, pill radii, raw font sizes,
  cross-file duplicate selectors, dark overrides, undefined custom
  properties, and more. Values may only go down. Record an improvement with
  `python scripts/css_audit.py --ratchet`. Fix a regression; never raise the
  budget.
- `tests/test_css_tokens_contrast.py` keeps every required text pair at
  4.5:1 and every UI mark at 3:1, in both themes.
- `scripts/css_audit.py --list <metric>`, `--selector <class>`, and
  `--dark-redundancy` explain the numbers. `scripts/css_token_codemod.py` and
  `scripts/css_scale_codemod.py` map raw values onto tokens deterministically.
