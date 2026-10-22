"""Inspect exactly the outgoing commits, then check the clean current branch."""
from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

from scripts.local_checks import Runner, run_selected
from scripts.test_selection import ROOT, git, select

OID = re.compile(r"[0-9a-f]{40}(?:[0-9a-f]{24})?\Z")


def outgoing(root: Path, remote: str, records: str) -> tuple[list[str], list[str]]:
    commits: set[str] = set()
    paths: set[str] = set()
    head = git(root, "rev-parse", "HEAD").strip()
    for line in records.splitlines():
        fields = line.split()
        if len(fields) != 4 or not OID.fullmatch(fields[1]) or not OID.fullmatch(fields[3]):
            raise ValueError("Malformed pre-push ref record.")
        local_ref, local, remote_ref, old = fields
        if set(local) == {"0"}:
            print("SKIP  Ref deletion sends no new file content.")
            continue
        if not local_ref.startswith("refs/heads/") or not remote_ref.startswith("refs/heads/"):
            raise ValueError("Only branch pushes are supported by this hook; tags require explicit release qualification.")
        if local != head:
            raise ValueError("Push the checked-out branch at HEAD so local tests examine the code being sent.")
        args = ["rev-list", local]
        if set(old) != {"0"}:
            git(root, "cat-file", "-e", old + "^{commit}")  # Missing base fails, never guesses.
            args.append("^" + old)
        else:
            # Exclude only this destination's fetched refs, never another remote's history.
            remotes = git(root, "remote").splitlines()
            if remote in remotes:
                refs = git(root, "for-each-ref", "--format=%(objectname)", "refs/remotes/" + remote + "/").splitlines()
                args.extend("^" + ref for ref in refs)
        new = git(root, *args).splitlines()
        commits.update(new)
        # Check the tip even for a branch made entirely of already-published commits.
        commits.add(local)
        for revision in new:
            paths.update(git(root, "diff-tree", "--root", "--no-commit-id", "--name-only", "--no-renames", "-r", "-m", "-z", revision).split("\0"))
    return sorted(commits), sorted(paths - {""})


def require_clean(root: Path, head: str) -> None:
    if git(root, "status", "--porcelain", "--untracked-files=normal").strip():
        raise ValueError("Commit or set aside pending changes before push; tests must exercise the committed files.")
    if git(root, "rev-parse", "HEAD").strip() != head:
        raise ValueError("HEAD changed while checks ran; retry on the final revision.")


def main() -> int:
    runner = Runner()
    try:
        if len(sys.argv) != 3:
            raise ValueError("This command is invoked by Git pre-push with a remote name and URL.")
        head = git(ROOT, "rev-parse", "HEAD").strip()
        require_clean(ROOT, head)
        commits, paths = outgoing(ROOT, sys.argv[1], sys.stdin.read())
        runner.context = {"head": head, "commits": commits, "working_tree": False}
        if not commits:
            return 0
        print(f"Checking {len(commits)} outgoing/tip snapshots. No data is uploaded by this hook.")
        for commit in commits:
            if not runner.run("Privacy " + commit[:12], [sys.executable, "scripts/check_public_tree.py", "--rev", commit], timeout=90):
                return 1
        # --no-walk scans these commits only; -m includes merge-parent diffs, --root initial commits.
        for start in range(0, len(commits), 50):
            log_options = "--no-walk --root -m " + " ".join(commits[start:start + 50])
            if not runner.run("Outgoing secret scan", ["gitleaks", "git", ".", "--redact", "--no-banner",
                                                       "--log-opts=" + log_options], timeout=180):
                return 1
        selected = set(select(paths)["checks"])
        print("Local selection: " + (", ".join(sorted(selected)) or "documentation only; application checks skipped"))
        if not run_selected(runner, selected):
            return 1
        require_clean(ROOT, head)
        return 0
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print("ERROR Push checks incomplete: " + (str(error) if isinstance(error, ValueError)
              else f"{type(error).__name__}; check Git refs/tools and fetch a missing remote base."))
        return 2
    finally:
        runner.finish()


if __name__ == "__main__":
    raise SystemExit(main())
