from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[2]


class WatchdogRelayConfigTests(unittest.TestCase):
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


if __name__ == "__main__":
    unittest.main()
