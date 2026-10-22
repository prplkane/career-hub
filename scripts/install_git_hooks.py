"""Enable hooks for this checkout, preserving any existing hook installation."""
from __future__ import annotations

import os
import subprocess
from pathlib import Path

from scripts.test_selection import ROOT, git, git_environment


def config(root: Path, key: str) -> str:
    result = subprocess.run(["git", "config", "--get", key], cwd=root, text=True, capture_output=True,
                            timeout=10, env=git_environment(root))
    if result.returncode not in (0, 1):
        raise ValueError("Cannot inspect Git configuration.")
    return result.stdout.strip()


def install(root: Path) -> None:
    expected = (root / ".githooks").resolve().as_posix()
    existing = config(root, "core.hooksPath")
    if existing and existing != expected:
        raise ValueError("Existing core.hooksPath retained; integrate the pre-push command into that hook manually.")
    if not existing and (root / git(root, "rev-parse", "--git-path", "hooks/pre-push").strip()).is_file():
        raise ValueError("Existing pre-push hook retained; integrate manually.")
    if config(root, "core.worktree") or config(root, "core.bare") == "true":
        raise ValueError("Custom/bare worktree configuration requires manual hook setup.")
    hook = root / ".githooks/pre-push"
    if not hook.is_file():
        raise ValueError("Checked-in .githooks/pre-push is missing.")
    if os.name != "nt":
        hook.chmod(hook.stat().st_mode | 0o111)
    git(root, "config", "--local", "extensions.worktreeConfig", "true")
    git(root, "config", "--worktree", "core.hooksPath", expected)
    print("Pre-push checks enabled for this checkout. Existing hooks and other worktrees are unchanged.")


if __name__ == "__main__":
    try:
        install(ROOT)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        raise SystemExit(f"Hook setup stopped: {error}")
