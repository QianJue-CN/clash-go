import hashlib
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import release


class ReleaseTests(unittest.TestCase):
    def test_tag_validation(self):
        self.assertEqual(release.version_from_tag("v0.2.0"), "0.2.0")
        for tag in ("main", "1.2.3", "v01.2.3", "v1.2", "v1.2.3-rc.1", "v65536.0.0", "v1.2.3\n", "v1.2.3;echo bad"):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                release.version_from_tag(tag)

    def test_collect_all_targets_and_manifest(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            packages, output = root / "packages", root / "release"
            for target in release.TARGETS:
                directory = packages / target
                directory.mkdir(parents=True)
                arch = target.split("-")[1]
                if target.startswith("linux-"):
                    names = [f"clash-go_1.2.3_{arch}.deb", f"clash-go-1.2.3-{target}.tar.gz", "install.sh"]
                elif target.startswith("windows-"):
                    names = [f"Clash Go Setup 1.2.3 {arch}.exe"]
                else:
                    names = [f"Clash Go 1.2.3 {arch}.dmg"]
                for name in names:
                    (directory / name).write_bytes(b"fixture")
                release.collect("v1.2.3", target, packages, output)
            self.assertEqual(len(list(output.iterdir())), 10)
            release.manifest("v1.2.3", output)
            lines = (output / "SHA256SUMS").read_text().splitlines()
            self.assertEqual(len(lines), 10)
            self.assertTrue(all(line.startswith(hashlib.sha256(b"fixture").hexdigest() + "  ") for line in lines))
            with self.assertRaises(ValueError):
                release.collect("v1.2.3", "linux-amd64", packages, output)
            (output / "unexpected.exe").write_bytes(b"unexpected")
            with self.assertRaises(ValueError):
                release.manifest("v1.2.3", output)
            (output / "unexpected.exe").unlink()
            (output / release.asset_names("1.2.3", "windows-arm64")[0]).unlink()
            with self.assertRaises(ValueError):
                release.manifest("v1.2.3", output)

    def test_missing_installer_is_fatal(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            with self.assertRaises(ValueError):
                release.collect("v1.2.3", "windows-amd64", root, root / "out")
            self.assertFalse((root / "out").exists())

    def test_prepare_version_and_dependency_licenses(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            go = root / "go"
            module = root / "dependency"
            go.mkdir()
            module.mkdir()
            (go / "LICENSE").write_text("Go license")
            (module / "LICENSE").write_text("Dependency license")
            (module / "NOTICE").write_text("Dependency notice")
            config = {"version": "0.1.0", "linux": {"webview": False}}
            (root / "mygo.json").write_text(json.dumps(config))
            modules = json.dumps({"Module": {"Main": True}}) + json.dumps({"Module": {"Path": "example/module", "Version": "v1.0.0", "Dir": str(module)}})
            previous = Path.cwd()
            try:
                os.chdir(root)
                with patch.object(release, "go_output", side_effect=[str(go), modules]):
                    release.prepare("v1.2.3", "linux-amd64")
                result = json.loads(Path("mygo.json").read_text())
                self.assertEqual(result["version"], "1.2.3")
                self.assertFalse(result["linux"]["webview"])
                notices = Path("build/THIRD_PARTY_LICENSES.txt").read_text()
                for text in ("Go license", "example/module v1.0.0", "Dependency license", "Dependency notice"):
                    self.assertIn(text, notices)
            finally:
                os.chdir(previous)


if __name__ == "__main__":
    unittest.main()
