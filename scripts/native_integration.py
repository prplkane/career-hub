"""Run Windows native integration using the current qualification's build."""
from __future__ import annotations

import argparse
import hashlib
import os
import subprocess
from pathlib import Path

from scripts.test_selection import ROOT, git_environment


def native_environment(root: Path, startup_fixture: Path) -> dict:
    if os.name != "nt":
        raise ValueError("Native qualification requires Windows; it cannot count platform skips as passes.")
    binary = root / "packaging/dist/go-onedir/career-hub-sidecar/career-hub-sidecar.exe"
    for label, candidate in (("sidecar", binary), ("startup fixture", startup_fixture)):
        if not candidate.is_absolute() or not candidate.is_file():
            raise ValueError(f"Missing fresh {label}; run the native profile to build prerequisites.")
    # Hash the exact freshly built artifact, overriding every inherited selector.
    # The caller must build from this checkout first; a hash is not source provenance.
    return git_environment(root, {
        **os.environ, "CAREERHUB_TEST_GO_SIDECAR": "1",
        "CAREERHUB_GO_SIDECAR": str(binary),
        "CAREERHUB_GO_SIDECAR_SHA256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        "CAREERHUB_GO_STARTUP_FIXTURE": str(startup_fixture),
    })


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--startup-fixture", type=Path, required=True)
    args = parser.parse_args()
    try:
        environment = native_environment(ROOT, args.startup_fixture)
    except (OSError, ValueError) as error:
        # File content and OS paths belong in local evidence, not this receipt.
        print(f"ERROR Native prerequisites: {error}" if isinstance(error, ValueError)
              else "ERROR Native prerequisites could not be read.")
        return 2
    return subprocess.run([
        "node", "--test", "--test-concurrency=1",
        "desktop/tests/go-backup.integration.test.js",
        "desktop/tests/go-maintenance.integration.test.js",
        "desktop/tests/go-sidecar.integration.test.js",
    ], cwd=ROOT, env=environment, check=False).returncode


if __name__ == "__main__":
    raise SystemExit(main())
