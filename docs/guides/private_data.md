# Private Local Data

Career Hub keeps personal career records available to the local application and
coding agents without publishing them to GitHub.

Persistent personal data belongs in the desktop's Data Home, outside the code
checkout and the installed application. On Windows each release channel has its
own Home at `%LOCALAPPDATA%\CareerHub\<channel>` (`dev`, `stable`, or
`dev-go-preview`), resolved by `desktop/src/app_paths.js`; Settings shows the
active path and provides backup, restore, and recovery. Homes under
`%USERPROFILE%\CareerHubData\` were created by earlier releases and are never
adopted implicitly. The repository-root paths below remain ignored so legacy
copies inside a developer checkout cannot be committed.

## Never Commit

- `career_vault/*.yaml`
- `career_vault/taxonomy/`
- `career_vault/raw/`
- `career_vault/intake_runs/`
- `career_vault/logs/`
- `resume_versions/`
- `applications/`
- `documents/`
- `logs/`
- `review_artifacts/`
- `jobs/incoming/`
- `jobs/normalized/`
- `outputs/`
- `templates/`
- `backups/`
- `security-reports/`
- `secrets/`

These paths are ignored by the repository but remain readable locally. Do not
use `git add -f` to bypass the protection. Do not place API keys in `.env`,
YAML, SQLite, scanner reports, screenshots, or test fixtures; future provider keys use
Windows Credential Manager through the Go `backend/internal/storage/secretstore`
package.

GitHub has no server-side ignore for committed content. Before every push,
verify the staged file list and scan it for personal data or secrets. Raw
Semgrep, Trivy, Gitleaks, and similar scanner reports stay in the external
`security-reports` data-home directory, never in Git or a normal backup ZIP.

Agent-authored application texts live below
`outputs/application_texts/<job_id>/` when used. They may contain private
answers but must never contain passwords, government identifiers, payment data,
voluntary demographic answers, or private document contents. They use the
normal data-home ACL and recovery boundary; they are not separately encrypted.

## Repairing Data Home permissions

Every start checks that the Data Home folder is still private: owned by your
Windows account (or the Administrators group, where Windows assigns that for
administrators), with permissions for only your account and SYSTEM. If that
changed, for example after copying or restoring the folder
with another tool, Career Hub stops with `DATA_HOME_PERMISSIONS_UNSAFE` and
changes nothing.

To restore the private permissions, close Career Hub and run this in PowerShell
as your own account (use `stable` instead of `dev` for the stable app):

```powershell
$dataHome = Join-Path $env:LOCALAPPDATA 'CareerHub\dev'
$acl = New-Object System.Security.AccessControl.DirectorySecurity
$acl.SetAccessRuleProtection($true, $false)
foreach ($id in @([System.Security.Principal.WindowsIdentity]::GetCurrent().User,
                  (New-Object System.Security.Principal.SecurityIdentifier('S-1-5-18')))) {
  $acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule(
    $id, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')))
}
Set-Acl -LiteralPath $dataHome -AclObject $acl
```

This removes every other entry, keeps inheritance off at the root, and lets
files inside inherit the private permissions. If the folder's **owner** is a
different account, it must be taken back first by an administrator
(`takeown /F <folder>`), because Windows does not let an ordinary account
change ownership.
