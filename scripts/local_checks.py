"""Deterministic local checks; no tool installation, AI calls or automatic retries."""
from __future__ import annotations

import argparse
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

from scripts.test_selection import ALL, ROOT, changed_paths, git, git_environment, select


class Runner:
    def __init__(self, root: Path = ROOT, budget: float = 1800):
        self.root = root
        self.started = time.monotonic()
        self.budget = budget
        self.directory = Path(tempfile.mkdtemp(prefix="career-hub-checks-"))
        self.results: list[dict] = []
        self.context: dict = {}
        print(f"Local logs relative to OS temp: {self.directory.name}/", flush=True)

    def run(self, name: str, command: list[str], *, cwd: str = ".", timeout: float = 180,
            env: dict | None = None) -> bool:
        print(f"RUN   {name}", flush=True)
        start = time.monotonic()
        status, reason = "ERROR", ""
        log = self.directory / f"{len(self.results) + 1:02d}.log"
        executable = shutil.which(command[0])
        remaining = self.budget - (start - self.started)
        if not executable:
            reason = f"Missing executable: {Path(command[0]).name}; install/configure it before retrying."
        elif remaining <= 0:
            reason = "Overall check budget exhausted; qualification is incomplete."
        else:
            options = {"creationflags": subprocess.CREATE_NO_WINDOW} if os.name == "nt" else {"start_new_session": True}
            try:
                environment = git_environment(self.root, {
                    **os.environ, "CGO_ENABLED": "0", "GOTOOLCHAIN": "local", **(env or {}),
                })
                with log.open("w", encoding="utf-8") as output:
                    child = subprocess.Popen([executable, *command[1:]], cwd=self.root / cwd,
                                             env=environment, stdout=output, stderr=subprocess.STDOUT, **options)
                    try:
                        code = child.wait(timeout=min(timeout, remaining))
                        status = "PASS" if code == 0 else "FAIL"
                        if code:
                            reason = f"Command exited {code}; inspect {log.name}."
                    except (subprocess.TimeoutExpired, KeyboardInterrupt):
                        if os.name == "nt":
                            subprocess.run(["taskkill", "/PID", str(child.pid), "/T", "/F"],
                                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                           creationflags=subprocess.CREATE_NO_WINDOW, timeout=15)
                        else:
                            os.killpg(child.pid, signal.SIGKILL)
                        child.wait(timeout=15)
                        reason = "Timed out or interrupted; owned process tree stopped."
            except (OSError, subprocess.SubprocessError) as error:
                reason = f"Unable to complete command: {type(error).__name__}; check local prerequisites."
        duration = round(time.monotonic() - start, 2)
        self.results.append({"check": name, "status": status, "seconds": duration, "log": log.name, "reason": reason})
        print(f"{status:5} {name} ({duration:.2f}s) {reason}", flush=True)
        return status == "PASS"

    def finish(self) -> None:
        (self.directory / "result.json").write_text(json.dumps({"context": self.context, "checks": self.results,
            "seconds": round(time.monotonic() - self.started, 2)}, indent=2), encoding="utf-8")
        print(f"Report relative to OS temp: {self.directory.name}/result.json", flush=True)


def run_selected(runner: Runner, selected: set[str], *, broad: bool = False) -> bool:
    selected = set(selected)
    if "native" in selected:
        selected.add("build")
    go = os.environ.get("CAREER_HUB_GO_EXECUTABLE", "go")
    startup_fixture = runner.directory / "startup-fixture.exe"
    specs = [
        ("python", "Development-tool tests", [sys.executable, "-m", "unittest", "discover", "-s", "tests", "-q"], ".", 180),
        ("node", "Fast UI and test-harness tests", ["npm", "run", "test:fast"], ".", 180),
        ("desktop", "Desktop foundation tests", ["npm", "test"], "desktop", 180),
        ("desktop", "Desktop configuration", ["npm", "run", "check"], "desktop", 60),
        ("go", "Go formatting", [sys.executable, "-m", "scripts.local_checks", "--gofmt"], ".", 60),
        ("go", "Go vet", [go, "vet", "./..."], "backend", 300),
        # Bound competing native process fixtures without changing any test deadline.
        ("go", "Go regression tests", [go, "test", "-p", "2", "./cmd/...", "./internal/..."], "backend", 900),
        ("build", "Build current native sidecar", ["node", "desktop/scripts/build-go-sidecar.cjs", "--allow-dirty"], ".", 240),
        ("native", "Compile native startup fixture", [go, "test", "-c", "-o", str(startup_fixture),
                                                     "./internal/sidecar/startup"], "backend", 180),
        ("native", "Native supervisor integration", [sys.executable, "-m", "scripts.native_integration",
                                                    "--startup-fixture", str(startup_fixture)], ".", 300),
        ("startup", "Native desktop startup smoke", ["npm", "run", "smoke:native-desktop"], ".", 180),
        ("workflows", "Profile workflow scenarios", ["npm", "run", "test:workflows"], ".", 180),
        ("competency", "Competency rendered scenarios", ["npm", "run", "smoke:competency"], ".", 180),
        ("recovery", "Recovery rendered scenarios", ["npm", "run", "smoke:desktop-recovery"], ".", 90),
        ("diagnostics", "Diagnostics rendered scenarios", ["npm", "run", "smoke:diagnostics-history"], ".", 90),
        ("extension", "Extension compatibility", ["npm", "run", "test:extension-compat"], ".", 90),
    ]
    if broad:
        specs.insert(7, ("go", "Reachable Go vulnerabilities", [go, "run", "golang.org/x/vuln/cmd/govulncheck@v1.9.0", "./..."], "backend", 300))
    # Opt-in native tests run only in the explicit post-build step, never from
    # stale shell overrides during the fast desktop foundation suite.
    env = {"CAREERHUB_DESKTOP_TEST_EMPTY_PROFILE": "1", "CAREER_HUB_SIDECAR_BINARY": "",
           "CAREERHUB_TEST_GO_SIDECAR": "", "CAREERHUB_GO_SIDECAR": "",
           "CAREERHUB_GO_SIDECAR_SHA256": "", "CAREERHUB_GO_STARTUP_FIXTURE": ""}
    if "build" in selected:
        env["CAREER_HUB_SIDECAR_BINARY"] = str(runner.root / "packaging/dist/go-onedir/career-hub-sidecar/career-hub-sidecar.exe")
        env["CAREERHUB_GO_SIDECAR"] = env["CAREER_HUB_SIDECAR_BINARY"]
    for key, label, command, cwd, timeout in specs:
        if key not in selected:
            print(f"SKIP  {label} (outside selected scope)")
        elif not runner.run(label, command, cwd=cwd, timeout=timeout, env=env):
            print("STOP  Remaining checks were not run.")
            return False
    return True


def check_gofmt() -> int:
    go = os.environ.get("CAREER_HUB_GO_EXECUTABLE", "go")
    fmt = str(Path(go).with_name("gofmt.exe" if os.name == "nt" else "gofmt")) if Path(go).is_absolute() else "gofmt"
    output = subprocess.check_output([fmt, "-l", "."], cwd=ROOT / "backend", text=True, timeout=60)
    print(output or "Go formatting passed.")
    return int(bool(output.strip()))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", choices=("auto", "quick", "native", "broad"), default="auto")
    parser.add_argument("--base", default="origin/main")
    parser.add_argument("--gofmt", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.gofmt:
        return check_gofmt()
    runner = Runner()
    try:
        if args.profile == "broad":
            selected = ALL
        elif args.profile == "quick":
            selected = {"python", "node", "desktop"}
        elif args.profile == "native":
            selected = {"build", "native"}
        else:
            base = git(ROOT, "merge-base", args.base, "HEAD").strip()
            selected = set(select(changed_paths(ROOT, base, worktree=True))["checks"])
        runner.context = {"profile": args.profile, "head": git(ROOT, "rev-parse", "HEAD").strip(),
                          "checks": sorted(selected), "working_tree": True}
        print(f"Profile: {args.profile}; HEAD: {runner.context['head'][:12]}")
        print("Selection: " + (", ".join(sorted(selected)) or "documentation only"))
        return 0 if run_selected(runner, selected, broad=args.profile == "broad") else 1
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"ERROR Selection could not complete ({type(error).__name__}); check the base ref and tools.")
        return 2
    finally:
        runner.finish()


if __name__ == "__main__":
    raise SystemExit(main())
