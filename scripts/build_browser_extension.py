"""Build reproducible Career Hub extension packages for desktop browsers."""

from __future__ import annotations

import argparse
from copy import deepcopy
import json
from pathlib import Path
import shutil
import stat
import zipfile


PROJECT_ROOT = Path(__file__).resolve().parents[1]
EXTENSION_ROOT = PROJECT_ROOT / "extension"
DEFAULT_OUTPUT_ROOT = PROJECT_ROOT / "outputs" / "browser_extensions"
TARGETS = ("chrome", "edge", "firefox", "safari")
BACKGROUND_SCRIPTS = [
    "browser-api.js",
    "helper-catalog.js",
    "page-extractor.js",
    "application-session-observer.js",
    "application-form-helper.js",
    "application-answer-helper.js",
    "service-worker.js",
]
PACKAGE_SUFFIXES = frozenset({".css", ".html", ".js", ".png", ".svg"})
FIREFOX_EXTENSION_ID = "career-hub-capture@example.com"
FIREFOX_REQUIRED_DATA_COLLECTION = (
    "personallyIdentifyingInfo",
    "locationInfo",
    "browsingActivity",
    "websiteContent",
    "websiteActivity",
)


def _read_base_manifest() -> dict[str, object]:
    manifest = json.loads(
        (EXTENSION_ROOT / "manifest.json").read_text(encoding="utf-8")
    )
    if manifest.get("manifest_version") != 3:
        raise ValueError("Career Hub browser packages require Manifest V3")
    return manifest


def _without_permission(manifest: dict[str, object], permission: str) -> None:
    permissions = manifest.get("permissions")
    if not isinstance(permissions, list):
        raise ValueError("Extension permissions must be a list")
    manifest["permissions"] = [value for value in permissions if value != permission]


def manifest_for_target(target: str) -> dict[str, object]:
    """Return one target manifest without weakening the shared permission set."""

    if target not in TARGETS:
        raise ValueError(f"Unsupported browser target: {target}")
    manifest = deepcopy(_read_base_manifest())
    if target in {"chrome", "edge"}:
        return manifest

    manifest.pop("key", None)
    manifest.pop("minimum_chrome_version", None)
    manifest.pop("side_panel", None)
    _without_permission(manifest, "sidePanel")
    manifest["background"] = {"scripts": list(BACKGROUND_SCRIPTS)}

    if target == "firefox":
        manifest["incognito"] = "not_allowed"
        manifest["sidebar_action"] = {
            "default_title": "Career Hub Application Helper",
            "default_panel": "sidepanel.html",
            "default_icon": {
                "16": "icons/icon-16.png",
                "32": "icons/icon-32.png",
            },
        }
        manifest["browser_specific_settings"] = {
            "gecko": {
                "id": FIREFOX_EXTENSION_ID,
                # Firefox 140 introduced the built-in data collection
                # declaration required for new AMO submissions.
                "strict_min_version": "140.0",
                "data_collection_permissions": {
                    "required": list(FIREFOX_REQUIRED_DATA_COLLECTION)
                },
            }
        }
    else:
        # Safari 17+ runs these as a nonpersistent background document. The
        # helper opens in a dedicated extension tab because Safari has no
        # compatible Chrome/Firefox side-panel API.
        background = manifest["background"]
        if not isinstance(background, dict):
            raise ValueError("Safari background configuration must be an object")
        background["persistent"] = False
        manifest["browser_specific_settings"] = {
            "safari": {"strict_min_version": "17.0"}
        }
    return manifest


def _package_sources() -> list[Path]:
    sources: list[Path] = []
    for path in sorted(EXTENSION_ROOT.rglob("*")):
        if path.is_symlink():
            raise ValueError(f"Extension packages may not contain symlinks: {path}")
        if not path.is_file() or path.name == "manifest.json":
            continue
        if path.suffix.lower() not in PACKAGE_SUFFIXES:
            raise ValueError(f"Unsupported extension package file: {path}")
        sources.append(path)
    required = {EXTENSION_ROOT / item for item in BACKGROUND_SCRIPTS}
    missing = sorted(str(path) for path in required.difference(sources))
    if missing:
        raise FileNotFoundError(f"Missing background source files: {', '.join(missing)}")
    return sources


def _prepare_destination(path: Path, *, overwrite: bool) -> None:
    if not path.exists():
        return
    if not overwrite:
        raise FileExistsError(f"Build output already exists: {path}")
    if path.is_dir():
        shutil.rmtree(path)
    else:
        path.unlink()


def _write_reproducible_archive(source_root: Path, archive_path: Path) -> None:
    with zipfile.ZipFile(
        archive_path,
        "w",
        compression=zipfile.ZIP_DEFLATED,
        compresslevel=9,
    ) as archive:
        for path in sorted(item for item in source_root.rglob("*") if item.is_file()):
            relative = path.relative_to(source_root).as_posix()
            info = zipfile.ZipInfo(relative, date_time=(2020, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = (stat.S_IFREG | 0o644) << 16
            archive.writestr(info, path.read_bytes())


def build_target(
    target: str,
    output_root: Path,
    *,
    overwrite: bool = False,
) -> tuple[Path, Path]:
    """Build one unpacked directory and its store-upload archive."""

    manifest = manifest_for_target(target)
    version = manifest.get("version")
    if not isinstance(version, str) or not version:
        raise ValueError("Extension version is missing")

    target_root = output_root / target
    archive_suffix = ".xpi" if target == "firefox" else ".zip"
    archive_path = output_root / f"career-hub-capture-{target}-{version}{archive_suffix}"
    _prepare_destination(target_root, overwrite=overwrite)
    _prepare_destination(archive_path, overwrite=overwrite)
    target_root.mkdir(parents=True)

    for source in _package_sources():
        destination = target_root / source.relative_to(EXTENSION_ROOT)
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, destination)
    (target_root / "manifest.json").write_text(
        json.dumps(manifest, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )
    _write_reproducible_archive(target_root, archive_path)
    return target_root, archive_path


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--target",
        action="append",
        choices=TARGETS,
        help="Browser target to build; repeat as needed. Defaults to all targets.",
    )
    parser.add_argument(
        "--output-root",
        type=Path,
        default=DEFAULT_OUTPUT_ROOT,
        help="Directory for unpacked builds and archives.",
    )
    parser.add_argument(
        "--overwrite",
        action="store_true",
        help="Replace existing generated outputs for the selected targets.",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    targets = tuple(dict.fromkeys(args.target or TARGETS))
    output_root = args.output_root.resolve()
    output_root.mkdir(parents=True, exist_ok=True)
    for target in targets:
        target_root, archive_path = build_target(
            target,
            output_root,
            overwrite=args.overwrite,
        )
        print(f"{target}: {target_root}")
        print(f"{target} archive: {archive_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
