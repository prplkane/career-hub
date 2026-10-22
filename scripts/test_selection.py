"""Conservative, shared path selection for local checks and routine CI."""
from __future__ import annotations

import argparse
import json
import os
import subprocess
from functools import lru_cache
from pathlib import Path, PurePosixPath

ROOT = Path(__file__).resolve().parents[1]
ALL = {"python", "node", "desktop", "go", "build", "native", "startup", "workflows",
       "competency", "recovery", "diagnostics", "extension"}
TOOLS = {"test_selection.py", "local_checks.py", "pre_push.py", "install_git_hooks.py",
         "check_public_tree.py", "agent_workflow_audit.py"}


@lru_cache(maxsize=1)
def repository_environment_keys() -> tuple[str, ...]:
    # Git itself defines which variables redirect commands to a repository.
    return tuple(subprocess.check_output(["git", "rev-parse", "--local-env-vars"],
                                        text=True, timeout=10).splitlines())


def git_environment(root: Path, environment: dict | None = None) -> dict:
    clean = dict(os.environ if environment is None else environment)
    for key in repository_environment_keys():
        clean.pop(key, None)
    for key in list(clean):
        if key.startswith(("GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_")):
            del clean[key]
    # Trust only this already-selected checkout; never add a global wildcard.
    clean.update(GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="safe.directory",
                 GIT_CONFIG_VALUE_0=str(root.resolve()))
    return clean


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=root, text=True,
                                   encoding="utf-8", errors="strict", timeout=30,
                                   env=git_environment(root))


def is_documentation(name: str) -> bool:
    path = PurePosixPath(name)
    return (path.suffix == ".md" and (
        len(path.parts) == 1 or name.startswith(("docs/", ".backlog/", ".agents/skills/"))
    )) or name in {"LICENSE", "NOTICE"}


def select(paths: list[str]) -> dict:
    checks: set[str] = set()
    for name in paths:
        if is_documentation(name):
            continue
        if name.startswith("src/intake_ui_static/"):
            checks.update({"node", "python", "build", "workflows", "competency", "recovery", "diagnostics"})
        elif name.startswith("backend/") and name not in {"backend/go.mod", "backend/go.sum"}:
            checks.update(ALL - {"extension"})
        elif name.startswith("desktop/") and not name.endswith(("package.json", "package-lock.json")):
            checks.update({"node", "desktop", "build", "native", "startup", "recovery", "diagnostics"})
        elif name.startswith("extension/"):
            checks.update({"python", "node", "extension"})
        elif name.startswith("tests/workflows/"):
            checks.update({"node", "workflows"})
        elif name.startswith("tests/test_") or (name.startswith("scripts/") and PurePosixPath(name).name in TOOLS):
            checks.update({"python", "node"})
        elif name.startswith("tests/") and name.endswith((".test.mjs", ".test.js", ".test.cjs")):
            checks.add("node")
        else:
            checks.update(ALL)  # Includes dependencies, CI, packaging and unknown consumers.
    return {"checks": sorted(checks), "code": bool(checks), "go": "go" in checks,
            "app": bool(checks & {"go", "build", "startup", "extension", "workflows"})}


def changed_paths(root: Path, base: str, head: str = "HEAD", *, worktree: bool = False) -> list[str]:
    base = git(root, "rev-parse", "--verify", base + "^{commit}").strip()
    head = git(root, "rev-parse", "--verify", head + "^{commit}").strip()
    paths = set(git(root, "diff", "--name-only", "--no-renames", "-z", base, head).split("\0"))
    if worktree:
        paths.update(git(root, "diff", "--name-only", "--no-renames", "-z", "HEAD").split("\0"))
        paths.update(git(root, "ls-files", "--others", "--exclude-standard", "-z").split("\0"))
    return sorted(paths - {""})


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", default=os.environ.get("CHECK_BASE"))
    parser.add_argument("--head", default=os.environ.get("CHECK_HEAD", "HEAD"))
    parser.add_argument("--github-output", action="store_true")
    args = parser.parse_args()
    # A new/default branch without a usable base receives full checks.
    result = select(changed_paths(ROOT, args.base, args.head)) if args.base and set(args.base) != {"0"} else select(["unknown"])
    print(json.dumps(result, indent=2))
    if args.github_output:
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
            for name in ("code", "go", "app"):
                output.write(f"{name}={str(result[name]).lower()}\n")


if __name__ == "__main__":
    main()
