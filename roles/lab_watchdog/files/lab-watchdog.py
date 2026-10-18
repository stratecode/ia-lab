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
import signal
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Optional, Tuple

VERSION = "1.2.0"
TOKEN_ENV = "LAB_WATCHDOG_TELEGRAM_TOKEN"
MAC_RE = re.compile(r"^([0-9a-f]{2}:){5}[0-9a-f]{2}$")
PLUG_TYPES = ("none", "shelly_gen1", "shelly_gen2", "tasmota")
INDICATOR_PINS = ("buzzer", "green", "red", "button")
HHMM_RE = re.compile(r"^([01]?\d|2[0-3]):([0-5]\d)$")
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
        "retry_seconds": 0,         # after giving up, send another Wake-on-LAN this often (0 = never retry)
    },
    "thresholds": {"hot_celsius": 80, "hot_memory_seconds": 900},
    "limits": {"max_recoveries_per_hour": 2, "max_recoveries_per_day": 4, "flap_window_seconds": 1800,
               "give_up_at_second_wake": False},  # True: raise the "gave up" alarm as soon as the 2nd packet is sent
    "plug": {"type": "none", "host": "", "off_seconds": 10},
    "notify": {
        # The Pi has no Internet: events are relayed to Alertmanager on the lab (LAN only) once it is up.
        "relay_enabled": True,
        "relay_path": "/alertmanager/api/v2/alerts",
        "relay_verify_tls": True,
        "telegram_enabled": False,  # only for a Pi that does have Internet
        "chat_id": "", "heartbeat_url": "", "daily_summary_hour": 9,
        "outbox_max_age_seconds": 0,  # drop queued events older than this (0 = keep them all)
    },
    "indicator": {
        # Local alarm on the Pi (LEDs, buzzer, mute button). It works with the lab and the network down.
        "enabled": False,
        # BCM numbers. "ready" is optional (None = no LED): a blue LED that is lit only while the watchdog loop is alive.
        "pins": {"buzzer": 17, "green": 27, "red": 22, "button": 23, "ready": None},
        "quiet_start": "23:00",  # no buzzer in this window, except when the watchdog has given up
        "quiet_end": "08:00",    # both empty = no quiet hours
        "reminder_seconds": 600,  # period of the "gave up" alarm until the button is pressed
        "startup_beep": True,     # one beep when the service starts (off when an early boot beep exists)
    },
    "commands": {
        # Telegram commands from the one authorised private chat. Long polling: nothing listens on the Pi.
        "enabled": False,
        "max_age_seconds": 300,       # ignore commands older than this: a restart must never replay old ones
        "wake_cooldown_seconds": 30,  # minimum time between two manual wake-ups
        "poll_timeout_seconds": 25,   # long-poll wait of getUpdates
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
    for key in ("poll_seconds", "down_after_failures", "boot_window_seconds"):
        if not isinstance(cfg["intervals"].get(key), (int, float)) or cfg["intervals"][key] <= 0:
            errors.append("intervals.%s must be a positive number" % key)
    for key in ("grace_seconds", "cooldown_seconds", "retry_seconds"):  # 0 is allowed: no wait / no retries
        if not isinstance(cfg["intervals"].get(key), (int, float)) or cfg["intervals"][key] < 0:
            errors.append("intervals.%s must be a number >= 0" % key)
    for key in ("max_recoveries_per_hour", "max_recoveries_per_day"):
        if not isinstance(cfg["limits"].get(key), int) or cfg["limits"][key] < 0:
            errors.append("limits.%s must be an integer >= 0" % key)
    if not isinstance(cfg["limits"].get("give_up_at_second_wake"), bool):
        errors.append("limits.give_up_at_second_wake must be true or false")
    if cfg["plug"].get("type") not in PLUG_TYPES:
        errors.append("plug.type must be one of %s" % ", ".join(PLUG_TYPES))
    if cfg["plug"].get("type") != "none" and not cfg["plug"].get("host"):
        errors.append("plug.host is required when plug.type is not none")
    if not str(cfg["notify"].get("relay_path", "")).startswith("/"):
        errors.append("notify.relay_path must start with /")
    if cfg["notify"].get("telegram_enabled") and not re.match(r"^-?\d+$", str(cfg["notify"].get("chat_id", ""))):
        errors.append("notify.chat_id must be a numeric Telegram chat id")
    if not isinstance(cfg["notify"].get("outbox_max_age_seconds"), (int, float)) or cfg["notify"]["outbox_max_age_seconds"] < 0:
        errors.append("notify.outbox_max_age_seconds must be a number >= 0")
    if not isinstance(cfg["mode"].get("dry_run"), bool):
        errors.append("mode.dry_run must be true or false")
    cmd = cfg["commands"]
    if not isinstance(cmd.get("enabled"), bool):
        errors.append("commands.enabled must be true or false")
    elif cmd["enabled"]:
        if not cfg["notify"].get("telegram_enabled"):
            errors.append("commands.enabled needs notify.telegram_enabled (they use the same bot and chat)")
        for key in ("max_age_seconds", "wake_cooldown_seconds"):
            if not isinstance(cmd.get(key), (int, float)) or cmd[key] < 0:
                errors.append("commands.%s must be a number >= 0" % key)
        if not isinstance(cmd.get("poll_timeout_seconds"), (int, float)) or not 1 <= cmd["poll_timeout_seconds"] <= 50:
            errors.append("commands.poll_timeout_seconds must be between 1 and 50")
    ind = cfg["indicator"]
    if not isinstance(ind.get("enabled"), bool):
        errors.append("indicator.enabled must be true or false")
    elif ind["enabled"]:
        pins = [ind.get("pins", {}).get(name) for name in INDICATOR_PINS]
        if ind.get("pins", {}).get("ready") is not None:
            pins.append(ind["pins"]["ready"])
        if not all(isinstance(p, int) and 2 <= p <= 27 for p in pins) or len(set(pins)) != len(pins):
            errors.append("indicator.pins needs %d different BCM numbers (2-27): %s" % (len(INDICATOR_PINS), ", ".join(INDICATOR_PINS)))
        quiet = (str(ind.get("quiet_start", "")), str(ind.get("quiet_end", "")))
        if any(quiet) and (parse_hhmm(quiet[0]) is None or parse_hhmm(quiet[1]) is None):
            errors.append("indicator.quiet_start and quiet_end must both be HH:MM (or both empty)")
        if not isinstance(ind.get("reminder_seconds"), (int, float)) or ind["reminder_seconds"] <= 0:
            errors.append("indicator.reminder_seconds must be a positive number")
        if not isinstance(ind.get("startup_beep"), bool):
            errors.append("indicator.startup_beep must be true or false")
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
    except RuntimeError:
        raise
    except urllib.error.HTTPError as exc:  # only the status code: its URL contains the token
        hint = {400: " (is the chat id right, and did you press Start on the bot?)", 401: " (invalid token)",
                403: " (the bot is blocked or cannot write to that chat)"}.get(exc.code, "")
        raise RuntimeError("telegram HTTP %s%s" % (exc.code, hint)) from None
    except Exception as exc:  # never let the token leak through an exception message
        raise RuntimeError("telegram send failed (%s)" % exc.__class__.__name__) from None


def telegram_api(token: str, method: str, payload: Dict[str, Any], timeout: float) -> Dict[str, Any]:
    """One Telegram Bot API call. Errors carry the status or the exception class, never the URL (it holds the token)."""
    body = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request("https://api.telegram.org/bot%s/%s" % (token, method), data=body,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:  # noqa: S310
            data = json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        raise RuntimeError("telegram %s HTTP %s" % (method, exc.code)) from None
    except Exception as exc:
        raise RuntimeError("telegram %s failed (%s)" % (method, exc.__class__.__name__)) from None
    if not data.get("ok"):
        raise RuntimeError("telegram %s was refused" % method)
    return data


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

    def _deliver(self, item: Dict[str, Any], text: str) -> None:
        """Telegram directly first (it works while the lab is down); the lab relay is the fallback."""
        n, errors = self.cfg["notify"], []
        if n.get("telegram_enabled") and self.token:
            try:
                self.sender(self.token, str(n["chat_id"]), text, item["silent"])
                return
            except RuntimeError as exc:
                errors.append(str(exc))
        if n.get("relay_enabled"):
            try:
                self.relay(self.cfg, item["level"], text, item["t"])
                return
            except RuntimeError as exc:
                errors.append(str(exc))
        raise RuntimeError(" | ".join(errors) or "no notification channel is usable")  # every channel's reason, not just the last

    def _expire(self, box: List[Dict[str, Any]]) -> None:
        """Drop queued events that are too old to be useful (they would arrive as a burst of stale news)."""
        max_age = float(self.cfg["notify"].get("outbox_max_age_seconds", 0) or 0)
        if not max_age:
            return
        fresh = [i for i in box if self.now() - i["t"] <= max_age]
        if len(fresh) != len(box):
            logging.info("dropped %d queued message(s) older than %d h", len(box) - len(fresh), max_age // 3600)
            box[:] = fresh

    def flush(self) -> None:
        box = self.state.setdefault("outbox", [])
        self._expire(box)
        while box:
            item = box[0]
            text = "%s %s" % (ICON.get(item["level"], ""), item["text"])
            if self.now() - item["t"] > 120:
                text += "\n(enviado con retraso; ocurrió a las %s)" % fmt_time(item["t"])
            try:
                self._deliver(item, text)
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
        "last_summary_day": None, "outbox": [], "tg_offset": None,
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
                if s["recovery"]["wait"] <= 0:  # no grace period configured: act in this very cycle
                    self._down_tick(now, maint)
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
        if wait <= 0:
            msg += "\nLo enciendo ahora con Wake-on-LAN."
        elif hot:
            msg += "\nVenía caliente: espero %d min antes de intentar encenderlo para no encenderlo contra el mismo problema." % (wait // 60)
        else:
            msg += "\nEspero %d min por si es un reinicio previsto." % (wait // 60)
        if s["flap_mult"] > 1 and wait > 0:
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
        elif ph == "GAVE_UP":
            retry = self.iv.get("retry_seconds", 0)
            if retry and now - rec.get("last_retry", rec["since"]) >= retry:
                self._retry_wake(now)
            if now - rec["last_reminder"] >= self.iv["remind_seconds"]:
                rec["last_reminder"] = now
                status = ("y sigo reintentando el Wake-on-LAN cada %d min." % (retry // 60)) if retry \
                    else "y el vigilante ya no actúa."
                self._say(False, "crit", "El lab sigue caído desde las %s %s Necesita intervención."
                          % (fmt_time(self.s["down_since"]), status))

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
        final = n == 2 and self.cfg["limits"].get("give_up_at_second_wake") and not self.plug.configured
        if self.dry:
            self.n.send("info", "[SIMULACIÓN] habría enviado el paquete Wake-on-LAN nº %d a %s." % (n, lab["mac"]))
        else:
            self.waker.send(lab["mac"], lab["broadcast"])
            if not final:  # the "gave up" message below already says what happens next
                self.n.send("warn", "Enviado paquete Wake-on-LAN nº %d. Espero %d min a que arranque."
                            % (n, self.iv["boot_window_seconds"] // 60))
        rec["actions"].append("wol%d" % n)
        rec["phase"], rec["since"] = "WAIT%d" % n, now
        if final:  # raise the alarm now; the retries keep going from here
            self._give_up(now, "He enviado el segundo paquete Wake-on-LAN y el lab sigue sin responder.")

    def _retry_wake(self, now: float) -> None:
        """After giving up: keep trying, ignoring the attempt limits. A magic packet sent to a running lab does nothing."""
        rec, lab = self.s["recovery"], self.cfg["lab"]
        rec["last_retry"] = now
        n = rec["retries"] = rec.get("retries", 0) + 1
        if not self.dry:
            self.waker.send(lab["mac"], lab["broadcast"])
        logging.info("retry %d of the Wake-on-LAN%s", n, " (simulated)" if self.dry else "")
        if n == 1:  # one message when the retries begin; the hourly reminder covers the rest (no message per minute)
            self.n.send("info" if self.dry else "warn",
                        ("[SIMULACIÓN] habría reintentado el Wake-on-LAN (reintento %d)." if self.dry
                         else "Sigue caído: empiezo a reintentar el Wake-on-LAN (reintento %d).") % n, True)

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


# --------------------------------------------------------------------------- local indicator

SHORT_BEEP, LONG_BEEP, BEEP_GAP = 0.15, 0.6, 0.2
GPIO_ERRORS = (OSError, subprocess.SubprocessError, ValueError)


def parse_hhmm(text: str) -> Optional[int]:
    """'23:30' -> minutes since midnight, or None when it is not a valid HH:MM."""
    match = HHMM_RE.match(str(text).strip())
    return int(match.group(1)) * 60 + int(match.group(2)) if match else None


def indicator_mode(state: Dict[str, Any], maint: bool) -> Optional[str]:
    """Translate the controller state into what the LEDs and buzzer must show (None = nothing yet)."""
    if maint:
        return "MAINTENANCE"
    if state.get("net_down"):
        return "NETWORK_DOWN"
    status = state.get("status")
    if status == "DOWN":
        phase = str((state.get("recovery") or {}).get("phase", "GRACE"))
        if phase == "GAVE_UP":
            return "GAVE_UP"
        return "WOL_SENT" if phase.startswith("WAIT") else "DOWN"
    if status in ("SUSPECT", "DEGRADED"):
        return "SUSPECT"
    return "UP" if status == "UP" else None


class PinctrlBackend:
    """GPIO through `pinctrl`, which ships with Raspberry Pi OS (no pip, no extra packages).

    Any object with setup/write/read/cleanup can replace it (tests use a fake)."""

    def __init__(self, binary: Optional[str] = None, timeout: float = 2.0):
        self.binary = binary or shutil.which("pinctrl")
        self.timeout = timeout
        if not self.binary:
            raise OSError("pinctrl not found")

    def _run(self, *args: str) -> str:
        done = subprocess.run([str(self.binary), *args], capture_output=True, text=True,
                              timeout=self.timeout, check=True)
        return done.stdout

    def setup(self, outputs: List[int], inputs: List[int]) -> None:
        for pin in outputs:
            self._run("set", str(pin), "op", "dl")
        for pin in inputs:
            self._run("set", str(pin), "ip", "pu")

    def write(self, pin: int, level: bool) -> None:
        self._run("set", str(pin), "op", "dh" if level else "dl")

    def read(self, pin: int) -> bool:
        match = re.search(r"\|\s*(hi|lo)\b", self._run("get", str(pin)))
        if not match:
            raise OSError("unexpected pinctrl output")
        return match.group(1) == "hi"

    def cleanup(self, outputs: List[int]) -> None:
        for pin in outputs:
            self._run("set", str(pin), "op", "dl")


class Indicator:
    """LEDs, buzzer and mute button on the Pi: tells a person in the room what the watchdog is doing.

    It is fed the controller state (`update`) and drives the pins from its own thread (`run`/`tick`) so
    that blinking is faster than the 30 s poll. It never raises: with no GPIO hardware, or if the
    backend fails, it degrades to doing nothing and retries later, so the watchdog keeps watching."""

    STARTUP_SECONDS = 1.5
    RETRY_SECONDS = 300
    MAX_FAILURES = 3
    READY_TIMEOUT = 90.0  # the "ready" LED goes dark if the main loop has not reported for this long

    def __init__(self, cfg: Dict[str, Any], backend_factory: Callable[[], Any] = PinctrlBackend):
        ind = cfg["indicator"]
        self.pins = {name: int(ind["pins"][name]) for name in INDICATOR_PINS}
        if ind["pins"].get("ready") is not None:
            self.pins["ready"] = int(ind["pins"]["ready"])
        self._outputs = [name for name in ("buzzer", "green", "red", "ready") if name in self.pins]
        self._last_update: Optional[float] = None
        self.reminder = float(ind["reminder_seconds"])
        self.startup_beep = bool(ind.get("startup_beep", True))
        start, end = parse_hhmm(ind.get("quiet_start", "")), parse_hhmm(ind.get("quiet_end", ""))
        self._quiet = (start, end) if start is not None and end is not None and start != end else None
        self._factory = backend_factory
        self._lock = threading.Lock()
        self.backend: Any = None
        self.mode: Optional[str] = None
        self.acked = False  # the button silenced the "gave up" alarm
        self._prev: Optional[Tuple[Any, int, Optional[str]]] = None
        self._retry_at = 0.0
        self._failures = 0
        self._next_reminder = 0.0
        self._beeps: List[Tuple[float, bool]] = []
        self._seg_on, self._seg_end = False, 0.0
        self._startup_until = 0.0
        self._last: Dict[str, bool] = {}

    # ---- fed by the main loop
    def start(self, t: float, wall: float) -> None:
        """Service self-test: both LEDs and, unless an early boot beep covers it, one short beep (quiet hours apply)."""
        with self._lock:
            self._startup_until = t + self.STARTUP_SECONDS
            if self.startup_beep:
                self._beep(1, False, wall)

    def update(self, state: Dict[str, Any], maint: bool, wall: float, t: float) -> None:
        try:
            mode = indicator_mode(state, maint)
            actions = len((state.get("recovery") or {}).get("actions") or [])
            status = state.get("status")
            with self._lock:
                self._last_update = t
                prev = self._prev
                if mode == "GAVE_UP" and (prev is None or prev[2] != "GAVE_UP"):
                    self.acked, self._next_reminder = False, t
                elif mode != "GAVE_UP":
                    self.acked = False
                if prev is not None and mode not in (None, "MAINTENANCE"):
                    if mode == "WOL_SENT" and actions > prev[1]:
                        self._beep(1, False, wall)
                    if prev[0] == "DOWN" and status in ("UP", "DEGRADED"):
                        self._beep(2, False, wall)
                self._prev, self.mode = (status, actions, mode), mode
        except Exception:  # the indicator must never take the watchdog down
            logging.exception("indicator update failed")

    # ---- driven from its own thread
    def tick(self, t: float) -> None:
        with self._lock:
            if not self._ensure_backend(t):
                return
            if self.mode == "GAVE_UP" and not self.acked and t >= self._next_reminder:
                self._enqueue(3, True)
                self._next_reminder = t + self.reminder
            if t >= self._seg_end:
                if self._beeps:
                    duration, on = self._beeps.pop(0)
                    self._seg_on, self._seg_end = on, t + duration
                else:
                    self._seg_on = False
            green, red = self._leds(t)
            try:
                if self._seg_on or self._beeps or self.mode == "GAVE_UP":
                    if not self.backend.read(self.pins["button"]):  # pull-up: low means pressed
                        self._pressed()
                wanted = {"green": green, "red": red, "buzzer": self._seg_on}
                if "ready" in self.pins:
                    wanted["ready"] = self._ready(t)
                for name, level in wanted.items():
                    if self._last.get(name) != level:
                        self.backend.write(self.pins[name], level)
                        self._last[name] = level
                self._failures = 0
            except GPIO_ERRORS as exc:
                self._fail(t, exc)

    def run(self, stop: threading.Event, clock: Callable[[], float] = time.monotonic, period: float = 0.1) -> None:
        while not stop.is_set():
            try:
                self.tick(clock())
            except Exception:
                logging.exception("indicator tick failed")
            stop.wait(period)
        self.shutdown()

    def shutdown(self) -> None:
        """Leave every output low (a buzzer left on would sound until the next boot)."""
        with self._lock:
            backend, self.backend = self.backend, None
            self._retry_at, self._last = float("inf"), {}
            if backend is None:
                return
            for name in self._outputs:
                try:
                    backend.write(self.pins[name], False)
                except GPIO_ERRORS:
                    pass

    def silence(self) -> bool:
        """Same as pressing the mute button. True when something was sounding or an alarm was pending."""
        with self._lock:
            sounding = self._seg_on or bool(self._beeps) or (self.mode == "GAVE_UP" and not self.acked)
            self._pressed()
            return sounding

    def demo(self, mode: str, t: float, beeps: int = 0, long_beeps: bool = False) -> None:
        """Force a mode (hardware test, `test-indicator`); the beeps ignore quiet hours."""
        with self._lock:
            self.mode, self.acked, self._next_reminder = mode, False, t
            self._last_update = t
            self._enqueue(beeps, long_beeps)

    # ---- internals (the lock is held)
    def _ensure_backend(self, t: float) -> bool:
        if self.backend is not None:
            return True
        if t < self._retry_at:
            return False
        try:
            backend = self._factory()
            backend.setup([self.pins[n] for n in self._outputs], [self.pins["button"]])
        except GPIO_ERRORS as exc:
            self._retry_at = t + self.RETRY_SECONDS
            logging.warning("indicator unavailable (%s); retrying in %d s", exc.__class__.__name__, self.RETRY_SECONDS)
            return False
        self.backend, self._failures, self._last = backend, 0, {}
        return True

    def _fail(self, t: float, exc: Exception) -> None:
        self._failures += 1
        logging.warning("indicator GPIO error (%s)", exc.__class__.__name__)
        if self._failures >= self.MAX_FAILURES:
            backend, self.backend = self.backend, None
            self._retry_at, self._last = t + self.RETRY_SECONDS, {}
            try:
                backend.cleanup([self.pins[n] for n in self._outputs])
            except GPIO_ERRORS:
                pass

    def _pressed(self) -> None:
        self._beeps.clear()
        self._seg_on, self._seg_end = False, 0.0
        if self.mode == "GAVE_UP":
            self.acked = True

    def _in_quiet_hours(self, wall: float) -> bool:
        if self._quiet is None:
            return False
        tm = time.localtime(wall)
        minute, (start, end) = tm.tm_hour * 60 + tm.tm_min, self._quiet
        return start <= minute < end if start < end else (minute >= start or minute < end)

    def _beep(self, count: int, long_beeps: bool, wall: float) -> None:
        if not self._in_quiet_hours(wall):
            self._enqueue(count, long_beeps)

    def _enqueue(self, count: int, long_beeps: bool) -> None:
        for _ in range(count):
            self._beeps.append((LONG_BEEP if long_beeps else SHORT_BEEP, True))
            self._beeps.append((BEEP_GAP, False))

    def _ready(self, t: float) -> bool:
        """Blue LED: lit while the main loop is alive (it reports every cycle); dark if it stalls or the service stops."""
        if t < self._startup_until:
            return True
        return self._last_update is not None and t - self._last_update < self.READY_TIMEOUT

    def _leds(self, t: float) -> Tuple[bool, bool]:
        blink = lambda half: int(t / half) % 2 == 0  # noqa: E731
        if t < self._startup_until:
            return True, True
        mode = self.mode
        if mode == "UP":
            return True, False
        if mode == "SUSPECT":
            return blink(1.0), False
        if mode == "DOWN":
            return False, blink(1.0)
        if mode == "WOL_SENT":
            return False, True
        if mode == "GAVE_UP":
            return False, blink(0.25)
        if mode == "NETWORK_DOWN":
            return blink(0.5), not blink(0.5)
        if mode == "MAINTENANCE":
            return blink(2.0), blink(2.0)
        return False, False


DEMO_STEPS = [
    ("UP: verde fijo", "UP", 0, False),
    ("SUSPECT: verde parpadeo lento", "SUSPECT", 0, False),
    ("DOWN en espera: rojo parpadeo lento", "DOWN", 0, False),
    ("WoL enviado: rojo fijo y 1 pitido", "WOL_SENT", 1, False),
    ("Recuperado: verde fijo y 2 pitidos", "UP", 2, False),
    ("NETWORK_DOWN: verde y rojo alternan", "NETWORK_DOWN", 0, False),
    ("Mantenimiento: verde y rojo parpadeo muy lento", "MAINTENANCE", 0, False),
]


def run_demo(ind: Indicator, hold: float, clock: Callable[[], float], sleep: Callable[[float], None],
             out: Callable[[str], None]) -> bool:
    """Show every state for `hold` seconds, then the "gave up" alarm until the button is pressed."""
    def wait(seconds: float, stop_when_acked: bool = False) -> None:
        end = clock() + seconds
        while clock() < end and not (stop_when_acked and ind.acked):
            ind.tick(clock())
            sleep(0.1)

    for label, mode, beeps, long_beeps in DEMO_STEPS:
        out(label)
        ind.demo(mode, clock(), beeps, long_beeps)
        wait(hold)
    out("GAVE_UP: rojo parpadeo rápido y 3 pitidos largos. PULSA EL BOTÓN para silenciar")
    ind.demo("GAVE_UP", clock())
    wait(max(hold, 20.0), stop_when_acked=True)
    out("botón: silenciado" if ind.acked else "botón: no se pulsó (tiempo agotado)")
    return ind.acked


# --------------------------------------------------------------------------- telegram commands

def fmt_duration(seconds: float) -> str:
    seconds = int(max(0, seconds))
    hours, rest = divmod(seconds, 3600)
    if hours:
        return "%d h %02d min" % (hours, rest // 60)
    return "%d min" % (rest // 60) if rest >= 60 else "%d s" % seconds


class CommandBot:
    """Telegram commands from the one authorised private chat, by long polling (no port is opened on the Pi).

    It can read the state, send a Wake-on-LAN, pause the watchdog and mute the buzzer. It cannot log in to the
    lab (the Pi holds no credentials for it), so there is deliberately no command to shut anything down."""

    HELP = ("Órdenes:\n/estado: situación del lab y del vigilante\n"
            "/encender: enviar un Wake-on-LAN ahora (\"/encender forzar\" aunque el lab responda)\n"
            "/mantenimiento N: pausar N minutos (1-720); /reanudar para volver antes\n"
            "/silenciar: callar el zumbador, como el botón\n/ayuda: esta lista")
    PHASES = {"GRACE": "caída confirmada", "WAIT1": "WoL nº 1 enviado, esperando el arranque",
              "WAIT2": "WoL nº 2 enviado, esperando el arranque", "WAIT3": "ciclo de enchufe hecho, esperando el arranque"}

    def __init__(self, cfg: Dict[str, Any], state: Dict[str, Any], token: str,
                 api: Callable[[str, str, Dict[str, Any], float], Dict[str, Any]] = telegram_api,
                 waker: Any = None, indicator: Any = None, now: Callable[[], float] = time.time):
        c = cfg["commands"]
        self.cfg, self.state, self.token, self.api, self.now = cfg, state, token, api, now
        self.waker, self.indicator = waker or Waker(), indicator
        self.chat_id = int(str(cfg["notify"]["chat_id"]))
        self.max_age, self.cooldown, self.poll_timeout = c["max_age_seconds"], c["wake_cooldown_seconds"], c["poll_timeout_seconds"]
        self._last_wake = float("-inf")
        self._handlers = {"/estado": self._status, "/encender": self._wake, "/mantenimiento": self._maintenance,
                          "/reanudar": self._resume, "/silenciar": self._silence, "/ayuda": self._help, "/start": self._help}

    # ---- the loop
    def run(self, stop: threading.Event) -> None:
        failures = 0
        while not stop.is_set():
            try:
                self.poll_once(self.now())
                failures = 0
            except RuntimeError as exc:  # no Internet, Telegram down...: say it now and then, and keep trying
                failures += 1
                if failures in (1, 10) or failures % 60 == 0:
                    logging.warning("telegram commands: %s (attempt %d)", exc, failures)
                stop.wait(min(60, 5 * failures))
            except Exception:
                logging.exception("telegram commands failed")
                stop.wait(30)

    def poll_once(self, now: float) -> int:
        """Fetch and answer pending commands. The first call after a fresh start discards whatever is queued."""
        if self.state.get("tg_offset") is None:
            last = self.api(self.token, "getUpdates", {"offset": -1, "timeout": 0}, 10).get("result", [])
            self.state["tg_offset"] = last[-1]["update_id"] + 1 if last else 0
            return 0
        payload = {"offset": self.state["tg_offset"], "timeout": self.poll_timeout, "allowed_updates": ["message"]}
        answered = 0
        for update in self.api(self.token, "getUpdates", payload, self.poll_timeout + 10).get("result", []):
            self.state["tg_offset"] = update["update_id"] + 1  # acknowledged before acting: never replayed
            reply = self.handle(update.get("message") or {}, now)
            if reply:
                self._reply(reply)
                answered += 1
        return answered

    def _reply(self, text: str) -> None:
        try:
            self.api(self.token, "sendMessage", {"chat_id": self.chat_id, "text": text}, 10)
        except RuntimeError as exc:
            logging.warning("telegram commands: could not answer (%s)", exc)

    # ---- one command
    def handle(self, msg: Dict[str, Any], now: float) -> Optional[str]:
        chat = msg.get("chat") or {}
        if chat.get("id") != self.chat_id or chat.get("type") != "private":
            logging.warning("telegram commands: ignored a message from an unauthorised chat")
            return None  # no answer at all: do not reveal that the bot is listening
        if now - msg.get("date", 0) > self.max_age:
            logging.info("telegram commands: ignored a stale command")
            return None
        text = str(msg.get("text") or "").strip()
        if not text.startswith("/"):
            return None
        word, _, arg = text.partition(" ")
        command, arg = word.split("@")[0].lower(), arg.strip().lower()
        handler = self._handlers.get(command)
        logging.info("telegram command: %s", command if handler else "(unknown)")
        return handler(arg, now) if handler else "No conozco esa orden.\n" + self.HELP

    def _help(self, arg: str, now: float) -> str:
        return self.HELP

    def _status(self, arg: str, now: float) -> str:
        s, rec = self.state, self.state.get("recovery") or {}
        lk, status, retry = s.get("last_known") or {}, s.get("status"), self.cfg["intervals"].get("retry_seconds", 0)
        if s.get("net_down"):
            lab = "Sin red local: no puedo juzgar al lab."
        elif status in ("UP", "DEGRADED"):
            facts = (["%.0f °C" % lk["temp_c"]] if "temp_c" in lk else []) + (
                ["SAI EN BATERÍA"] if lk.get("on_battery") is True else ["SAI en red"] if lk.get("on_battery") is False else [])
            lab = "Lab: responde%s." % (" (%s)" % ", ".join(facts) if facts else "")
            if status == "DEGRADED":
                lab += " Contesta al ping pero no por SSH ni HTTPS."
        elif status == "DOWN":
            phase = rec.get("phase")
            detail = ("sigo reintentando el Wake-on-LAN cada %d min" % max(1, int(retry // 60)) if retry else "me he rendido") \
                if phase == "GAVE_UP" else self.PHASES.get(phase, "caído")
            lab = "Lab: CAÍDO desde las %s (%s): %s." % (fmt_time(s.get("down_since") or now),
                                                        fmt_duration(now - (s.get("down_since") or now)), detail)
        elif status == "SUSPECT":
            lab = "Lab: sin respuesta en la última comprobación; lo confirmo en el siguiente ciclo."
        else:
            lab = "Lab: aún sin datos."
        lines = [lab, "Vigilante en modo %s." % ("SIMULACIÓN (no envía WoL)" if self.cfg["mode"]["dry_run"] else "real")]
        until = maintenance_until(self.cfg["paths"]["maintenance"], now)
        if until:
            lines.append("En mantenimiento hasta las %s: sin avisos ni WoL." % fmt_time(until))
        queued = len(s.get("outbox") or [])
        if queued:
            lines.append("Avisos pendientes de enviar: %d." % queued)
        return "\n".join(lines)

    def _wake(self, arg: str, now: float) -> str:
        if self.cfg["mode"]["dry_run"]:
            return "El vigilante está en modo SIMULACIÓN: no envío ningún Wake-on-LAN."
        if self.state.get("status") in ("UP", "DEGRADED") and arg != "forzar":
            return "El lab responde ahora mismo. Si aun así quieres mandar el paquete: /encender forzar"
        if now - self._last_wake < self.cooldown:
            return "Acabo de enviar uno hace %d s: espera un poco." % (now - self._last_wake)
        lab = self.cfg["lab"]
        self.waker.send(lab["mac"], lab["broadcast"])
        self._last_wake = now
        return "Wake-on-LAN enviado a %s." % lab["mac"]

    def _maintenance(self, arg: str, now: float) -> str:
        if not arg.isdigit() or not 1 <= int(arg) <= 720:
            return "Uso: /mantenimiento N, con N entre 1 y 720 minutos (por ejemplo /mantenimiento 30)."
        until = set_maintenance(self.cfg["paths"]["maintenance"], int(arg), now)
        return "Mantenimiento hasta las %s: no enviaré avisos ni Wake-on-LAN. /reanudar para volver antes." % fmt_time(until or now)

    def _resume(self, arg: str, now: float) -> str:
        set_maintenance(self.cfg["paths"]["maintenance"], 0, now)
        return "Mantenimiento terminado: vuelvo a vigilar."

    def _silence(self, arg: str, now: float) -> str:
        if self.indicator is None:
            return "El indicador local no está activado: no hay zumbador que silenciar."
        return "Zumbador silenciado." if self.indicator.silence() else "No había nada sonando."


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


def set_maintenance(path: str, minutes: int, now: float) -> Optional[float]:
    """Pause the watchdog for N minutes (returns when it ends); 0 or less resumes it (returns None)."""
    os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
    if minutes <= 0:
        if os.path.exists(path):
            os.remove(path)
        return None
    until = now + minutes * 60
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(str(until))
    return until


def maintenance_until(path: str, now: float) -> Optional[float]:
    """When the current maintenance window ends, or None when there is none."""
    try:
        with open(path, "r", encoding="utf-8") as handle:
            until = float(handle.read().strip())
    except (OSError, ValueError):
        return None
    return until if until > now else None


def purge_outbox(state: Dict[str, Any]) -> int:
    """Forget every queued message and say how many there were."""
    dropped = len(state.get("outbox", []))
    state["outbox"] = []
    return dropped


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
    indicator, stop, thread = None, threading.Event(), None
    if cfg["indicator"]["enabled"] and not once:
        indicator = Indicator(cfg)
        indicator.start(time.monotonic(), time.time())
        thread = threading.Thread(target=indicator.run, args=(stop,), name="indicator", daemon=True)
        thread.start()
        signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))  # let `finally` silence the buzzer on `systemctl stop`
    if cfg["commands"]["enabled"] and not once:
        if notifier.token:
            bot = CommandBot(cfg, state, notifier.token, indicator=indicator)
            threading.Thread(target=bot.run, args=(stop,), name="telegram-commands", daemon=True).start()
        else:
            logging.warning("telegram commands are enabled but there is no bot token: they stay off")
    notifier.send("info", "Vigilante del lab arrancado (v%s, %s)." % (VERSION, "SIMULACIÓN: no actuaré" if cfg["mode"]["dry_run"] else "modo real"), silent=True)
    try:
        while True:
            try:
                reading = probe.read()
                now = time.time()
                ctl.step(reading, now)
                if indicator:
                    indicator.update(state, ctl.maint(now), now, time.monotonic())
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
    finally:
        stop.set()
        if thread:
            thread.join(timeout=3)
        if indicator:
            indicator.shutdown()


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
    demo = sub.add_parser("test-indicator", help="show every LED/buzzer state, then wait for the button (stop the service first)")
    demo.add_argument("--seconds", type=float, default=5.0, help="how long to hold each state")
    maint = sub.add_parser("maintenance", help="pause the watchdog for N minutes (0 = resume)")
    maint.add_argument("minutes", type=int)
    sub.add_parser("reset", help="clear the recovery state and the attempt history")
    sub.add_parser("purge-outbox", help="drop every queued message (stop the service first: it keeps its own copy)")
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
    elif args.cmd == "test-indicator":
        ind = Indicator(cfg)
        ind.start(time.monotonic(), time.time())
        try:
            ind.tick(time.monotonic())
            if ind.backend is None:
                print("no GPIO available: is `pinctrl` installed and is the user in the gpio group?", file=sys.stderr)
                return 1
            run_demo(ind, args.seconds, time.monotonic, time.sleep, print)
        finally:
            ind.shutdown()
    elif args.cmd == "maintenance":
        until = set_maintenance(cfg["paths"]["maintenance"], args.minutes, time.time())
        print("maintenance %s" % ("until %s" % fmt_time(until) if until else "cleared"))
    elif args.cmd == "purge-outbox":
        dropped = purge_outbox(state)
        save_state(cfg["paths"]["state"], state)
        print("dropped %d queued message(s)" % dropped)
    elif args.cmd == "reset":
        state["recovery"], state["history"], state["flap_mult"] = new_state()["recovery"], [], 1
        save_state(cfg["paths"]["state"], state)
        print("state cleared")
    return 0


if __name__ == "__main__":
    sys.exit(main())
