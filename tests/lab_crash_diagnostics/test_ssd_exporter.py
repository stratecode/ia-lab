"""Tests for roles/lab_crash_diagnostics/files/lab-ssd-textfile-exporter.

No disk, no smartctl and no root are needed: smartctl and lsblk are replaced by fake outputs.
Run:  python3 -m unittest discover -s tests/lab_crash_diagnostics
"""
import importlib.machinery
import importlib.util
import os
import re
import stat
import sys
import tempfile
import types
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "..", "..", "roles", "lab_crash_diagnostics", "files", "lab-ssd-textfile-exporter")
loader = importlib.machinery.SourceFileLoader("lab_ssd_exporter", SCRIPT)
spec = importlib.util.spec_from_loader("lab_ssd_exporter", loader)
exporter = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = exporter
loader.exec_module(exporter)

# Real `smartctl -A` output of the lab's SSD (KY510-1TB), 19-oct-2026.
SMARTCTL = """smartctl 7.4 2023-08-01 r5530 [x86_64-linux-7.0.0-38-generic] (local build)
Copyright (C) 2002-23, Bruce Allen, Christian Franke, www.smartmontools.org

=== START OF READ SMART DATA SECTION ===
SMART Attributes Data Structure revision number: 1
Vendor Specific SMART Attributes with Thresholds:
ID# ATTRIBUTE_NAME          FLAG     VALUE WORST THRESH TYPE      UPDATED  WHEN_FAILED RAW_VALUE
  5 Reallocated_Sector_Ct   0x0032   100   100   050    Old_age   Always       -       0
  9 Power_On_Hours          0x0032   100   100   050    Old_age   Always       -       3464
 12 Power_Cycle_Count       0x0032   100   100   050    Old_age   Always       -       148
175 Program_Fail_Count_Chip 0x0032   100   100   050    Old_age   Always       -       16777216
192 Power-Off_Retract_Count 0x0032   100   100   050    Old_age   Always       -       102
194 Temperature_Celsius     0x0032   100   100   050    Old_age   Always       -       31 (Min/Max 20/45)
"""
LSBLK = """loop0 loop
loop1 loop
sr0 rom
sda disk
"""
PROM_LINE = re.compile(r'^(# (HELP|TYPE) .*|[a-z_]+(\{[^}]*\})? -?\d+(\.\d+)?)$')


def fake_run(stdout_by_command, calls=None):
    def run(cmd, **kwargs):
        if calls is not None:
            calls.append(cmd)
        out = stdout_by_command.get(cmd[0])
        if isinstance(out, Exception):
            raise out
        return types.SimpleNamespace(stdout=out or "", returncode=0)
    return run


class ParsingTests(unittest.TestCase):
    def test_reads_the_attributes_that_matter_with_their_real_values(self):
        found = exporter.parse_attributes(SMARTCTL, {9, 12, 192})
        self.assertEqual(found, {9: ("Power_On_Hours", 3464), 12: ("Power_Cycle_Count", 148),
                                 192: ("Power-Off_Retract_Count", 102)})

    def test_ignores_the_other_attributes_and_the_header(self):
        self.assertEqual(set(exporter.parse_attributes(SMARTCTL, {5})), {5})
        self.assertEqual(exporter.parse_attributes(SMARTCTL, {250}), {})

    def test_a_raw_value_with_trailing_text_gives_the_leading_number(self):
        self.assertEqual(exporter.parse_attributes(SMARTCTL, {194}), {194: ("Temperature_Celsius", 31)})

    def test_garbage_gives_nothing(self):
        self.assertEqual(exporter.parse_attributes("", {9}), {})
        self.assertEqual(exporter.parse_attributes("Smartctl open device: /dev/sda failed: Permission denied", {9}), {})

    def test_finds_the_first_real_disk_and_skips_loops_and_optical_drives(self):
        self.assertEqual(exporter.find_device(LSBLK), "/dev/sda")
        self.assertEqual(exporter.find_device("loop0 loop\nnvme0n1 disk\n"), "/dev/nvme0n1")
        self.assertIsNone(exporter.find_device("loop0 loop\nsr0 rom\n"))
        self.assertIsNone(exporter.find_device(""))


class RenderingTests(unittest.TestCase):
    def test_the_prometheus_text_format(self):
        attrs = exporter.parse_attributes(SMARTCTL, {9, 12, 192})
        text = exporter.render("/dev/sda", attrs, 1792361978, True)
        self.assertTrue(text.endswith("\n"))
        for line in text.splitlines():
            self.assertRegex(line, PROM_LINE, line)
        self.assertIn("lab_ssd_smart_up 1", text)
        self.assertIn('lab_ssd_smart_attribute_raw{device="sda",id="192",name="Power-Off_Retract_Count"} 102', text)
        self.assertIn('lab_ssd_smart_attribute_raw{device="sda",id="12",name="Power_Cycle_Count"} 148', text)
        self.assertIn("lab_ssd_smart_last_success_timestamp_seconds 1792361978", text)

    def test_when_smart_cannot_be_read_it_says_so_and_has_no_values(self):
        text = exporter.render("unknown", {}, 1792361978, False)
        self.assertIn("lab_ssd_smart_up 0", text)
        self.assertNotIn("lab_ssd_smart_attribute_raw{", text)
        self.assertNotIn("last_success", text)  # the timestamp must not move on a failed read


class MainTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.out = os.path.join(self.tmp.name, "lab-ssd.prom")

    def read(self):
        with open(self.out, encoding="utf-8") as handle:
            return handle.read()

    def test_auto_detects_the_disk_and_writes_the_metrics(self):
        calls = []
        run = fake_run({"lsblk": LSBLK, "smartctl": SMARTCTL}, calls)
        self.assertEqual(exporter.main(["--out", self.out], run=run, now=lambda: 1792361978), 0)
        self.assertEqual(calls[1], ["smartctl", "-A", "/dev/sda"])
        self.assertIn('id="192"', self.read())
        self.assertIn("lab_ssd_smart_up 1", self.read())

    def test_an_explicit_device_skips_lsblk(self):
        calls = []
        exporter.main(["--device", "/dev/sdb", "--out", self.out], run=fake_run({"smartctl": SMARTCTL}, calls), now=lambda: 1)
        self.assertEqual(calls, [["smartctl", "-A", "/dev/sdb"]])
        self.assertIn('device="sdb"', self.read())

    def test_only_the_requested_attributes_are_exported(self):
        exporter.main(["--device", "/dev/sda", "--attributes", "192", "--out", self.out], run=fake_run({"smartctl": SMARTCTL}), now=lambda: 1)
        text = self.read()
        self.assertIn('id="192"', text)
        self.assertNotIn('id="9"', text)

    def test_a_missing_smartctl_does_not_fail_the_timer(self):
        run = fake_run({"lsblk": LSBLK, "smartctl": FileNotFoundError("smartctl")})
        self.assertEqual(exporter.main(["--out", self.out], run=run, now=lambda: 1), 0)
        self.assertIn("lab_ssd_smart_up 0", self.read())

    def test_smartctl_output_without_attributes_is_reported_as_down_not_as_ok(self):
        # smartctl runs but prints an error (no permission, USB bridge, SMART off): "up" must not claim success.
        run = fake_run({"lsblk": LSBLK, "smartctl": "Smartctl open device: /dev/sda failed: Permission denied\n"})
        exporter.main(["--out", self.out], run=run, now=lambda: 1)
        text = self.read()
        self.assertIn("lab_ssd_smart_up 0", text)
        self.assertNotIn("lab_ssd_smart_attribute_raw{", text)

    def test_a_hung_disk_does_not_fail_the_timer(self):
        import subprocess
        run = fake_run({"lsblk": LSBLK, "smartctl": subprocess.TimeoutExpired("smartctl", 30)})
        self.assertEqual(exporter.main(["--out", self.out], run=run, now=lambda: 1), 0)
        self.assertIn("lab_ssd_smart_up 0", self.read())

    def test_no_disk_at_all_is_reported_as_down(self):
        exporter.main(["--out", self.out], run=fake_run({"lsblk": "loop0 loop\n"}), now=lambda: 1)
        self.assertIn("lab_ssd_smart_up 0", self.read())

    def test_the_file_is_replaced_atomically_and_readable_by_node_exporter(self):
        run = fake_run({"smartctl": SMARTCTL})
        exporter.main(["--device", "/dev/sda", "--out", self.out], run=run, now=lambda: 1)
        exporter.main(["--device", "/dev/sda", "--out", self.out], run=run, now=lambda: 2)
        self.assertEqual(os.listdir(self.tmp.name), ["lab-ssd.prom"])  # no temporary file left behind
        self.assertEqual(stat.S_IMODE(os.stat(self.out).st_mode), 0o644)
        self.assertIn("last_success_timestamp_seconds 2", self.read())


if __name__ == "__main__":
    unittest.main()
