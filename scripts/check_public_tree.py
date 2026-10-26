"""Fail when Git tracks private Career Hub data or likely personal artifacts."""

from __future__ import annotations

import argparse
import hashlib
import re
import subprocess
import sys
from pathlib import Path, PurePosixPath
from typing import NamedTuple


PRIVATE_PREFIXES = (
    "applications/",
    "backups/",
    "career_vault/",
    "documents/",
    "jobs/",
    "logs/",
    "outputs/",
    "private_documents/",
    "resume_templates/",
    "resume_versions/",
    "review_artifacts/",
    "secrets/",
    "security-reports/",
    "templates/",
    "tmp/",
    ".career_hub_staging/",
)
PRIVATE_SUFFIXES = {
    ".7z",
    ".csv",
    ".db",
    ".doc",
    ".docx",
    ".env",
    ".gz",
    ".key",
    ".odt",
    ".p12",
    ".pdf",
    ".pem",
    ".pfx",
    ".rtf",
    ".sqlite",
    ".sqlite3",
    ".tar",
    ".xls",
    ".xlsx",
    ".zip",
}
PRIVATE_NAMES = {".npmrc", ".pypirc"}
MAX_TRACKED_FILE_BYTES = 2 * 1024 * 1024

# Engineering history, planning, process and decision records are not part of
# the public tree; only user and agent-flow guides under docs/guides remain.
RETIRED_DOC_PREFIXES = (
    "docs/adr/",
    "docs/history/",
    "docs/process/",
    "docs/product/",
    "docs/reference/",
    ".backlog/",
)
# Internal ticket identifiers are change narrative, not documentation.
TICKET_ID_PATTERN = re.compile(
    r"\b(CAP|PB|RM|MAIL|EXT|INT|DISC|CG|CH|LINK|SEC|DESK|ELEC|MIG|OPT|ROAD|"
    r"DATA|SIDE|PLAT|DIAG|DIST|CI|REL|UPD|HELP|CONTACT)-[0-9]{2,4}\b"
)
TICKET_ID_EXEMPT_PREFIXES = ("backend/licenses/",)
TICKET_ID_EXEMPT_PATHS = {"CHANGELOG.md"}

EMAIL_PATTERN = re.compile(
    r"(?i)(?<![A-Za-z0-9._%+-])([A-Za-z0-9._%+-]+)@"
    r"([A-Za-z0-9.-]+\.[A-Za-z]{2,})(?![A-Za-z0-9.-])"
)
PHONE_PATTERN = re.compile(
    r"(?<!\d)(?:\+?1[-.\s]?)?\(?([0-9]{3})\)?[-.\s]+"
    r"[0-9]{3}[-.\s]+[0-9]{4}(?!\d)"
)
PERSONAL_PATH_PATTERNS = (
    re.compile(
        r"(?i)\b[A-Z]:[\\/](?:Users|Documents and Settings)[\\/]"
        r"(?P<profile>[^\\/\s\"'`]+)"
    ),
    re.compile(
        r"(?<![A-Za-z0-9_])/(?:Users|home)/(?P<profile>[^/\s\"'`]+)"
    ),
)
SYNTHETIC_PROFILE_NAMES = {"example", "test"}
ALLOWED_EMAIL_DOMAINS = {
    "example.com",
    "example.net",
    "example.org",
    "example.test",
    "example.invalid",
    "users.noreply.github.com",
}
ALLOWED_EMAIL_ADDRESSES = {
    "career-hub-capture@careerhub.local",
    "handshake@notifications.joinhandshake.com",
    "jobs-noreply@linkedin.com",
}
ALLOWED_EMAIL_SUFFIXES = (
    ".example",
    ".example.com",
    ".example.net",
    ".example.org",
    ".example.test",
)
REVIEWED_BINARY_SHA256 = {
    # Unmodified Bitstream Vera faces; license and source hashes are documented
    # beside the assets. Only these exact files are admitted.
    "backend/internal/materials/materialrender/assets/Vera.ttf": (
        "c4c45690b345435b2cba52ecabe275f05e49b389b39fe68ad03afbb551288d3d"
    ),
    "backend/internal/materials/materialrender/assets/VeraBd.ttf": (
        "cc037385e4d55bfde89b13e03091ee93bf40c0c52ddd391ff031ab276f13b8e9"
    ),
    # Generated from src/intake_ui_static/favicon.svg by scripts/build_app_icon.py;
    # tests/test_app_icon.py keeps it in sync with the SVG.
    "desktop/assets/career-hub.ico": (
        "cbaadda77d0ac0d37034507a2962d8608e9abeca274ed472f0405fc9c91e4b09"
    ),
    "extension/icons/icon-16.png": (
        "c81d7f9655c70259d6b728ab7b0548624cb026282f9587530f9199fda34a383a"
    ),
    "extension/icons/icon-32.png": (
        "280fa1edff6756e75c8c4a69665cce1a45f4f7a2611523f1aac4c60dea287859"
    ),
    "extension/icons/icon-48.png": (
        "b3980df41053a44d86276b6dd32ba54a94c9e11979c7303a225c3af0e9714851"
    ),
    "extension/icons/icon-128.png": (
        "c88cc717f892d60994270cc5bb1ab0cfb2e120a7e5956160d535d940f9b723c0"
    ),
    # IBM Plex (SIL OFL 1.1), from @ibm/plex-sans@1.1.0 and @ibm/plex-mono@2.5.0.
    "src/intake_ui_static/fonts/IBMPlexSans-Regular.woff2": (
        "ba711a3085ff9f27440b6b9c4550cfc47c97bf36591d5da958b975bb3add8c1a"
    ),
    "src/intake_ui_static/fonts/IBMPlexSans-SemiBold.woff2": (
        "f78048030eab62e860efa39a0df79e2e5581bf122eb95b9bc42c0b8a4988d205"
    ),
    "src/intake_ui_static/fonts/IBMPlexSans-Bold.woff2": (
        "fa7130d854a660b39a7fc9e6e0f2dc23dba5f1346e2adea3e1fe37b6d884133d"
    ),
    "src/intake_ui_static/fonts/IBMPlexMono-Regular.woff2": (
        "ba204497f16b6d334cee9d1e963a831b73e3a56e1d6300a8489d18df7214b350"
    ),
    "src/intake_ui_static/fonts/IBMPlexMono-Bold.woff2": (
        "ea576f38d05cc44cca48c45314984beb8cc1d2b886f58e1dce99f15dc344eb1d"
    ),
}
PATH_ALLOWED_EMAIL_ADDRESSES = {
    "desktop/package-lock.json": frozenset({"i" + "@izs.me"}),
    # Preserve upstream copyright notices without admitting these addresses in
    # arbitrary product data or documents.
    "backend/internal/materials/materialrender/assets/LICENSE.pkg-errors": frozenset({"dave" + "@cheney.net"}),
    "backend/licenses/sqlite-pilot/go-humanize/LICENSE": frozenset({"dustin" + "@spy.net"}),
    "backend/licenses/sqlite-pilot/go-isatty/LICENSE": frozenset({"mattn.jp" + "@gmail.com"}),
    "backend/licenses/sqlite-pilot/modernc-memory/LICENSE-MMAP-GO": frozenset({"edsrzf" + "@gmail.com"}),
    # Provider positive/negative hostname cases are deliberately literal.
    "tests/fixtures/migration_email_sender_suggestion_cases.json": frozenset({
        "noreply" + "@alerts.jobs2web.com", "updates" + "@jobs2web.com.evil",
    }),
}


def _is_allowed_email_domain(value: str) -> bool:
    domain = value.casefold().rstrip(".")
    return domain in ALLOWED_EMAIL_DOMAINS or domain.endswith(
        ALLOWED_EMAIL_SUFFIXES
    )


def reviewed_binary_matches(relative_path: str, raw: bytes) -> bool:
    normalized = PurePosixPath(relative_path.replace("\\", "/")).as_posix()
    expected = REVIEWED_BINARY_SHA256.get(normalized)
    return expected is not None and hashlib.sha256(raw).hexdigest() == expected


def tracked_paths(project_root: Path) -> list[str]:
    result = subprocess.run(
        [
            "git",
            "-c",
            f"safe.directory={project_root.as_posix()}",
            "ls-files",
            "-z",
        ],
        cwd=project_root,
        check=True,
        capture_output=True,
    )
    return [
        item.decode("utf-8", errors="strict")
        for item in result.stdout.split(b"\0")
        if item
    ]


def path_violations(relative_path: str) -> list[str]:
    normalized = PurePosixPath(relative_path.replace("\\", "/")).as_posix()
    lowered = normalized.casefold()
    name = PurePosixPath(normalized).name.casefold()
    suffix = PurePosixPath(normalized).suffix.casefold()
    errors: list[str] = []

    if any(lowered.startswith(prefix.casefold()) for prefix in PRIVATE_PREFIXES):
        if name != ".gitkeep":
            errors.append("private data path is tracked")
    if suffix in PRIVATE_SUFFIXES or name in PRIVATE_NAMES:
        errors.append("private or binary artifact type is tracked")
    if any(lowered.startswith(prefix.casefold()) for prefix in RETIRED_DOC_PREFIXES):
        errors.append("retired documentation path is tracked")
    return errors


def _contains_personal_profile_path(text: str) -> bool:
    for pattern in PERSONAL_PATH_PATTERNS:
        for match in pattern.finditer(text):
            if match.group("profile").casefold() not in SYNTHETIC_PROFILE_NAMES:
                return True
    return False


def content_violations(
    text: str,
    *,
    allowed_email_addresses: frozenset[str] = frozenset(),
) -> list[str]:
    errors: list[str] = []
    for match in EMAIL_PATTERN.finditer(text):
        address = match.group(0).casefold()
        if (
            address not in ALLOWED_EMAIL_ADDRESSES
            and address not in allowed_email_addresses
            and not _is_allowed_email_domain(match.group(2))
        ):
            errors.append("non-example email address")
            break
    for match in PHONE_PATTERN.finditer(text):
        if match.group(1) != "555":
            errors.append("non-example phone number")
            break
    if _contains_personal_profile_path(text):
        errors.append("absolute user-profile path")
    return errors


def markdown_ticket_violations(relative_path: str, text: str) -> list[str]:
    """Reject ticket identifiers in tracked Markdown outside the exempt paths."""
    normalized = PurePosixPath(relative_path.replace("\\", "/")).as_posix()
    if PurePosixPath(normalized).suffix.casefold() != ".md":
        return []
    if normalized in TICKET_ID_EXEMPT_PATHS or normalized.startswith(
        TICKET_ID_EXEMPT_PREFIXES
    ):
        return []
    if TICKET_ID_PATTERN.search(text):
        return ["ticket identifier in documentation"]
    return []


def _content_errors(relative_path: str, raw: bytes) -> list[str]:
    """Judge one tracked file's bytes. Shared by the tree and revision audits."""
    if reviewed_binary_matches(relative_path, raw):
        return []
    if b"\0" in raw:
        return [f"{relative_path}: tracked binary content is not allowed"]
    try:
        text = raw.decode("utf-8", errors="strict")
    except UnicodeDecodeError:
        return [f"{relative_path}: tracked text must be UTF-8"]
    return [
        f"{relative_path}: {reason}"
        for reason in content_violations(
            text,
            allowed_email_addresses=PATH_ALLOWED_EMAIL_ADDRESSES.get(
                relative_path,
                frozenset(),
            ),
        )
        + markdown_ticket_violations(relative_path, text)
    ]


def audit_public_tree(project_root: Path) -> list[str]:
    """Audit the files Git tracks as they exist in the working tree."""
    project_root = project_root.resolve()
    errors: list[str] = []
    for relative_path in tracked_paths(project_root):
        for reason in path_violations(relative_path):
            errors.append(f"{relative_path}: {reason}")

        candidate = project_root / relative_path
        if candidate.is_symlink():
            errors.append(f"{relative_path}: tracked symbolic link is not allowed")
            continue
        path = candidate.resolve()
        try:
            path.relative_to(project_root)
        except ValueError:
            errors.append(f"{relative_path}: resolves outside repository")
            continue
        if not path.is_file():
            errors.append(f"{relative_path}: tracked path is not a regular file")
            continue
        if path.stat().st_size > MAX_TRACKED_FILE_BYTES:
            errors.append(
                f"{relative_path}: tracked file exceeds {MAX_TRACKED_FILE_BYTES} bytes"
            )
            continue
        errors.extend(_content_errors(relative_path, path.read_bytes()))
    return errors


class RevisionEntry(NamedTuple):
    """One `git ls-tree -r -l` row: the object a commit records at a path."""

    mode: str
    object_type: str
    sha: str
    size: int | None
    path: str


def _git(project_root: Path, *args: str, stdin: bytes = b"") -> bytes:
    result = subprocess.run(
        ["git", "-c", f"safe.directory={project_root.as_posix()}", *args],
        cwd=project_root,
        check=True,
        capture_output=True,
        input=stdin,
    )
    return result.stdout


def revision_entries(project_root: Path, revision: str) -> list[RevisionEntry]:
    """List what a commit records, without touching the working tree.

    Each row is `<mode> <type> <sha> <size>\\t<path>`; `-l` pads the size with
    spaces and prints `-` for non-blobs, and `-z` terminates rows with NUL so
    unusual path bytes cannot split a row.
    """
    entries: list[RevisionEntry] = []
    for row in _git(project_root, "ls-tree", "-r", "-l", "-z", revision).split(b"\0"):
        if not row:
            continue
        meta, _, path = row.partition(b"\t")
        mode, object_type, sha, size = meta.decode("ascii").split()
        entries.append(
            RevisionEntry(
                mode=mode,
                object_type=object_type,
                sha=sha,
                size=None if size == "-" else int(size),
                path=path.decode("utf-8", errors="strict"),
            )
        )
    return entries


def _entry_errors(entry: RevisionEntry) -> list[str]:
    """Decide whether an entry can be read as an ordinary file.

    Return the reasons it cannot; an empty list means "read and judge its
    bytes". This mirrors the symlink / regular-file / size checks the tree
    audit performs with the filesystem, using the Git object mode instead.
    Mode values: 100644 and 100755 are regular blobs, 120000 is a symbolic
    link, 160000 is a submodule (gitlink); `object_type` is "blob", "commit"
    or "tree". Keep error wording identical to the tree audit so the existing
    gate tests and CI output read the same in both modes. A symbolic link is
    reported as such even though Git stores it as a small blob, and a submodule
    is reported as not a regular file because its bytes live in another repo.
    """
    if entry.mode == "120000":
        return ["tracked symbolic link is not allowed"]
    if entry.object_type != "blob" or entry.size is None:
        return ["tracked path is not a regular file"]
    if entry.size > MAX_TRACKED_FILE_BYTES:
        return [f"tracked file exceeds {MAX_TRACKED_FILE_BYTES} bytes"]
    return []


def revision_blobs(project_root: Path, shas: list[str]) -> dict[str, bytes]:
    """Fetch many blobs in one `git cat-file --batch` call.

    One subprocess per file is too slow for a hook that loops over commits.
    Batch output is `<sha> <type> <size>\\n<bytes>\\n` per requested object.
    """
    if not shas:
        return {}
    output = _git(
        project_root,
        "cat-file",
        "--batch",
        stdin=("\n".join(shas) + "\n").encode("ascii"),
    )
    blobs: dict[str, bytes] = {}
    offset = 0
    while offset < len(output):
        header_end = output.index(b"\n", offset)
        sha, object_type, size = output[offset:header_end].decode("ascii").split()
        if object_type == "missing":
            raise ValueError(f"object {sha} is missing from the repository")
        start = header_end + 1
        end = start + int(size)
        blobs[sha] = output[start:end]
        offset = end + 1  # trailing newline after each object
    return blobs


def audit_revision(project_root: Path, revision: str) -> list[str]:
    """Audit the files a commit records, ignoring the working tree entirely."""
    project_root = project_root.resolve()
    errors: list[str] = []
    readable: list[RevisionEntry] = []
    for entry in revision_entries(project_root, revision):
        for reason in path_violations(entry.path):
            errors.append(f"{entry.path}: {reason}")
        entry_errors = _entry_errors(entry)
        if entry_errors:
            errors.extend(f"{entry.path}: {reason}" for reason in entry_errors)
            continue
        readable.append(entry)
    blobs = revision_blobs(project_root, [entry.sha for entry in readable])
    for entry in readable:
        errors.extend(_content_errors(entry.path, blobs[entry.sha]))
    return errors


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Fail when Git tracks private Career Hub data or personal artifacts."
    )
    parser.add_argument(
        "--rev",
        metavar="COMMIT",
        help=(
            "audit the files recorded by this commit instead of the working tree; "
            "use it to check what a push or export would actually send"
        ),
    )
    args = parser.parse_args(argv)
    project_root = Path(__file__).resolve().parents[1]
    label = f" at {args.rev}" if args.rev else ""
    try:
        if args.rev:
            errors = audit_revision(project_root, args.rev)
        else:
            errors = audit_public_tree(project_root)
    except (OSError, subprocess.CalledProcessError, UnicodeError, ValueError) as exc:
        print(f"Public-tree audit{label} failed to run: {type(exc).__name__}: {exc}")
        return 2
    if errors:
        print(f"Public-tree audit{label} failed:")
        for error in errors:
            print(f"- {error}")
        return 1
    print(f"Public-tree audit{label} passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
