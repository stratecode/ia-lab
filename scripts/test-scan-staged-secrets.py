#!/usr/bin/env python3
"""Integration tests for the staged secret scanner, using isolated temp repos."""

from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path

SCANNER = Path(__file__).with_name("scan-staged-secrets.py").resolve()


def git(repo: Path, *args: str) -> None:
    subprocess.run(["git", *args], cwd=repo, check=True, stdout=subprocess.DEVNULL)


class StagedScannerTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.repo = Path(self.tmp.name)
        git(self.repo, "init", "-q")
        git(self.repo, "config", "user.email", "test@example.invalid")
        git(self.repo, "config", "user.name", "Scanner Test")

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def scan(self) -> subprocess.CompletedProcess[str]:
        return subprocess.run(["python3", str(SCANNER)], cwd=self.repo, text=True, capture_output=True)

    def stage(self, path: str, data: bytes) -> None:
        target = self.repo / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        git(self.repo, "add", "--", path)

    def test_private_key_match_rejected_without_content(self) -> None:
        marker = b"-----BEGIN " + b"PRIVATE KEY-----"
        secret_like = b"prefix\n" + marker + b"\nDO_NOT_PRINT\n"
        self.stage("fixture.txt", secret_like)
        result = self.scan()
        self.assertEqual(result.returncode, 1)
        self.assertIn("fixture.txt: private-key-marker", result.stderr)
        self.assertNotIn("DO_NOT_PRINT", result.stderr)

    def test_telegram_api_and_jwt_patterns_are_detected(self) -> None:
        self.stage("tokens.txt", b"1234567890:" + b"a" * 35 + b" AKIA" + b"A" * 16 + b" eyJ" + b"a" * 18 + b".eyJ" + b"b" * 18 + b".\n")
        result = self.scan()
        self.assertEqual(result.returncode, 1)
        for kind in ("telegram-bot-token", "api-key", "jwt"):
            self.assertIn(kind, result.stderr)

    def test_all_api_key_prefixes_are_detected(self) -> None:
        samples = [
            b"ghp_" + b"a" * 36,
            b"github_pat_" + b"b" * 40,
            b"sk-" + b"c" * 32,
        ]
        for index, sample in enumerate(samples):
            self.stage(f"api-{index}.txt", sample)
        result = self.scan()
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr.count("api-key"), len(samples))

    def test_credential_filename_patterns_are_detected(self) -> None:
        for path in ("ssh/lab", "keys/id_rsa", "keys/id_ed25519", "backup.kdbx", "backup.p12", "backup.pfx"):
            self.stage(path, b"ordinary placeholder\n")
        result = self.scan()
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr.count("credential-file"), 6)

    def test_secret_filenames_and_plain_vault_rejected(self) -> None:
        self.stage("config/.env.local", b"ordinary=example\n")
        self.stage("group_vars/vault.yml", b"password: test\n")
        result = self.scan()
        self.assertEqual(result.returncode, 1)
        self.assertIn("environment-file", result.stderr)
        self.assertIn("unencrypted-vault", result.stderr)

    def test_encrypted_vault_and_large_blob_are_allowed(self) -> None:
        self.stage("vault.yml", b"$ANSIBLE_VAULT;1.1;AES256\n" + b"x" * 40)
        self.stage("large.dat", b"-----BEGIN " + b"PRIVATE KEY-----\n" + b"x" * 1_048_600)
        result = self.scan()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_staged_added_modified_and_rename_destination(self) -> None:
        self.stage("old.txt", b"safe\n")
        git(self.repo, "commit", "-qm", "seed")
        (self.repo / "old.txt").write_text("still safe\n", encoding="utf-8")
        git(self.repo, "add", "old.txt")
        git(self.repo, "mv", "old.txt", "renamed.txt")
        self.stage("new.txt", b"ordinary content\n")
        self.stage("rename-source.txt", b"safe credential name\n")
        git(self.repo, "commit", "-qm", "seed rename")
        git(self.repo, "mv", "rename-source.txt", ".env")
        result = self.scan()
        self.assertEqual(result.returncode, 1)
        self.assertIn(".env: environment-file", result.stderr)


if __name__ == "__main__":
    unittest.main()
