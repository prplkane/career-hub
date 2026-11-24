"""Prepare Windows release assets and advance the anonymous Stable update manifest."""

import argparse
import hashlib
import json
import re
import shutil
import subprocess
import tempfile
import xml.etree.ElementTree as ET
import zipfile
from pathlib import Path

FEED_BRANCH = "windows-stable-feed"
LEGACY_VERSION = "0.3.2"
LEGACY_NAME = "CHDesktopDev-0.3.2-full.nupkg"
LEGACY_SHA1 = "BD25B5D4E7031B5B4C04A2102906A686911C1BCC"
LEGACY_SIZE = 177937795
SEMVER = r"(?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*)){2}"


def version_key(version):
    if not re.fullmatch(SEMVER, version):
        raise ValueError(f"Expected a three-part release version, got {version!r}")
    return tuple(map(int, version.split(".")))


def feed_url(repo):
    return f"https://raw.githubusercontent.com/{repo}/{FEED_BRANCH}"


def package_url(repo, version, name):
    return f"https://github.com/{repo}/releases/download/v{version}/{name}"


def entry(checksum, location, size):
    return f"{checksum.upper()} {location} {size}\n"


def legacy_manifest(repo):
    return entry(LEGACY_SHA1, package_url(repo, LEGACY_VERSION, LEGACY_NAME), LEGACY_SIZE)


def digest(path, algorithm):
    with path.open("rb") as source:
        return hashlib.file_digest(source, algorithm).hexdigest()


def write_text(path, content):
    path.write_text(content, encoding="utf-8", newline="\n")


def prepare(repo, channel, version, made, out):
    version_key(version)
    identity, title = {"stable": ("CHDesktop", "Career Hub"), "dev": ("CHDesktopDev", "Career Hub Dev")}[channel]
    name = f"{identity}-{version}-full.nupkg"
    package = made / name
    setup = made / f"{title}-{version} Setup.exe"
    expected = {name, setup.name, "RELEASES"}
    if {item.name for item in made.iterdir() if item.is_file()} != expected:
        raise ValueError(f"Expected exactly {sorted(expected)} in the Squirrel output")
    checksum = digest(package, "sha1")
    size = package.stat().st_size
    if (made / "RELEASES").read_text(encoding="utf-8-sig").splitlines() != [entry(checksum, name, size).strip()]:
        raise ValueError("Generated RELEASES disagrees with the package name, SHA-1 or size")
    with zipfile.ZipFile(package) as archive:
        spec = ET.fromstring(archive.read(f"{identity}.nuspec"))
        metadata = json.loads(archive.read("lib/net45/resources/career-hub/build-metadata.json"))
    if (spec.findtext("{*}metadata/{*}id"), spec.findtext("{*}metadata/{*}version")) != (identity, version):
        raise ValueError("NuGet package identity or version disagrees with the installer")
    expected_feed = feed_url(repo) if channel == "stable" else None
    if (metadata["channel"], metadata["app_version"], metadata["update_feed_url"]) != (channel, version, expected_feed):
        raise ValueError("Embedded build channel, version or update feed disagrees with the release")
    out.mkdir()  # A fresh directory prevents stale packages entering the upload.
    shutil.copyfile(package, out / name)
    shutil.copyfile(setup, out / f"CareerHub-{channel}-{version}-Setup.exe")
    if channel == "stable":
        write_text(out / "RELEASES-stable", entry(checksum, package_url(repo, version, name), size))
        write_text(out / "RELEASES", legacy_manifest(repo))
    else:
        shutil.copyfile(made / "RELEASES", out / "RELEASES")
    sums = "".join(f"{digest(item, 'sha256')}  {item.name}\n" for item in sorted(out.iterdir(), key=lambda p: p.name))
    write_text(out / "SHA256SUMS.txt", sums)


def stable_version(repo, manifest):
    match = re.fullmatch(r"([A-F0-9]{40}) (\S+) ([1-9][0-9]*)\n", manifest)
    if match:
        location = match[2]
        filename = location.rsplit("/", 1)[-1]
        package = re.fullmatch(rf"CHDesktop-({SEMVER})-full\.nupkg", filename)
        if package and location == package_url(repo, package[1], filename):
            return package[1]
    raise ValueError("Invalid Stable manifest or versioned package URL")


def command(*args, cwd=None):
    return subprocess.run(args, cwd=cwd, check=True, stdout=subprocess.PIPE, text=True).stdout.strip()


def checkout_feed(remote, directory):
    command("git", "init", "-q", "-b", FEED_BRANCH, cwd=directory)
    command("git", "remote", "add", "origin", remote, cwd=directory)
    ref = f"refs/heads/{FEED_BRANCH}"
    if not command("git", "ls-remote", "origin", ref, cwd=directory):
        return None
    command("git", "fetch", "-q", "--depth=1", "origin", ref, cwd=directory)
    command("git", "checkout", "-q", "-B", FEED_BRANCH, "FETCH_HEAD", cwd=directory)
    if command("git", "ls-files", cwd=directory) != "RELEASES":
        raise ValueError("Unexpected files on the Stable manifest branch")
    return (directory / "RELEASES").read_text(encoding="utf-8")


def advance_feed(repo, directory, previous, manifest):
    version = stable_version(repo, manifest)
    if previous is not None:
        if previous == manifest:
            return
        if version_key(stable_version(repo, previous)) >= version_key(version):
            raise ValueError("Refusing to rewind Stable or replace a published package at the same version")
    write_text(directory / "RELEASES", manifest)
    command("git", "add", "--", "RELEASES", cwd=directory)
    command("git", "-c", "user.name=github-actions[bot]", "-c", "user.email=41898282+github-actions[bot]@users.noreply.github.com",
            "commit", "-q", "-m", f"Publish Windows Stable {version}", cwd=directory)
    # A competing publisher cannot overwrite a newer ref.
    command("git", "push", "origin", f"HEAD:refs/heads/{FEED_BRANCH}", cwd=directory)


def published_manifest(repo, version, receipt):
    tag = f"v{version}"
    release = json.loads(command("gh", "release", "view", tag, "--repo", repo,
                                 "--json", "assets,isDraft,isPrerelease"))
    if release["isDraft"] or release["isPrerelease"]:
        raise ValueError("Stable requires a published, non-prerelease tag")
    assets = {asset["name"]: asset for asset in release["assets"]}
    name = f"CHDesktop-{version}-full.nupkg"
    expected = {"RELEASES", "RELEASES-stable", name, f"CareerHub-stable-{version}-Setup.exe"}
    if set(assets) != expected | {"SHA256SUMS.txt"}:
        raise ValueError("Unexpected published release assets")
    command("gh", "release", "download", tag, "--repo", repo, "--dir", str(receipt),
            "--pattern", "RELEASES*", "--pattern", "SHA256SUMS.txt")
    sums = "".join(f"{assets[n]['digest'].removeprefix('sha256:')}  {n}\n" for n in sorted(expected))
    if (receipt / "SHA256SUMS.txt").read_text() != sums:
        raise ValueError("Published checksums disagree with GitHub asset digests")
    for filename in ("RELEASES", "RELEASES-stable", "SHA256SUMS.txt"):
        if "sha256:" + digest(receipt / filename, "sha256") != assets[filename]["digest"]:
            raise ValueError(f"Published {filename} digest mismatch")
    if (receipt / "RELEASES").read_text() != legacy_manifest(repo):
        raise ValueError("The public latest manifest must remain compatible with Dev 0.3.2")
    manifest = (receipt / "RELEASES-stable").read_text()
    if stable_version(repo, manifest) != version or int(manifest.split()[2]) != assets[name]["size"]:
        raise ValueError("Published Stable manifest disagrees with the versioned package")
    return manifest


def publish(repo, version, assets, notes):
    tag = f"v{version}"
    with tempfile.TemporaryDirectory() as temp:
        work = Path(temp)
        previous = checkout_feed(f"https://github.com/{repo}.git", work)
        latest = command("gh", "api", f"repos/{repo}/releases/latest", "--jq", ".tag_name")
        if version_key(version) < version_key(latest.removeprefix("v")) or (
            previous and version_key(version) < version_key(stable_version(repo, previous))
        ):
            raise ValueError("A newer release already exists")
        tags = command("gh", "api", "--paginate", f"repos/{repo}/releases?per_page=100",
                       "--jq", '.[] | [.tag_name, (.draft | tostring)] | @tsv')
        existing = dict(line.split("\t") for line in tags.splitlines()).get(tag)
        files = [str(path) for path in sorted(assets.iterdir())]
        if existing is None:
            command("gh", "release", "create", tag, *files, "--repo", repo, "--verify-tag",
                    "--title", f"Career Hub {version}", "--notes-file", str(notes), "--latest")
        elif existing == "true":
            # A failed asset upload can leave a draft at this tag.
            command("gh", "release", "upload", tag, *files, "--repo", repo, "--clobber")
            command("gh", "release", "edit", tag, "--repo", repo, "--draft=false", "--latest", "--notes-file", str(notes))
        receipt = work / "receipt"
        receipt.mkdir()
        # On retry use the immutable files actually released, even if a rebuild differs.
        manifest = published_manifest(repo, version, receipt)
        advance_feed(repo, work, previous, manifest)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest="action", required=True)
    prepare_args = actions.add_parser("prepare")
    prepare_args.add_argument("channel", choices=["dev", "stable"])
    prepare_args.add_argument("made", type=Path)
    prepare_args.add_argument("out", type=Path)
    publish_args = actions.add_parser("publish")
    publish_args.add_argument("assets", type=Path)
    publish_args.add_argument("notes", type=Path)
    for action in (prepare_args, publish_args):
        action.add_argument("--repo", required=True)
        action.add_argument("--version", required=True)
    args = parser.parse_args()
    if args.action == "prepare":
        prepare(args.repo, args.channel, args.version, args.made, args.out)
    else:
        publish(args.repo, args.version, args.assets, args.notes)


if __name__ == "__main__":
    main()
