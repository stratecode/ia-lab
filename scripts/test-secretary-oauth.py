import importlib.util
from pathlib import Path
import unittest
import tempfile
import json
import stat

spec = importlib.util.spec_from_file_location("secretary_oauth", Path(__file__).with_name("connect-secretary.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class OAuthTests(unittest.TestCase):
    def test_metadata_path_for_issuer_with_path(self):
        self.assertEqual(module.metadata_url("https://mcp-auth.granola.ai/client"),
                         "https://mcp-auth.granola.ai/.well-known/oauth-authorization-server/client")

    def test_provider_endpoints_are_fixed(self):
        with self.assertRaises(ValueError):
            module.validate_endpoint("granola", "http://mcp-auth.granola.ai/token")

    def test_callback_requires_matching_state_and_code(self):
        self.assertEqual(module.callback_code({"state": ["expected"], "code": ["code"]}, "expected"), "code")
        for query in [{"code": ["code"]}, {"state": ["wrong"], "code": ["code"]}, {"state": ["expected"]}]:
            with self.assertRaises(ValueError):
                module.callback_code(query, "expected")

    def test_callback_uri_supports_public_https_origin_for_granola(self):
        origin = "https://assistant.stratecode.com"
        self.assertEqual(module.callback_uri("granola", 18766, origin),
                         "https://assistant.stratecode.com/oauth/granola/callback")

    def test_callback_uri_preserves_localhost_default(self):
        self.assertEqual(module.callback_uri("granola", 18765, None),
                         "http://127.0.0.1:18765/callback")

    def test_public_callback_origin_must_be_exact_trusted_https_origin(self):
        for origin in (
            "http://assistant.stratecode.com",
            "https://assistant.stratecode.com/",
            "https://assistant.stratecode.com/path",
            "https://attacker.example",
            "https://user@assistant.stratecode.com",
            "https://assistant.stratecode.com:443",
        ):
            with self.subTest(origin=origin), self.assertRaises(ValueError):
                module.callback_uri("granola", 18765, origin)

    def test_secret_file_is_private_and_preserves_other_provider(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'connectors.json'
            module.save_config(path, 'granola', {'access_token': 'synthetic-granola'})
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            self.assertEqual(set(json.loads(path.read_text())), {'granola'})

    def test_secret_file_cannot_be_written_in_repository(self):
        with self.assertRaises(ValueError):
            module.save_config(Path(__file__).with_name('oauth-private-test.json'), 'granola', {})


if __name__ == "__main__":
    unittest.main()
