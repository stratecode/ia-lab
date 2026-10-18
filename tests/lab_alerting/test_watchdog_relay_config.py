from pathlib import Path
import os
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


class WatchdogRelayConfigTests(unittest.TestCase):
    def test_alertmanager_technical_alerts_default_to_watchdog_and_donna_is_opt_in(self):
        alertmanager = (ROOT / "roles/lab_alerting/templates/alertmanager.yml.j2").read_text()

        self.assertIn("receiver: telegram-watchdog", alertmanager)
        self.assertIn('matchers: [notify_donna="true"]', alertmanager)
        self.assertIn("receiver: telegram-donna", alertmanager)
        self.assertIn("continue: true", alertmanager)
        self.assertIn("- receiver: telegram-watchdog", alertmanager)
        self.assertIn("bot_token_file: {{ lab_alerting_watchdog_telegram_token_file }}", alertmanager)
        self.assertIn('bot_token_file: /etc/prometheus/telegram_bot_token', alertmanager)

    def test_watchdog_token_is_installed_privately_from_existing_env_variable(self):
        defaults = (ROOT / "roles/lab_alerting/defaults/main.yml").read_text()
        tasks = (ROOT / "roles/lab_alerting/tasks/main.yml").read_text()
        env_example = (ROOT / ".env.example").read_text()
        docs = (ROOT / "docs/monitoring.md").read_text()

        self.assertIn("lookup('env', 'LAB_WATCHDOG_TELEGRAM_TOKEN')", defaults)
        task_start = tasks.index("- name: Install Watchdog Telegram bot token file")
        task_end = tasks.index("\n    - name:", task_start + 1)
        install_task = tasks[task_start:task_end]
        self.assertIn('dest: "{{ lab_alerting_watchdog_telegram_token_file }}"', install_task)
        self.assertIn("mode: \"0640\"", install_task)
        self.assertIn("no_log: true", install_task)
        self.assertIn("lab_alerting_watchdog_telegram_token_file: /etc/prometheus/watchdog_telegram_bot_token", defaults)
        self.assertIn("LAB_WATCHDOG_TELEGRAM_TOKEN=replace-with-watchdog-bot-token", env_example)
        self.assertIn("LAB_WATCHDOG_TELEGRAM_TOKEN", docs)
        self.assertIn("al `.env` de la raíz del checkout local", docs)

    def test_alertmanager_endpoint_is_post_only_and_pi_scoped_before_grafana(self):
        nginx = (ROOT / "roles/monitor/tasks/main.yml").read_text()
        route_index = nginx.index("location = /alertmanager/api/v2/alerts {")
        server_start = nginx.rfind("      server {", 0, route_index)
        server_end = nginx.index("\n      server {", route_index)
        server = nginx[server_start:server_end]
        route_start = server.index("location = /alertmanager/api/v2/alerts {")
        grafana_start = server.index("location / {")
        route = server[route_start:grafana_start]

        self.assertLess(route_start, grafana_start)
        for directive in (
            "allow {{ lab_watchdog_pi_ip }};",
            "allow 127.0.0.1;",
            "deny all;",
            "limit_except POST { deny all; }",
            "proxy_pass http://127.0.0.1:9093/api/v2/alerts;",
        ):
            with self.subTest(directive=directive):
                self.assertIn(directive, route)

    def test_chrony_and_ufw_are_separately_opted_in_from_relay(self):
        tasks = (ROOT / "roles/lab_alerting/tasks/main.yml").read_text()
        defaults = (ROOT / "roles/lab_alerting/defaults/main.yml").read_text()
        chrony_start = tasks.index("- name: Configure chrony for the external watchdog")
        chrony_end = tasks.index("# ---------------------------------------------------------------- Reglas de Prometheus", chrony_start)
        chrony_block = tasks[chrony_start:chrony_end]

        self.assertIn("lab_watchdog_relay_chrony_enabled", chrony_block)
        self.assertIn("lab_watchdog_relay_chrony_enabled: false", defaults)
        self.assertIn("lab_watchdog_manage_ufw | default(false) | bool", chrony_block)

    def test_nginx_config_validation_is_read_only_in_ansible_check_mode(self):
        tasks = (ROOT / "roles/monitor/tasks/main.yml").read_text()
        validation_start = tasks.index("- name: Validate nginx configuration, including the watchdog endpoint")
        validation_end = tasks.index("\n- name:", validation_start + 1)
        validation = tasks[validation_start:validation_end]

        self.assertIn("ansible.builtin.command: nginx -t", validation)
        self.assertIn("check_mode: false", validation)

    def test_first_check_run_skips_activation_of_not_yet_installed_boot_units(self):
        tasks = (ROOT / "roles/lab_alerting/tasks/main.yml").read_text()
        timer_start = tasks.index("- name: Enable and start timers and path units")
        marker_start = tasks.index("- name: Enable clean shutdown marker service")
        baseline_start = tasks.index("- name: Establish boot state baseline and publish initial metrics")
        grafana_start = tasks.index("# ---------------------------------------------------------------- Grafana")
        timer_task = tasks[timer_start:marker_start]
        marker_task = tasks[marker_start:baseline_start]
        baseline_task = tasks[baseline_start:grafana_start]

        self.assertIn("ansible_check_mode", timer_task)
        self.assertIn("ansible_check_mode", marker_task)
        self.assertIn("ansible_check_mode", baseline_task)

    def test_promtool_validator_stages_rules_inside_snap_accessible_directory(self):
        tasks = (ROOT / "roles/lab_alerting/tasks/main.yml").read_text()
        script = ROOT / "roles/lab_alerting/files/bin/lab-alerting-promtool-check"
        self.assertIn("validate: /usr/local/bin/lab-alerting-promtool-check %s", tasks)
        self.assertTrue(script.exists())

        with tempfile.TemporaryDirectory() as tmpdir:
            validation_dir = Path(tmpdir) / "promreg"
            validation_dir.mkdir()
            rules = Path(tmpdir) / "rules.yml"
            rules.write_text("groups: []\n")
            recorder = Path(tmpdir) / "promtool-recorder"
            recorder.write_text(
                "#!/usr/bin/env python3\n"
                "import pathlib, sys\n"
                "assert sys.argv[1:3] == ['check', 'rules']\n"
                "path = pathlib.Path(sys.argv[3])\n"
                "assert path.parent == pathlib.Path(__import__('os').environ['LAB_PROMTOOL_VALIDATION_DIR'])\n"
                "assert path.read_text() == 'groups: []\\n'\n"
            )
            recorder.chmod(0o755)
            result = subprocess.run(
                [str(script), str(rules)],
                env={
                    **os.environ,
                    "LAB_PROMTOOL_BIN": str(recorder),
                    "LAB_PROMTOOL_VALIDATION_DIR": str(validation_dir),
                },
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(list(validation_dir.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
