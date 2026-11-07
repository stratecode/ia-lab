import asyncio
import importlib.util
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from threading import Thread
import unittest


MODULE_PATH = Path(__file__).with_name("donna_jira_actions.py")
SPEC = importlib.util.spec_from_file_location("donna_jira_actions", MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class JiraActionTests(unittest.TestCase):
    def setUp(self):
        self.requests = []
        self.preview_kind = "create"
        requests = self.requests
        test_case = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(inner_self):
                requests.append((inner_self.path, None, inner_self.headers))
                inner_self.send_response(200)
                inner_self.send_header("Content-Type", "application/json")
                inner_self.end_headers()
                inner_self.wfile.write(("{\"action_id\":\"action-1\",\"kind\":\"" + test_case.preview_kind + "\",\"payload_digest\":\"" + "a" * 64 + "\"}").encode())

            def do_POST(inner_self):
                length = int(inner_self.headers.get("Content-Length", "0"))
                payload = json.loads(inner_self.rfile.read(length))
                requests.append((inner_self.path, payload, inner_self.headers))
                inner_self.send_response(200)
                inner_self.send_header("Content-Type", "application/json")
                inner_self.end_headers()
                if inner_self.path == "/internal/jira-proposals/start":
                    inner_self.wfile.write(json.dumps({"message": "¿Qué proyecto?"}, ensure_ascii=False).encode())
                else:
                    inner_self.wfile.write(b'{"issue_key":"LAB-42","verified":true}')

            def log_message(inner_self, *_args):
                return

        self.server = HTTPServer(("127.0.0.1", 0), Handler)
        self.thread = Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.action = MODULE.Action()
        self.action.valves.service_url = f"http://127.0.0.1:{self.server.server_port}"
        self.action.valves.service_token = "s" * 40
        self.action.valves.operator_id = 17
        self.action.valves.admin_user_id = "admin-1"
        self.action.valves.secretary_url = f"http://127.0.0.1:{self.server.server_port}"

    def tearDown(self):
        self.server.shutdown()
        self.thread.join(timeout=2)
        self.server.server_close()

    def test_exact_digest_and_openwebui_principal_are_forwarded(self):
        content = "Propuesta: LAB task\n<!-- donna-jira-action:action-1:" + "a" * 64 + " -->"
        result = asyncio.run(self.action.action(
            {"id": "message-1", "chat_id": "chat-1", "content": content},
            "create",
            {"id": "admin-1", "role": "admin"},
        ))
        self.assertEqual(len(self.requests), 2)
        preview_path, _, preview_headers = self.requests[0]
        self.assertEqual(preview_path, "/v1/actions/action-1")
        self.assertEqual(preview_headers["X-Donna-Channel"], "openwebui")
        path, payload, headers = self.requests[1]
        self.assertEqual(path, "/v1/actions/action-1/apply")
        self.assertEqual(payload["payload_digest"], "a" * 64)
        self.assertEqual(headers["X-Donna-Operator-ID"], "17")
        self.assertEqual(headers["X-Donna-Channel"], "openwebui")
        self.assertEqual(headers["X-Donna-Conversation-ID"], "chat-1")
        self.assertIn("LAB-42", result["messages"][0]["content"])

    def test_other_admin_cannot_confirm_and_markerless_message_is_noop(self):
        content = "Proposal <!-- donna-jira-action:action-1:" + "a" * 64 + " -->"
        asyncio.run(self.action.action({"id": "m", "chat_id": "chat"}, {"id": "other", "role": "admin"}))
        asyncio.run(self.action.action({"id": "m", "chat_id": "chat", "content": "ordinary reply"}, {"id": "admin-1", "role": "admin"}))
        self.assertEqual(self.requests, [])

    def test_not_same_action_cancels_comment_draft_without_jira_write(self):
        self.preview_kind = "comment"
        content = "Coincidencia LAB-9 <!-- donna-jira-action:action-1:" + "a" * 64 + " -->"
        result = asyncio.run(self.action.action(
            {"id": "message-2", "chat_id": "chat-2", "content": content},
            "not_same",
            {"id": "admin-1", "role": "admin"},
        ))
        self.assertEqual(self.requests[0][0], "/v1/actions/action-1")
        self.assertEqual(self.requests[1][0], "/v1/actions/action-1/cancel")
        self.assertIn("descartado", result["messages"][0]["content"])

    def test_discard_action_cancels_create_draft_without_jira_write(self):
        content = "Propuesta LAB-42 <!-- donna-jira-action:action-1:" + "a" * 64 + " -->"
        result = asyncio.run(self.action.action(
            {"id": "message-4", "chat_id": "chat-4", "content": content},
            "discard",
            {"id": "admin-1", "role": "admin"},
        ))
        self.assertEqual(self.requests[0][0], "/v1/actions/action-1")
        self.assertEqual(self.requests[1][0], "/v1/actions/action-1/cancel")
        self.assertIn("No he escrito nada", result["messages"][0]["content"])

    def test_service_url_must_be_loopback(self):
        self.action.valves.service_url = "https://example.com"
        with self.assertRaises(RuntimeError):
            self.action._service_base()

    def test_propose_forwards_candidate_and_trusted_openwebui_context(self):
        content = "Granola action <!-- donna-jira-candidate:notification-1:2 -->"
        result = asyncio.run(self.action.action(
            {"id": "message-3", "chat_id": "chat-3", "content": content},
            "propose",
            {"id": "admin-1", "role": "admin"},
        ))
        path, payload, headers = self.requests[0]
        self.assertEqual(path, "/internal/jira-proposals/start")
        self.assertEqual(payload, {"notification_id": "notification-1", "candidate_index": 2})
        self.assertEqual(headers["X-Donna-Operator-ID"], "17")
        self.assertEqual(headers["X-Donna-Conversation-ID"], "chat-3")
        self.assertEqual(headers["X-OpenWebUI-User-Id"], "admin-1")
        self.assertIn("¿Qué proyecto?", result["messages"][0]["content"])


if __name__ == "__main__":
    unittest.main()
