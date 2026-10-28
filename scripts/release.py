#!/usr/bin/env python3
"""Prepare MyGo builds and collect an exact, checksummed release asset set."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess


TARGETS = tuple(f"{os}-{arch}" for os in ("linux", "windows", "darwin") for arch in ("amd64", "arm64"))


def version_from_tag(tag):
    if not re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", tag):
        raise ValueError("release tag must be vMAJOR.MINOR.PATCH, e.g. v0.2.0")
    version = tag[1:]
    if any(int(part) > 65535 for part in version.split(".")):
        raise ValueError("version components must fit Windows version metadata (0–65535)")
    return version


def asset_names(version, target):
    if target not in TARGETS:
        raise ValueError(f"unsupported target: {target}")
    prefix = f"clash-go-{version}-{target}"
    if target.startswith("windows-"):
        return [prefix + "-setup.exe"]
    if target.startswith("darwin-"):
        return [prefix + ".dmg"]
    return [prefix + ".deb", prefix + ".tar.gz", prefix + "-install.sh"]


def go_output(*args, env=None):
    return subprocess.check_output(["go", *args], text=True, env=env)


def dependency_licenses(target):
    sections = []
    goroot = Path(go_output("env", "GOROOT").strip())
    sections.append("Go toolchain and standard library\n\n" + (goroot / "LICENSE").read_text())
    goos, goarch = target.split("-")
    remaining = go_output("list", "-deps", "-json", ".", env={**os.environ, "GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"}).strip()
    decoder = json.JSONDecoder()
    seen = set()
    while remaining:
        package, end = decoder.raw_decode(remaining)
        remaining = remaining[end:].lstrip()
        module = package.get("Module")
        if not module or module.get("Main") or module["Path"] in seen:
            continue
        seen.add(module["Path"])
        source = module.get("Replace", module)
        root = Path(source["Dir"])
        files = sorted(path for path in root.iterdir() if path.is_file() and
                       path.name.upper().startswith(("LICENSE", "COPYING", "NOTICE", "COPYRIGHT", "PATENTS")))
        if not files:
            raise ValueError(f"missing license for {module['Path']}")
        sections.append(f"{module['Path']} {module.get('Version', '')}\n\n" +
                        "\n\n".join(f"{path.name}\n{path.read_text()}" for path in files))
    return "\n\n" + ("\n\n" + "=" * 72 + "\n\n").join(sections) + "\n"


def prepare(tag, target):
    version = version_from_tag(tag)
    asset_names(version, target)
    licenses = dependency_licenses(target)
    config_path = Path("mygo.json")
    config = json.loads(config_path.read_text())
    config["version"] = version
    Path("build").mkdir(exist_ok=True)
    Path("build/THIRD_PARTY_LICENSES.txt").write_text(licenses, encoding="utf-8")
    config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def collect(tag, target, packages=Path("build/packages"), output=Path("build/release")):
    version = version_from_tag(tag)
    names = asset_names(version, target)
    arch = target.split("-")[1]
    if target.startswith("windows-"):
        sources = [f"Clash Go Setup {version} {arch}.exe"]
    elif target.startswith("darwin-"):
        sources = [f"Clash Go {version} {arch}.dmg"]
    else:
        sources = [f"clash-go_{version}_{arch}.deb", f"clash-go-{version}-{target}.tar.gz", "install.sh"]
    paths = [packages / target / source for source in sources]
    for path in paths:
        if not path.is_file() or path.stat().st_size == 0:
            raise ValueError(f"missing or empty package: {path}")
    output.mkdir(parents=True, exist_ok=True)
    for path, name in zip(paths, names):
        destination = output / name
        if destination.exists():
            raise ValueError(f"refusing to overwrite collected asset: {destination}")
        shutil.copy2(path, destination)


def manifest(tag, directory=Path("build/release")):
    version = version_from_tag(tag)
    expected = {name for target in TARGETS for name in asset_names(version, target)}
    actual = {path.name for path in directory.iterdir() if path.name != "SHA256SUMS"}
    if actual != expected:
        raise ValueError(f"release assets mismatch: missing={sorted(expected-actual)}, unexpected={sorted(actual-expected)}")
    lines = []
    for name in sorted(expected):
        path = directory / name
        if path.is_symlink() or not path.is_file() or path.stat().st_size == 0:
            raise ValueError(f"invalid release asset: {name}")
        with path.open("rb") as stream:
            digest = hashlib.file_digest(stream, "sha256").hexdigest()
        lines.append(f"{digest}  {name}\n")
    (directory / "SHA256SUMS").write_text("".join(lines), encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("prepare", "collect", "manifest"))
    parser.add_argument("tag")
    parser.add_argument("--target", choices=TARGETS)
    args = parser.parse_args()
    if args.action in ("prepare", "collect") and not args.target:
        parser.error(f"{args.action} requires --target")
    if args.action == "collect":
        collect(args.tag, args.target)
    elif args.action == "prepare":
        prepare(args.tag, args.target)
    else:
        manifest(args.tag)


if __name__ == "__main__":
    main()
