# CG-900 Remediation Evidence — 2026-08-23

This receipt records remediation evidence for the independent CG-900 **FAIL**
and the release-evidence follow-up after the independent rerun.

## Independent rerun — 2026-08-24

The fresh rerun returned **PASS WITH GAPS** and found no acceptance-blocking
behavioral defect. It independently reproduced the original change-then-revert
race as rejected before backup installation, canonical-ID mutation, or audit
creation; verified all 17 CG definitions and both rendered surfaces; ran all
717 tests; and completed the functional and security gates.

The three remaining findings were release-evidence hygiene gaps:

1. Playwright was available only through an undeclared bundled runtime.
2. the two Markdown hard-break lines in the untracked traceability document
   failed a comprehensive whitespace check; and
3. this receipt reported 27 passing storage/runtime tests without the exact
   command, while the complete named modules contain 29 passing tests plus
   three expected skips.

The follow-up declares the Playwright library and lockfile, runs the rendered
smoke in CI, replaces the trailing-space hard breaks, and records the exact
commands and current counts below. The independent verdict remains
**PASS WITH GAPS**; this receipt does not retrospectively relabel it.

## Recovery ZIP integrity finding

**Broken boundary.** `create_backup()` captured source hashes before
`ZipFile.write()` but installed the archive after checking only names and
sizes. A same-size source change followed by a revert could therefore leave the
Vault revision unchanged while the stored payload disagreed with its manifest.

**Remediation.** `src/data_backup.py` now reopens the temporary archive and
streams every stored payload through SHA-256 verification against the pre-write
manifest before `os.replace()` installs the named recovery ZIP. A mismatch
raises `DataBackupError`, removes the temporary archive, and returns no backup.
The migration caller converts that failure into
`VaultCompetencyMigrationError` before any canonical ID or audit write.

**Tracked proof.** The following tests perform the change only while the ZIP
writer reads the target and then restore identical source bytes:

- `DataBackupTests.test_create_rejects_same_size_change_then_revert_during_archive_write`
- `VaultCompetencyMigrationTests.test_change_then_revert_during_backup_aborts_before_migration`

They assert that the source is restored, no named ZIP remains, migration writes
no `competency_ids`, and no migration audit directory is created. The normal
lossless backup/restore test remains green.

## CG ID traceability finding

`docs/history/competency_graph_traceability.md` is now the versioned CG-1.0 definition
and requirement-to-evidence map for all 17 IDs from CG-000 through CG-900. The
Capability Acceptance Registry remains authoritative for product status. An
acceptance test rejects missing, duplicate, reordered, or unlinked task
definitions.

## Rendered UI evidence finding

`tests/competency_surfaces_smoke.cjs` starts an isolated server over a temporary
synthetic Vault and demand projection, then drives Chromium through both
surfaces. It verifies:

- Skills search, selection, aliases, evidence, canonical-record navigation,
  and the `#competencies` compatibility redirect;
- Demand coverage counts, safe absence wording, coverage filtering, drawer
  focus entry/restoration, and Demand→Skills navigation;
- desktop and 390-pixel layouts without horizontal overflow;
- no console/page errors and no changes to synthetic Vault or job-source
  hashes.

The run produced six inspected screenshots and completed with:

```text
competency surfaces smoke: passed
```

## Reproducible verification commands

The complete storage/runtime module selection is:

```powershell
python -m unittest -q tests.test_data_backup tests.test_runtime_paths tests.test_data_migration tests.test_data_home_selection
```

Result: **29 passed, 3 expected platform skips** (32 tests run).

The complete repository suite is:

```powershell
python -m unittest discover -s tests -q
```

Result: **717 tests run; OK with 3 expected platform skips** in the independent
rerun.

The declared rendered check is:

```powershell
npm ci --ignore-scripts
npx playwright install chromium
npm run smoke:competency
```

Result: passed on desktop and a 390-pixel viewport; six screenshots were
inspected. CI runs the locked install, Chromium install, and same smoke command.

The remaining release-hygiene commands are:

```powershell
python -m compileall -q src tests
node --check tests/competency_surfaces_smoke.cjs
python scripts/check_public_tree.py
git diff --check
```

Because normal `git diff --check` omits untracked files, every untracked text
file must additionally be checked until it becomes tracked:

```powershell
$messages = @()
git ls-files --others --exclude-standard | ForEach-Object {
  $messages += @(git diff --no-index --check -- /dev/null $_ 2>&1)
}
if ($messages.Count) { $messages; exit 1 }
```

Both the tracked diff and the full untracked set passed after the two hard-break
spaces were removed.

No private `CareerHubData` path was read or modified. No commit, push, merge, or
PR mutation was performed as part of this remediation.

## Ongoing release check

The independent rerun is complete. Future Competency Graph releases retain the
race regressions, full suite, public-tree gate, exact traceability check, and
locked rendered smoke as required evidence.
