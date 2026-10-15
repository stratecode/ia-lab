#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import unittest


SCRIPT = Path(__file__).with_name("probe-secretary-rovo-context.py")
SPEC = importlib.util.spec_from_file_location("rovo_probe", SCRIPT)
probe = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(probe)


class RovoProbeTests(unittest.TestCase):
    def test_search_payload_matches_secretary_read_only_rovo_call(self):
        query = "Iconos de señales de tráfico; no urgente ahora."
        self.assertEqual(
            probe.build_search_arguments("cloud-123", query),
            {
                "cloudId": "cloud-123",
                "query": query,
                "targetApp": "JIRA",
                "mode": "STANDARD",
                "limit": 10,
            },
        )

    def test_provider_error_output_redacts_access_token(self):
        token = "private-bearer-token"
        error = f"HTTP 403: invalid token {token}"
        self.assertNotIn(token, probe.redact_secrets(error, [token]))
        self.assertIn("[REDACTED]", probe.redact_secrets(error, [token]))


if __name__ == "__main__":
    unittest.main()
