"""Confirm a Donna Jira task proposal from an Open WebUI assistant message.

Import this Action in Open WebUI and attach it to Donna's model only. Jira
credentials stay in the private donna-jira-actions service.
"""

import asyncio
import json
import re
import ipaddress
import urllib.parse
import urllib.request

from pydantic import BaseModel, Field


ACTION_MARKER = re.compile(r"<!-- donna-jira-action:([A-Za-z0-9-]{1,64}):([a-f0-9]{64}) -->")
CANDIDATE_MARKER = re.compile(r"<!-- donna-jira-candidate:([A-Za-z0-9_-]{1,200}):(\d{1,4}) -->")


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, new_url):
        return None


class Action:
    """title: Donna Jira actions
    author: StrateCode
    version: 0.1.0
    required_open_webui_version: 0.12.2
    """

    actions = [
        {"id": "propose", "name": "Proponer tarea Jira"},
        {"id": "create", "name": "Crear en Jira"},
        {"id": "comment", "name": "Actualizar tarea"},
        {"id": "not_same", "name": "No es la misma tarea"},
        {"id": "discard", "name": "Descartar propuesta"},
    ]

    class Valves(BaseModel):
        service_url: str = Field(default="http://127.0.0.1:8094")
        service_token: str = Field(default="", description="Private token for donna-jira-actions")
        operator_id: int = Field(default=0, description="Allowlisted operator ID configured for Donna")
        admin_user_id: str = Field(default="", description="Only this Open WebUI user may confirm Jira actions")
        secretary_url: str = Field(default="http://127.0.0.1:8082")

    def __init__(self):
        self.valves = self.Valves()

    async def action(self, body: dict, __id__=None, __user__=None, __event_emitter__=None, __event_call__=None):
        content = body.get("content", "")
        if not isinstance(content, str):
            return {"messages": [{"id": body.get("id"), "content": "No se encontró una propuesta de Jira válida."}]}

        user = __user__ if isinstance(__user__, dict) else {}
        role = str(user.get("role", "")).lower()
        user_id = str(user.get("id", ""))
        if __id__ == "propose":
            candidate = CANDIDATE_MARKER.search(content)
            if not candidate:
                return {"messages": [{"id": body.get("id"), "content": content}]}
            if role != "admin" or not self.valves.admin_user_id or user_id != self.valves.admin_user_id:
                return {"messages": [{"id": body.get("id"), "content": content + "\n\nNo tienes autorización para proponer tareas de Jira."}]}
            chat_id = str(body.get("chat_id", "")).strip()
            if not chat_id or len(chat_id) > 200 or self.valves.operator_id <= 0 or len(self.valves.service_token) < 32:
                return {"messages": [{"id": body.get("id"), "content": content + "\n\nLa integración de Jira no está configurada."}]}
            try:
                result = await asyncio.to_thread(self._start_proposal, candidate.group(1), int(candidate.group(2)), chat_id, user_id)
                message = str(result.get("message", ""))
                if not message:
                    raise RuntimeError("empty proposal response")
                return {"messages": [{"id": body.get("id"), "content": content + "\n\n" + message}]}
            except Exception:
                return {"messages": [{"id": body.get("id"), "content": content + "\n\nNo he podido preparar la propuesta. No se ha escrito nada."}]}

        match = ACTION_MARKER.search(content)
        if not match:
            return {"messages": [{"id": body.get("id"), "content": content}]}

        if role != "admin" or not self.valves.admin_user_id or user_id != self.valves.admin_user_id:
            return {"messages": [{"id": body.get("id"), "content": content + "\n\nNo tienes autorización para confirmar acciones de Jira."}]}

        action_id, digest = match.groups()
        if __id__ not in ("create", "comment", "not_same", "discard"):
            return {"messages": [{"id": body.get("id"), "content": content + "\n\nAcción de Jira no válida."}]}
        chat_id = str(body.get("chat_id", "")).strip()
        if not chat_id or len(chat_id) > 200 or self.valves.operator_id <= 0 or len(self.valves.service_token) < 32:
            return {"messages": [{"id": body.get("id"), "content": content + "\n\nLa integración de Jira no está configurada."}]}

        try:
            preview = await asyncio.to_thread(self._preview, action_id, chat_id)
            expected_kind = "comment" if __id__ == "not_same" else __id__
            if (__id__ != "discard" and preview.get("kind") != expected_kind) or preview.get("payload_digest") != digest:
                raise RuntimeError("Jira preview did not match the selected action")
            if __id__ in ("not_same", "discard"):
                await asyncio.to_thread(self._cancel, action_id, digest, chat_id)
                message = "Entendido: he descartado esa coincidencia y no añadiré el comentario." if __id__ == "not_same" else "Propuesta descartada. No he escrito nada en Jira."
                return {"messages": [{"id": body.get("id"), "content": content + "\n\n" + message}]}
            receipt = await asyncio.to_thread(self._apply, action_id, digest, chat_id)
        except Exception:
            return {"messages": [{"id": body.get("id"), "content": content + "\n\nNo pude verificar la escritura en Jira. Comprueba el estado antes de volver a intentarlo."}]}

        issue_key = str(receipt.get("issue_key", ""))
        if not receipt.get("verified") or not issue_key:
            return {"messages": [{"id": body.get("id"), "content": content + "\n\nJira no devolvió una confirmación verificable. Comprueba el estado antes de reintentar."}]}
        return {"messages": [{"id": body.get("id"), "content": content + "\n\nConfirmado y verificado en Jira: " + issue_key}]}

    def _apply(self, action_id: str, digest: str, chat_id: str) -> dict:
        base = self._service_base()
        url = f"{base}/v1/actions/{action_id}/apply"
        payload = json.dumps({"payload_digest": digest}).encode("utf-8")
        request = urllib.request.Request(url, data=payload, method="POST")
        request.add_header("Authorization", "Bearer " + self.valves.service_token)
        request.add_header("X-Donna-Operator-ID", str(self.valves.operator_id))
        request.add_header("X-Donna-Channel", "openwebui")
        request.add_header("X-Donna-Conversation-ID", chat_id)
        request.add_header("Content-Type", "application/json")
        with self._opener().open(request, timeout=10) as response:
            if response.status != 200:
                raise RuntimeError("Jira action service rejected the confirmation")
            data = response.read(65537)
            if len(data) > 65536:
                raise RuntimeError("Jira action response was too large")
        result = json.loads(data)
        if not isinstance(result, dict):
            raise RuntimeError("Jira action response was invalid")
        return result

    def _preview(self, action_id: str, chat_id: str) -> dict:
        base = self._service_base()
        url = f"{base}/v1/actions/{action_id}"
        request = urllib.request.Request(url, method="GET")
        request.add_header("Authorization", "Bearer " + self.valves.service_token)
        request.add_header("X-Donna-Operator-ID", str(self.valves.operator_id))
        request.add_header("X-Donna-Channel", "openwebui")
        request.add_header("X-Donna-Conversation-ID", chat_id)
        with self._opener().open(request, timeout=10) as response:
            if response.status != 200:
                raise RuntimeError("Jira action service rejected the preview")
            data = response.read(65537)
            if len(data) > 65536:
                raise RuntimeError("Jira preview response was too large")
        result = json.loads(data)
        if not isinstance(result, dict):
            raise RuntimeError("Jira preview response was invalid")
        return result

    def _cancel(self, action_id: str, digest: str, chat_id: str) -> None:
        base = self._service_base()
        url = f"{base}/v1/actions/{action_id}/cancel"
        payload = json.dumps({"payload_digest": digest}).encode("utf-8")
        request = urllib.request.Request(url, data=payload, method="POST")
        request.add_header("Authorization", "Bearer " + self.valves.service_token)
        request.add_header("X-Donna-Operator-ID", str(self.valves.operator_id))
        request.add_header("X-Donna-Channel", "openwebui")
        request.add_header("X-Donna-Conversation-ID", chat_id)
        request.add_header("Content-Type", "application/json")
        with self._opener().open(request, timeout=10) as response:
            if response.status != 200:
                raise RuntimeError("Jira action service rejected the cancellation")

    def _start_proposal(self, notification_id: str, candidate_index: int, chat_id: str, user_id: str) -> dict:
        base = self._loopback_base(self.valves.secretary_url)
        url = f"{base}/internal/jira-proposals/start"
        payload = json.dumps({"notification_id": notification_id, "candidate_index": candidate_index}).encode("utf-8")
        request = urllib.request.Request(url, data=payload, method="POST")
        request.add_header("Authorization", "Bearer " + self.valves.service_token)
        request.add_header("X-Donna-Operator-ID", str(self.valves.operator_id))
        request.add_header("X-Donna-Channel", "openwebui")
        request.add_header("X-Donna-Conversation-ID", chat_id)
        request.add_header("X-OpenWebUI-User-Id", user_id)
        request.add_header("X-OpenWebUI-User-Role", "admin")
        request.add_header("Content-Type", "application/json")
        with self._opener().open(request, timeout=10) as response:
            if response.status != 200:
                raise RuntimeError("Secretary rejected the proposal request")
            data = response.read(65537)
            if len(data) > 65536:
                raise RuntimeError("Secretary proposal response was too large")
        result = json.loads(data)
        if not isinstance(result, dict):
            raise RuntimeError("Secretary proposal response was invalid")
        return result

    def _service_base(self) -> str:
        return self._loopback_base(self.valves.service_url)

    @staticmethod
    def _loopback_base(value: str) -> str:
        parsed = urllib.parse.urlparse(value.strip())
        host = parsed.hostname or ""
        try:
            loopback = ipaddress.ip_address(host).is_loopback
        except ValueError:
            loopback = host.lower() == "localhost"
        if parsed.scheme not in ("http", "https") or not parsed.netloc or not loopback or parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise RuntimeError("Jira action service must be a loopback URL")
        return value.rstrip("/")

    @staticmethod
    def _opener():
        return urllib.request.build_opener(_NoRedirect())
