# Native Windows desktop packaging

The selected runtime is Go 1.27.3, Windows x64, packaged beside Electron 44.7.0.
Use Node 24.19.0 and the committed npm lockfiles. No Python runtime or PyInstaller
artifact is selected by the desktop launcher or package scripts.

```powershell
npm --prefix desktop run build:go-sidecar
npm --prefix desktop run make:windows:dev
```

Set `CAREER_HUB_GO_EXECUTABLE` to an absolute reviewed Go executable when it is
not on PATH. Standard packages require clean source and a matching native
artifact. The separate `make:windows:go-preview` target preserves preview
installer identity and data isolation; it uses the same native backend.

The artifact lives under ignored `packaging/dist/go-onedir/career-hub-sidecar`.
Manifest schema 3 binds both native executables, source commit, Go module inputs,
complete file inventory and reviewed maintenance capabilities. Public resources
are VERSION, the static UI and three shared catalogs. Private data, templates,
resumes, development scripts and tests are excluded. The build fixes Windows
amd64, disables CGO and VCS embedding, trims source paths and uses readonly
module resolution. The host verifies native resource identity and both executable
hashes before launching its owned service.

Standard metadata schema 4 selects Go explicitly. Runtime admission refuses
retired Python metadata; package preparation may safely replace exact old
metadata in its generated staging directory. Go preview metadata remains schema 3.

macOS, code signing, notarization and production release are not implied by
Windows development qualification. No installer is published by these commands;
only the tag-triggered release workflow (`.github/workflows/release.yml`)
publishes update packages.
