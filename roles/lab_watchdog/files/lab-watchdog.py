#!/usr/bin/env python3
"""lab-watchdog: watch the lab from a Raspberry Pi and power it on with Wake-on-LAN.

Python 3.9+, standard library only. It never logs in to the lab: liveness is TCP/ICMP and
telemetry comes from the read-only Prometheus HTTP API on the LAN. A compromised Pi can send
Wake-on-LAN packets and read metrics, nothing more.
"""
from __future__ import annotations

import argparse
import copy
import http.client
import json
import logging
import os
import re
import shutil
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Optional

VERSION = "1.1.0"
TOKEN_ENV = "LAB_WATCHDOG_TELEGRAM_TOKEN"
MAC_RE = re.compile(r"^([0-9a-f]{2}:){5}[0-9a-f]{2}$")
PLUG_TYPES = ("none", "shelly_gen1", "shelly_gen2", "tasmota")
ICON = {"ok": "✅", "info": "ℹ️", "warn": "⚠️", "crit": "\U0001f6a8"}

DEFAULTS: Dict[str, Any] = {
    "lab": {"name": "lab", "ip": "192.168.0.85", "mac": "", "broadcast": "192.168.0.255", "ports": [22, 443]},
    "gateway": "192.168.0.1",
    "prometheus": {
        "enabled": True,
        "host": "monitor.stratecode.com",  # TLS name; the connection goes to lab.ip (no DNS needed)
        "port": 443,
        "path": "/prometheus/api/v1/query",
        "verify_tls": True,
        "timeout": 5,
    },
    "intervals": {
        "poll_seconds": 30,
        "down_after_failures": 3,   # consecutive failed cycles before declaring DOWN (~90 s)
        "grace_seconds": 300,       # wait before acting: covers planned reboots and deployments
        "boot_window_seconds": 240,  # time given to the lab to boot after each action
        "cooldown_seconds": 1200,   # extra wait when the lab was hot before it went down
        "remind_seconds": 3600,     # reminder period once the watchdog has given up
    },
    "thresholds": {"hot_celsius": 80, "hot_memory_seconds": 900},
    "limits": {"max_recoveries_per_hour": 2, "max_recoveries_per_day": 4, "flap_window_seconds": 1800},
    "plug": {"type": "none", "host": "", "off_seconds": 10},
    "notify": {
        # The Pi has no Internet: events are relayed to Alertmanager on the lab (LAN only) once it is up.
        "relay_enabled": True,
        "relay_path": "/alertmanager/api/v2/alerts",
        "relay_verify_tls": True,
        "telegram_enabled": False,  # only for a Pi that does have Internet
        "chat_id": "", "heartbeat_url": "", "daily_summary_hour": 9,
    },
    "mode": {"dry_run": True},
    "paths": {
        "state": "/var/lib/lab-watchdog/state.json",
        "maintenance": "/var/lib/lab-watchdog/maintenance_until",
    },
}


def deep_merge(base: Dict[str, Any], extra: Dict[str, Any]) -> Dict[str, Any]:
    out = copy.deepcopy(base)
    for key, value in (extra or {}).items():
        if isinstance(value, dict) and isinstance(out.get(key), dict):
            out[key] = deep_merge(out[key], value)
        else:
            out[key] = value
    return out


def load_config(path: Optional[str]) -> Dict[str, Any]:
    if not path:
        return copy.deepcopy(DEFAULTS)
    with open(path, "r", encoding="utf-8") as handle:
        return deep_merge(DEFAULTS, json.load(handle))


def validate_config(cfg: Dict[str, Any]) -> List[str]:
    errors: List[str] = []
    lab = cfg["lab"]
    if not MAC_RE.match(str(lab.get("mac", "")).lower()):
        errors.append("lab.mac must look like aa:bb:cc:dd:ee:ff")
    for key in ("ip", "broadcast"):
        try:
            socket.inet_aton(str(lab.get(key, "")))
        except OSError:
            errors.append("lab.%s is not an IPv4 address" % key)
    try:
        socket.inet_aton(str(cfg.get("gateway", "")))
    except OSError:
        errors.append("gateway is not an IPv4 address")
    if not lab.get("ports") or not all(isinstance(p, int) and 0 < p < 65536 for p in lab["ports"]):
        errors.append("lab.ports must be a non-empty list of TCP ports")
    for key in ("poll_seconds", "down_after_failures", "grace_seconds", "boot_window_seconds"):
        if not isinstance(cfg["intervals"].get(key), (int, float)) or cfg["intervals"][key] <= 0:
            errors.append("intervals.%s must be a positive number" % key)
    for key in ("max_recoveries_per_hour", "max_recoveries_per_day"):
        if not isinstance(cfg["limits"].get(key), int) or cfg["limits"][key] < 0:
            errors.append("limits.%s must be an integer >= 0" % key)
    if cfg["plug"].get("type") not in PLUG_TYPES:
        errors.append("plug.type must be one of %s" % ", ".join(PLUG_TYPES))
    if cfg["plug"].get("type") != "none" and not cfg["plug"].get("host"):
        errors.append("plug.host is required when plug.type is not none")
    if not str(cfg["notify"].get("relay_path", "")).startswith("/"):
        errors.append("notify.relay_path must start with /")
    if cfg["notify"].get("telegram_enabled") and not re.match(r"^-?\d+$", str(cfg["notify"].get("chat_id", ""))):
        errors.append("notify.chat_id must be a numeric Telegram chat id")
    if not isinstance(cfg["mode"].get("dry_run"), bool):
        errors.append("mode.dry_run must be true or false")
    return errors


# --------------------------------------------------------------------------- readings

@dataclass
class Reading:
    gateway_ok: bool = True
    ping_ok: bool = False
    tcp_ok: Dict[int, bool] = field(default_factory=dict)
    prom: Optional[Dict[str, Any]] = None  # {"temp_c", "on_battery", "uptime_s"} (values may be None)

    @property
    def alive(self) -> bool:
        return self.ping_ok or any(self.tcp_ok.values())

    @property
    def serving(self) -> bool:
        return any(self.tcp_ok.values())


def tcp_check(ip: str, port: int, timeout: float = 3.0) -> bool:
    try:
        with socket.create_connection((ip, port), timeout):
            return True
    except OSError:
        return False


def ping_check(ip: str, timeout: int = 2) -> bool:
    binary = shutil.which("ping")
    if not binary:
        return False
    try:
        return subprocess.run([binary, "-c", "1", "-W", str(timeout), ip], stdout=subprocess.DEVNULL,
                              stderr=subprocess.DEVNULL, timeout=timeout + 3).returncode == 0
    except (OSError, subprocess.SubprocessError):
        return False


class _SniConnection(http.client.HTTPSConnection):
    """Connect to a fixed IP while presenting (and verifying) another host name."""

    def __init__(self, ip: str, host: str, port: int, timeout: float, context: ssl.SSLContext):
        super().__init__(host, port, timeout=timeout, context=context)
        self._ip = ip

    def connect(self) -> None:
        sock = socket.create_connection((self._ip, self.port), self.timeout)
        self.sock = self._context.wrap_socket(sock, server_hostname=self.host)


def prom_scalar(payload: Dict[str, Any]) -> Optional[float]:
    """First value of an instant-vector Prometheus response, or None."""
    try:
        result = payload["data"]["result"]
        return float(result[0]["value"][1]) if result else None
    except (KeyError, IndexError, TypeError, ValueError):
        return None


def prom_query(cfg: Dict[str, Any], expr: str) -> Optional[float]:
    p = cfg["prometheus"]
    context = ssl.create_default_context()
    if not p.get("verify_tls", True):
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
    conn = _SniConnection(cfg["lab"]["ip"], p["host"], int(p["port"]), float(p["timeout"]), context)
    try:
        conn.request("GET", p["path"] + "?" + urllib.parse.urlencode({"query": expr}), headers={"Host": p["host"]})
        resp = conn.getresponse()
        if resp.status != 200:
            return None
        return prom_scalar(json.loads(resp.read().decode("utf-8")))
    finally:
        conn.close()


class NetProbe:
    """Real network probe. Tests use fake probes instead."""

    def __init__(self, cfg: Dict[str, Any]):
        self.cfg = cfg

    def read(self) -> Reading:
        cfg, lab = self.cfg, self.cfg["lab"]
        gw = cfg["gateway"]
        gateway_ok = ping_check(gw) or tcp_check(gw, 80, 2) or tcp_check(gw, 443, 2)
        reading = Reading(gateway_ok=gateway_ok)
        if not gateway_ok:
            return reading
        reading.ping_ok = ping_check(lab["ip"])
        reading.tcp_ok = {p: tcp_check(lab["ip"], p) for p in lab["ports"]}
        if cfg["prometheus"]["enabled"] and reading.serving:
            try:
                temp = prom_query(cfg, "max(node_hwmon_temp_celsius)")
                batt = prom_query(cfg, 'max(nut_ups_status{flag="OB"})')
                up = prom_query(cfg, "time()-max(node_boot_time_seconds)")
                reading.prom = {"temp_c": temp, "on_battery": None if batt is None else batt >= 1, "uptime_s": up}
            except (OSError, ssl.SSLError, ValueError, http.client.HTTPException) as exc:
                logging.warning("prometheus unavailable: %s", exc.__class__.__name__)
        return reading


# --------------------------------------------------------------------------- actions

def magic_packet(mac: str) -> bytes:
    raw = bytes.fromhex(mac.replace(":", "").replace("-", ""))
    if len(raw) != 6:
        raise ValueError("a MAC address has 6 bytes")
    return b"\xff" * 6 + raw * 16


class Waker:
    def send(self, mac: str, broadcast: str) -> None:
        packet = magic_packet(mac)
        sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        try:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
            for port in (9, 7):
                sock.sendto(packet, (broadcast, port))
        finally:
            sock.close()


class Plug:
    """Smart plug with a local HTTP API (no cloud). Used only after Wake-on-LAN failed twice."""

    def __init__(self, cfg: Dict[str, Any]):
        self.cfg = cfg["plug"]

    @property
    def configured(self) -> bool:
        return self.cfg.get("type", "none") != "none"

    def _get(self, url: str) -> None:
        with urllib.request.urlopen(url, timeout=5) as resp:  # noqa: S310 (local LAN device)
            resp.read()

    def _set(self, on: bool) -> None:
        host, kind = self.cfg["host"], self.cfg["type"]
        if kind == "shelly_gen1":
            self._get("http://%s/relay/0?turn=%s" % (host, "on" if on else "off"))
        elif kind == "shelly_gen2":
            self._get("http://%s/rpc/Switch.Set?id=0&on=%s" % (host, "true" if on else "false"))
        elif kind == "tasmota":
            self._get("http://%s/cm?cmnd=Power%%20%s" % (host, "On" if on else "Off"))

    def status(self) -> str:
        """Read-only query used by `test-plug`; never switches the plug."""
        host, kind = self.cfg["host"], self.cfg["type"]
        url = {"shelly_gen1": "http://%s/relay/0", "shelly_gen2": "http://%s/rpc/Switch.GetStatus?id=0",
               "tasmota": "http://%s/cm?cmnd=Power"}[kind] % host
        with urllib.request.urlopen(url, timeout=5) as resp:  # noqa: S310 (local LAN device)
            return resp.read().decode("utf-8", "replace")[:200]

    def cycle(self, sleep: Callable[[float], None] = time.sleep) -> None:
        self._set(False)
        sleep(float(self.cfg.get("off_seconds", 10)))
        self._set(True)


def telegram_send(token: str, chat_id: str, text: str, silent: bool) -> None:
    body = json.dumps({"chat_id": chat_id, "text": text, "disable_notification": silent}).encode("utf-8")
    req = urllib.request.Request("https://api.telegram.org/bot%s/sendMessage" % token, data=body,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:  # noqa: S310
            if resp.status != 200:
                raise RuntimeError("telegram HTTP %s" % resp.status)
    except Exception as exc:  # never let the token leak through an exception message
        raise RuntimeError("telegram send failed (%s)" % exc.__class__.__name__) from None


SEVERITY = {"ok": "info", "info": "info", "warn": "warning", "crit": "critical"}


def relay_alert(level: str, text: str, when: float) -> List[Dict[str, Any]]:
    """Alertmanager v2 payload for one watchdog event (the lab's own route turns it into a Telegram message)."""
    iso = lambda ts: time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(ts))  # noqa: E731
    return [{
        "labels": {"alertname": "LabWatchdogEvento", "source": "lab-watchdog",
                   "severity": SEVERITY.get(level, "info"), "event_id": str(int(when))},
        "annotations": {"summary": text.split("\n")[0], "description": text},
        "startsAt": iso(when),
        "endsAt": iso(when + 3600),
    }]


def relay_send(cfg: Dict[str, Any], level: str, text: str, when: float) -> None:
    """POST an event to Alertmanager through the lab's LAN-only nginx (connect by IP, present the TLS name)."""
    p, n = cfg["prometheus"], cfg["notify"]
    context = ssl.create_default_context()
    if not n.get("relay_verify_tls", True):
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
    conn = _SniConnection(cfg["lab"]["ip"], p["host"], int(p["port"]), float(p["timeout"]), context)
    try:
        body = json.dumps(relay_alert(level, text, when)).encode("utf-8")
        conn.request("POST", n["relay_path"], body=body,
                     headers={"Host": p["host"], "Content-Type": "application/json"})
        resp = conn.getresponse()
        resp.read()
        if resp.status // 100 != 2:
            raise RuntimeError("relay HTTP %s" % resp.status)
    except RuntimeError:
        raise
    except Exception as exc:
        raise RuntimeError("relay failed (%s)" % exc.__class__.__name__) from None
    finally:
        conn.close()


def fmt_time(ts: float) -> str:
    return time.strftime("%H:%M", time.localtime(ts))


class Notifier:
    """Notifications with a persistent outbox: events wait while the lab (or the Internet) is unreachable.

    Default channel is the relay through the lab's Alertmanager; direct Telegram is optional."""

    MAX_OUTBOX = 50

    def __init__(self, cfg: Dict[str, Any], state: Dict[str, Any], token: str = "",
                 sender: Callable[[str, str, str, bool], None] = telegram_send, now: Callable[[], float] = time.time,
                 relay: Callable[[Dict[str, Any], str, str, float], None] = relay_send):
        self.cfg, self.state, self.token, self.sender, self.now, self.relay = cfg, state, token, sender, now, relay

    @property
    def enabled(self) -> bool:
        n = self.cfg["notify"]
        return bool(n.get("relay_enabled") or (n.get("telegram_enabled") and self.token))

    def send(self, level: str, text: str, silent: bool = False) -> None:
        logging.info("notify[%s] %s", level, text.replace("\n", " | "))
        if not self.enabled:
            return
        box = self.state.setdefault("outbox", [])
        box.append({"t": self.now(), "level": level, "text": text, "silent": silent})
        del box[:-self.MAX_OUTBOX]
        self.flush()

    def flush(self) -> None:
        box = self.state.setdefault("outbox", [])
        while box:
            item = box[0]
            text = "%s %s" % (ICON.get(item["level"], ""), item["text"])
            if self.now() - item["t"] > 120:
                text += "\n(enviado con retraso; ocurrió a las %s)" % fmt_time(item["t"])
            try:
                if self.cfg["notify"].get("relay_enabled"):
                    self.relay(self.cfg, item["level"], text, item["t"])
                else:
                    self.sender(self.token, str(self.cfg["notify"]["chat_id"]), text, item["silent"])
            except RuntimeError as exc:
                logging.warning("%s; %d message(s) queued", exc, len(box))
                return
            box.pop(0)


# --------------------------------------------------------------------------- state machine

def new_state() -> Dict[str, Any]:
    return {
        "status": "UNKNOWN", "fail_count": 0, "first_fail_at": None, "down_since": None, "net_fail": 0,
        "net_down": False, "last_known": {}, "last_recovery_end": None, "flap_mult": 1, "history": [],
        "recovery": {"phase": "IDLE", "since": 0, "wait": 0, "hot": False, "actions": [], "last_reminder": 0},
        "last_summary_day": None, "outbox": [],
    }


class Controller:
    def __init__(self, cfg: Dict[str, Any], state: Dict[str, Any], notifier: Any, waker: Any, plug: Any,
                 maintenance: Callable[[float], bool] = lambda now: False):
        self.cfg, self.s, self.n, self.waker, self.plug, self.maint = cfg, state, notifier, waker, plug, maintenance
        self.iv, self.dry = cfg["intervals"], cfg["mode"]["dry_run"]

    # ---- public
    def step(self, r: Reading, now: float) -> None:
        s = self.s
        maint = self.maint(now)
        if r.prom:
            lk = s["last_known"]
            if r.prom.get("temp_c") is not None:
                lk["temp_c"], lk["at"] = r.prom["temp_c"], now
            if r.prom.get("on_battery") is not None:
                lk["on_battery"] = r.prom["on_battery"]
            if r.prom.get("uptime_s") is not None:
                lk["uptime_s"] = r.prom["uptime_s"]
        if not r.gateway_ok:  # our own network is broken: do not judge the lab
            s["net_fail"] += 1
            if s["net_fail"] == 2 and not s["net_down"]:
                s["net_down"] = True
                self._say(maint, "warn", "Sin red local (la puerta de enlace no responde): no puedo vigilar al lab "
                                         "ni encenderlo hasta que vuelva.")
            return
        if s["net_down"]:
            s["net_down"], s["fail_count"], s["first_fail_at"] = False, 0, None
            self._say(maint, "info", "Red local recuperada: sigo vigilando al lab.")
        s["net_fail"] = 0
        self._daily(r, now, maint)
        if r.alive:
            self._alive(r, now, maint)
            return
        s["fail_count"] += 1
        if s["fail_count"] == 1:
            s["first_fail_at"] = now
        if s["status"] != "DOWN":
            if s["fail_count"] >= self.iv["down_after_failures"]:
                self._enter_down(now, maint)
            else:
                s["status"] = "SUSPECT"
            return
        self._down_tick(now, maint)

    # ---- helpers
    def _say(self, maint: bool, level: str, text: str, silent: bool = False) -> None:
        if not maint:
            self.n.send(level, text, silent)

    def _known(self) -> str:
        lk, parts = self.s["last_known"], []
        if "temp_c" in lk:
            parts.append("última temperatura conocida %.0f °C (%s)" % (lk["temp_c"], fmt_time(lk["at"])))
        if lk.get("on_battery") is True:
            parts.append("el SAI estaba EN BATERÍA (posible corte de luz)")
        elif lk.get("on_battery") is False:
            parts.append("el SAI estaba en red")
        return "; ".join(parts) or "sin datos previos de telemetría"

    def _daily(self, r: Reading, now: float, maint: bool) -> None:
        tm, s = time.localtime(now), self.s
        hour = self.cfg["notify"]["daily_summary_hour"]
        day = "%d-%d" % (tm.tm_year, tm.tm_yday)
        if hour is None or tm.tm_hour != hour or s["last_summary_day"] == day or not r.alive:
            return
        s["last_summary_day"] = day
        lk = s["last_known"]
        extra = ", %.0f °C" % lk["temp_c"] if "temp_c" in lk else ""
        self._say(maint, "info", "Latido diario del vigilante: el lab responde%s." % extra, silent=True)

    def _alive(self, r: Reading, now: float, maint: bool) -> None:
        s, prev = self.s, self.s["status"]
        s["fail_count"], s["first_fail_at"] = 0, None
        if prev == "DOWN":
            mins = max(1, int((now - s["down_since"]) / 60))
            acts = s["recovery"]["actions"]
            how = {"wol1": "tras 1 paquete Wake-on-LAN", "wol2": "tras 2 paquetes Wake-on-LAN",
                   "plug": "tras ciclar el enchufe"}.get(acts[-1] if acts else "", "por sí solo")
            self._say(maint, "ok", "Lab recuperado %s, después de %d min caído." % (how, mins))
            s["last_recovery_end"] = now
            s["recovery"] = new_state()["recovery"]
        s["status"] = "UP" if r.serving else "DEGRADED"

    def _enter_down(self, now: float, maint: bool) -> None:
        s = self.s
        s["status"], s["down_since"] = "DOWN", s["first_fail_at"] or now
        recent = s["last_recovery_end"] is not None and now - s["last_recovery_end"] < self.cfg["limits"]["flap_window_seconds"]
        s["flap_mult"] = min(4, s["flap_mult"] * 2) if recent else 1
        lk, th = s["last_known"], self.cfg["thresholds"]
        hot = "temp_c" in lk and lk["temp_c"] >= th["hot_celsius"] and s["down_since"] - lk["at"] <= th["hot_memory_seconds"]
        wait = self.iv["grace_seconds"] * s["flap_mult"] + (self.iv["cooldown_seconds"] * s["flap_mult"] if hot else 0)
        s["recovery"] = {"phase": "GRACE", "since": now, "wait": wait, "hot": hot, "actions": [], "last_reminder": now}
        msg = "El lab no responde desde las %s: %s." % (fmt_time(s["down_since"]), self._known())
        if hot:
            msg += "\nVenía caliente: espero %d min antes de intentar encenderlo para no encenderlo contra el mismo problema." % (wait // 60)
        else:
            msg += "\nEspero %d min por si es un reinicio previsto." % (wait // 60)
        if s["flap_mult"] > 1:
            msg += "\nHa vuelto a caer poco después de recuperarse: duplico la espera."
        level = "crit" if hot or s["last_known"].get("on_battery") else "warn"
        if maint:  # announce it when the maintenance window ends, if the lab is still down
            s["recovery"]["pending"] = {"level": level, "text": msg}
        else:
            self.n.send(level, msg)

    def _down_tick(self, now: float, maint: bool) -> None:
        rec = self.s["recovery"]
        if maint:  # maintenance pauses the timers
            rec["since"] = now
            return
        pending = rec.pop("pending", None)
        if pending:
            self.n.send(pending["level"], pending["text"])
        el, ph = now - rec["since"], rec["phase"]
        if ph == "GRACE" and el >= rec["wait"]:
            self._start_sequence(now)
        elif ph == "WAIT1" and el >= self.iv["boot_window_seconds"]:
            self._wake(2, now)
        elif ph == "WAIT2" and el >= self.iv["boot_window_seconds"]:
            if self.plug.configured:
                self._cycle_plug(now)
            else:
                self._give_up(now, "Wake-on-LAN no lo ha encendido tras 2 intentos y no hay enchufe configurado.")
        elif ph == "WAIT3" and el >= self.iv["boot_window_seconds"]:
            self._give_up(now, "Tampoco ha arrancado tras ciclar el enchufe.")
        elif ph == "GAVE_UP" and now - rec["last_reminder"] >= self.iv["remind_seconds"]:
            rec["last_reminder"] = now
            self._say(False, "crit", "El lab sigue caído desde las %s y el vigilante ya no actúa. Necesita intervención."
                      % fmt_time(self.s["down_since"]))

    def _start_sequence(self, now: float) -> None:
        s, lim = self.s, self.cfg["limits"]
        s["history"] = [t for t in s["history"] if now - t < 86400]
        hour = len([t for t in s["history"] if now - t < 3600])
        if hour >= lim["max_recoveries_per_hour"] or len(s["history"]) >= lim["max_recoveries_per_day"]:
            self._give_up(now, "Límite de intentos alcanzado (%d/hora, %d/día): no vuelvo a encenderlo yo."
                          % (lim["max_recoveries_per_hour"], lim["max_recoveries_per_day"]))
            return
        s["history"].append(now)
        self._wake(1, now)

    def _wake(self, n: int, now: float) -> None:
        rec, lab = self.s["recovery"], self.cfg["lab"]
        if self.dry:
            self.n.send("info", "[SIMULACIÓN] habría enviado el paquete Wake-on-LAN nº %d a %s." % (n, lab["mac"]))
        else:
            self.waker.send(lab["mac"], lab["broadcast"])
            self.n.send("warn", "Enviado paquete Wake-on-LAN nº %d. Espero %d min a que arranque."
                        % (n, self.iv["boot_window_seconds"] // 60))
        rec["actions"].append("wol%d" % n)
        rec["phase"], rec["since"] = "WAIT%d" % n, now

    def _cycle_plug(self, now: float) -> None:
        rec = self.s["recovery"]
        if self.dry:
            self.n.send("info", "[SIMULACIÓN] habría ciclado el enchufe inteligente.")
        else:
            try:
                self.plug.cycle()
                self.n.send("warn", "Ciclo de corriente del lab hecho con el enchufe inteligente. Espero a que arranque.")
            except (OSError, ValueError) as exc:
                self._give_up(now, "No pude manejar el enchufe (%s)." % exc.__class__.__name__)
                return
        rec["actions"].append("plug")
        rec["phase"], rec["since"] = "WAIT3", now

    def _give_up(self, now: float, why: str) -> None:
        rec = self.s["recovery"]
        rec["phase"], rec["since"], rec["last_reminder"] = "GAVE_UP", now, now
        self.n.send("crit", "%s\nEl lab sigue caído desde las %s. %s" % (why, fmt_time(self.s["down_since"]), self._known()))


# --------------------------------------------------------------------------- persistence and CLI

def load_state(path: str) -> Dict[str, Any]:
    try:
        with open(path, "r", encoding="utf-8") as handle:
            return deep_merge(new_state(), json.load(handle))
    except (OSError, ValueError):
        return new_state()


def save_state(path: str, state: Dict[str, Any]) -> None:
    directory = os.path.dirname(path) or "."
    os.makedirs(directory, exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=directory, prefix=".state-")
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        json.dump(state, handle)
    os.replace(tmp, path)


def maintenance_checker(path: str) -> Callable[[float], bool]:
    def active(now: float) -> bool:
        try:
            with open(path, "r", encoding="utf-8") as handle:
                return float(handle.read().strip()) > now
        except (OSError, ValueError):
            return False
    return active


def sd_notify(message: str) -> None:
    target = os.environ.get("NOTIFY_SOCKET")
    if not target:
        return
    if target.startswith("@"):
        target = "\0" + target[1:]
    try:
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
        sock.sendto(message.encode("utf-8"), target)
        sock.close()
    except OSError:
        pass


def load_env_file(path: str) -> None:
    """Load KEY=VALUE lines into the environment without overriding existing values."""
    try:
        with open(path, "r", encoding="utf-8") as handle:
            for line in handle:
                line = line.strip()
                if line and not line.startswith("#") and "=" in line:
                    key, value = line.split("=", 1)
                    os.environ.setdefault(key.strip(), value.strip().strip('"').strip("'"))
    except OSError:
        pass


def build(cfg: Dict[str, Any]):
    load_env_file(os.environ.get("LAB_WATCHDOG_SECRETS", "/etc/lab-watchdog/secrets.env"))
    state = load_state(cfg["paths"]["state"])
    notifier = Notifier(cfg, state, token=os.environ.get(TOKEN_ENV, ""))
    ctl = Controller(cfg, state, notifier, Waker(), Plug(cfg), maintenance_checker(cfg["paths"]["maintenance"]))
    return state, notifier, ctl


def heartbeat(cfg: Dict[str, Any]) -> None:
    url = cfg["notify"].get("heartbeat_url")
    if url:
        try:
            urllib.request.urlopen(url, timeout=10).read()  # noqa: S310 (operator-provided URL)
        except Exception:
            logging.warning("heartbeat ping failed")


def cmd_run(cfg: Dict[str, Any], once: bool) -> int:
    state, notifier, ctl = build(cfg)
    probe = NetProbe(cfg)
    notifier.send("info", "Vigilante del lab arrancado (v%s, %s)." % (VERSION, "SIMULACIÓN: no actuaré" if cfg["mode"]["dry_run"] else "modo real"), silent=True)
    while True:
        try:
            reading = probe.read()
            ctl.step(reading, time.time())
            notifier.flush()
            save_state(cfg["paths"]["state"], state)
            heartbeat(cfg)
            sd_notify("WATCHDOG=1")
            if once:
                print(json.dumps({"reading": {"gateway_ok": reading.gateway_ok, "ping_ok": reading.ping_ok,
                                               "tcp_ok": reading.tcp_ok, "prom": reading.prom},
                                  "status": state["status"], "phase": state["recovery"]["phase"]}, indent=2))
                return 0
        except Exception:  # keep watching no matter what
            logging.exception("cycle failed")
        time.sleep(cfg["intervals"]["poll_seconds"])


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(prog="lab-watchdog", description=__doc__.splitlines()[0])
    parser.add_argument("-c", "--config", default=os.environ.get("LAB_WATCHDOG_CONFIG", "/etc/lab-watchdog/config.json"))
    sub = parser.add_subparsers(dest="cmd", required=True)
    sub.add_parser("run", help="watch forever")
    sub.add_parser("once", help="one cycle, print the reading")
    sub.add_parser("status", help="show the saved state")
    sub.add_parser("check-config", help="validate the configuration")
    sub.add_parser("test-notify", help="send a test event (through the lab relay, or Telegram if configured)")
    wake = sub.add_parser("test-wake", help="send ONE Wake-on-LAN packet now")
    wake.add_argument("--really", action="store_true", help="send it even in dry-run mode")
    plug = sub.add_parser("test-plug", help="check that the smart plug answers (does not switch it)")
    plug.add_argument("--cycle", action="store_true", help="also power-cycle the plug (CUTS POWER TO THE LAB)")
    plug.add_argument("--really", action="store_true", help="required together with --cycle")
    maint = sub.add_parser("maintenance", help="pause the watchdog for N minutes (0 = resume)")
    maint.add_argument("minutes", type=int)
    sub.add_parser("reset", help="clear the recovery state and the attempt history")
    args = parser.parse_args(argv)
    logging.basicConfig(level=logging.INFO, format="%(levelname)s %(message)s", stream=sys.stdout)
    cfg = load_config(args.config)
    if args.cmd == "check-config":
        errors = validate_config(cfg)
        print("\n".join(errors) if errors else "configuration OK")
        return 1 if errors else 0
    errors = validate_config(cfg)
    if errors:
        print("invalid configuration:\n  " + "\n  ".join(errors), file=sys.stderr)
        return 2
    if args.cmd in ("run", "once"):
        return cmd_run(cfg, once=args.cmd == "once")
    state, notifier, _ = build(cfg)
    if args.cmd == "status":
        print(json.dumps({k: v for k, v in state.items() if k != "outbox"}, indent=2))
    elif args.cmd == "test-notify":
        if not notifier.enabled:
            print("no channel: relay disabled and no Telegram token (secrets file or %s)" % TOKEN_ENV, file=sys.stderr)
            return 1
        notifier.send("info", "Mensaje de prueba del vigilante del lab.")
        print("queued messages left: %d (they stay queued while the lab or the channel is unreachable)"
              % len(state.get("outbox", [])))
    elif args.cmd == "test-wake":
        if cfg["mode"]["dry_run"] and not args.really:
            print("dry-run: use --really to send the packet")
        else:
            Waker().send(cfg["lab"]["mac"], cfg["lab"]["broadcast"])
            print("magic packet sent")
    elif args.cmd == "test-plug":
        plug = Plug(cfg)
        if not plug.configured:
            print("no smart plug configured (plug.type is none)", file=sys.stderr)
            return 1
        try:
            print("plug answers: %s" % plug.status())
            if args.cycle:
                if not args.really:
                    print("refusing to cycle without --really (it cuts power to the lab)", file=sys.stderr)
                    return 1
                plug.cycle()
                print("plug cycled")
        except OSError as exc:
            print("plug unreachable (%s)" % exc.__class__.__name__, file=sys.stderr)
            return 1
    elif args.cmd == "maintenance":
        path = cfg["paths"]["maintenance"]
        os.makedirs(os.path.dirname(path), exist_ok=True)
        if args.minutes <= 0 and os.path.exists(path):
            os.remove(path)
        elif args.minutes > 0:
            with open(path, "w", encoding="utf-8") as handle:
                handle.write(str(time.time() + args.minutes * 60))
        print("maintenance %s" % ("until %s" % fmt_time(time.time() + args.minutes * 60) if args.minutes > 0 else "cleared"))
    elif args.cmd == "reset":
        state["recovery"], state["history"], state["flap_mult"] = new_state()["recovery"], [], 1
        save_state(cfg["paths"]["state"], state)
        print("state cleared")
    return 0


if __name__ == "__main__":
    sys.exit(main())
