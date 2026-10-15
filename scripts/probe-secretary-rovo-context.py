#!/usr/bin/env python3
"""Run or preview the secretary's read-only Rovo lookup for a Jira context."""

import argparse
import json
import os
from pathlib import Path
import re
import ssl
import stat
import sys
import urllib.error
import urllib.request


ENDPOINT = "https://mcp.atlassian.com/v2/mcp"
DEFAULT_QUERY = (
    "Iconos de señales de tráfico: no es urgente ahora; sería mejor "
    "asignarlos por categoría cuando esté lista la jerarquía. "
    "Busca tareas relacionadas en Jira y muestra estado y responsable."
)
ISSUE_KEY = re.compile(r"^[A-Z][A-Z0-9]*-[0-9]+$")
ISSUE_KEY_IN_TEXT = re.compile(r"\b[A-Z][A-Z0-9]*-[0-9]+\b")
MAX_BODY = 2 * 1024 * 1024


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def build_search_arguments(cloud_id, query):
    return {
        "cloudId": cloud_id,
        "query": query,
        "targetApp": "JIRA",
        "mode": "STANDARD",
        "limit": 10,
    }


def redact_secrets(value, secrets):
    for secret in secrets:
        if secret:
            value = value.replace(secret, "[REDACTED]")
    return value


def config_path(value):
    configured = value or os.environ.get("LAB_SECRETARY_CONNECTORS_FILE")
    if configured:
        return Path(configured).expanduser()
    return Path.home() / ".config/stratecode-lab/secretary-connectors.json"


def load_jira_config(path):
    try:
        mode = stat.S_IMODE(path.stat().st_mode)
    except OSError as error:
        raise RuntimeError(f"No se puede abrir el fichero privado de conectores: {error}") from error
    if mode & 0o077:
        raise RuntimeError(f"Permisos inseguros en {path}: usa chmod 600")
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
        jira = value["jira"]
        endpoint = jira.get("server_url", ENDPOINT)
        token = jira["access_token"]
    except (OSError, ValueError, KeyError, TypeError) as error:
        raise RuntimeError(f"Configuración Jira ausente o inválida en {path}: {error}") from error
    if endpoint != ENDPOINT:
        raise RuntimeError("Destino Jira rechazado; se permite únicamente el MCP oficial de Atlassian")
    if not isinstance(token, str) or not token.strip():
        raise RuntimeError("No hay access_token Jira en el fichero de conectores")
    return {"server_url": endpoint, "access_token": token, "cloud_id": jira.get("cloud_id", "")}


class MCPClient:
    def __init__(self, endpoint, token, timeout=35):
        if endpoint != ENDPOINT:
            raise ValueError("Solo se permite el endpoint oficial del MCP Atlassian")
        self.endpoint = endpoint
        self.token = token
        self.timeout = timeout
        self.session_id = ""
        self.protocol_version = ""
        self.request_id = 0
        try:
            import certifi

            tls_context = ssl.create_default_context(cafile=certifi.where())
        except ImportError:
            tls_context = ssl.create_default_context()
        self.opener = urllib.request.build_opener(
            NoRedirect(), urllib.request.HTTPSHandler(context=tls_context)
        )

    def rpc(self, method, params, notification=False):
        payload = {"jsonrpc": "2.0", "method": method, "params": params}
        if not notification:
            self.request_id += 1
            payload["id"] = self.request_id
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        headers = {
            "Authorization": "Bearer " + self.token,
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
        }
        if self.session_id:
            headers["Mcp-Session-Id"] = self.session_id
        if self.protocol_version:
            headers["MCP-Protocol-Version"] = self.protocol_version
        request = urllib.request.Request(self.endpoint, data=body, headers=headers, method="POST")
        try:
            with self.opener.open(request, timeout=self.timeout) as response:
                response_body = response.read(MAX_BODY + 1)
                status = response.status
                content_type = response.headers.get("Content-Type", "")
                session_id = response.headers.get("Mcp-Session-Id", "")
        except urllib.error.HTTPError as error:
            error_body = error.read(MAX_BODY).decode("utf-8", errors="replace")
            message = redact_secrets(error_body, [self.token])
            raise RuntimeError(f"HTTP {error.code} {error.reason}: {message[:8000]}") from error
        except urllib.error.URLError as error:
            raise RuntimeError(f"Transporte MCP no disponible: {error.reason}") from error

        if status < 200 or status >= 300:
            raise RuntimeError(f"MCP HTTP {status}")
        if session_id:
            self.session_id = session_id
        if notification:
            return None
        if len(response_body) > MAX_BODY:
            raise RuntimeError("Respuesta MCP excede el límite de 2 MiB")
        try:
            if content_type.lower().startswith("text/event-stream"):
                envelope = None
                for line in response_body.decode("utf-8", errors="replace").splitlines():
                    if line.startswith("data:"):
                        candidate = json.loads(line[5:].strip())
                        if candidate.get("id") == payload["id"]:
                            envelope = candidate
                            break
                if envelope is None:
                    raise RuntimeError("El stream MCP terminó sin resultado JSON-RPC")
            else:
                envelope = json.loads(response_body)
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise RuntimeError(f"Respuesta MCP no es JSON válido: {error}") from error
        if envelope.get("error"):
            detail = json.dumps(envelope["error"], ensure_ascii=False)
            raise RuntimeError("JSON-RPC error: " + redact_secrets(detail, [self.token]))
        if "result" not in envelope:
            raise RuntimeError("Respuesta MCP sin result")
        return envelope["result"]

    def initialize(self):
        result = self.rpc(
            "initialize",
            {
                "protocolVersion": "2025-03-26",
                "capabilities": {},
                "clientInfo": {"name": "stratecode-rovo-probe", "version": "1.0.0"},
            },
        )
        self.protocol_version = result.get("protocolVersion", "")
        self.rpc("notifications/initialized", {}, notification=True)

    def call_tool(self, name, arguments):
        if name not in {"getAccessibleAtlassianResources", "search", "getJiraIssue"}:
            raise ValueError(f"Herramienta fuera de la allowlist de solo lectura: {name}")
        result = self.rpc("tools/call", {"name": name, "arguments": arguments})
        if result.get("isError"):
            detail = json.dumps(result, ensure_ascii=False)
            raise RuntimeError("MCP tool isError: " + redact_secrets(detail, [self.token]))
        return result


def unwrap_tool_result(result):
    structured = result.get("structuredContent")
    if structured is not None:
        return structured
    for item in result.get("content", []):
        if item.get("type") == "text":
            text = item.get("text", "")
            try:
                return json.loads(text)
            except json.JSONDecodeError:
                return text
    return result


def find_cloud_id(value):
    if isinstance(value, dict):
        resources = value.get("resources")
        if not resources and isinstance(value.get("data"), dict):
            resources = value["data"].get("resources")
        if isinstance(resources, list) and len(resources) == 1:
            site = resources[0]
            if isinstance(site, dict):
                return site.get("cloudId") or site.get("id")
        return value.get("cloudId") or value.get("id")
    if isinstance(value, list) and len(value) == 1 and isinstance(value[0], dict):
        return value[0].get("cloudId") or value[0].get("id")
    return None


def issue_candidates(value):
    found = []
    seen = set()

    def add(record):
        if not isinstance(record, dict):
            return
        key = record.get("key") or record.get("issueKey") or record.get("id")
        if isinstance(key, str) and ISSUE_KEY.fullmatch(key) and key not in seen:
            seen.add(key)
            found.append(key)
        for name in ("key", "issueKey", "issue_key"):
            text = record.get(name)
            if isinstance(text, str):
                for match in ISSUE_KEY_IN_TEXT.findall(text):
                    if match not in seen:
                        seen.add(match)
                        found.append(match)

    def visit(node):
        if isinstance(node, dict):
            for name, child in node.items():
                if name.lower() in {"results", "items", "issues", "matches"} and isinstance(child, list):
                    for row in child:
                        if isinstance(row, dict):
                            add(row)
                elif name.lower() == "text" and isinstance(child, str):
                    try:
                        visit(json.loads(child))
                    except json.JSONDecodeError:
                        for match in ISSUE_KEY_IN_TEXT.findall(child):
                            if match not in seen:
                                seen.add(match)
                                found.append(match)
                else:
                    visit(child)
        elif isinstance(node, list):
            for child in node:
                visit(child)

    visit(value)
    return found[:5]


def show_tool_result(label, result, token):
    safe = redact_secrets(json.dumps(result, ensure_ascii=False, indent=2), [token])
    print(f"\n{label} · resultado MCP:\n{safe}")


def run(args):
    query = args.query.strip()
    if not query or len(query) > 2000:
        raise RuntimeError("La consulta debe contener entre 1 y 2000 caracteres")
    print("Consulta reconstruida; no es posible recuperar la consulta literal histórica:")
    print(query)
    print("Modo: " + ("ejecución real, solo lectura" if args.live else "simulación local, sin red"))

    if not args.live:
        cloud_id = args.cloud_id or "<cloud_id del fichero privado>"
        print("Petición tools/call:")
        print(json.dumps({"name": "search", "arguments": build_search_arguments(cloud_id, query)}, ensure_ascii=False, indent=2))
        print("Si Rovo devuelve claves, la secretaria verifica hasta 5 con getJiraIssue y fields=[summary,status,assignee,duedate,updated,project], updateHistory=false.")
        return 0

    config = load_jira_config(config_path(args.config))
    client = MCPClient(config["server_url"], config["access_token"])
    client.initialize()
    cloud_id = args.cloud_id or config["cloud_id"]
    if not cloud_id:
        print("Resolviendo cloudId con getAccessibleAtlassianResources...")
        resources_result = client.call_tool("getAccessibleAtlassianResources", {})
        resources = unwrap_tool_result(resources_result)
        cloud_id = find_cloud_id(resources)
        show_tool_result("getAccessibleAtlassianResources", resources_result, config["access_token"])
        if not cloud_id:
            raise RuntimeError("No se pudo elegir un único cloudId; pasa --cloud-id")
    search_args = build_search_arguments(cloud_id, query)
    print("\nPetición tools/call:")
    print(json.dumps({"name": "search", "arguments": search_args}, ensure_ascii=False, indent=2))
    search_result = client.call_tool("search", search_args)
    payload = unwrap_tool_result(search_result)
    show_tool_result("search", search_result, config["access_token"])
    keys = issue_candidates(payload)
    print(f"\nClaves Jira candidatas para verificación: {keys or 'ninguna'}")
    for key in keys:
        issue_args = {
            "cloudId": cloud_id,
            "issueIdOrKey": key,
            "fields": ["summary", "status", "assignee", "duedate", "updated", "project"],
            "updateHistory": False,
        }
        print("\nPetición tools/call:")
        print(json.dumps({"name": "getJiraIssue", "arguments": issue_args}, ensure_ascii=False, indent=2))
        issue_result = client.call_tool("getJiraIssue", issue_args)
        show_tool_result("getJiraIssue " + key, issue_result, config["access_token"])
    if not keys:
        print("Rovo no entregó una clave Jira verificable. Esto es resultado vacío/no verificable, no un error de conexión.")
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--query", default=DEFAULT_QUERY, help="Consulta natural enviada a Rovo (máx. 2000 caracteres)")
    parser.add_argument("--config", help="Ruta privada de secretary-connectors.json")
    parser.add_argument("--cloud-id", help="Fuerza el cloudId Jira; por defecto usa config o descubre un único sitio")
    parser.add_argument("--live", action="store_true", help="Ejecuta llamadas reales, limitadas a herramientas MCP de solo lectura")
    args = parser.parse_args()
    try:
        return run(args)
    except RuntimeError as error:
        print("ERROR: " + str(error), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
