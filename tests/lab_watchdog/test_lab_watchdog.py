"""Unit tests for roles/lab_watchdog/files/lab-watchdog.py.

Pure tests: fake clock, fake probe results, fake Telegram/Wake-on-LAN/plug. Any real socket use fails the
test, so running them never touches the network.  Run:  python3 -m unittest discover -s tests/lab_watchdog
"""
import copy
import importlib.util
import json
import os
import socket
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "..", "..", "roles", "lab_watchdog", "files", "lab-watchdog.py")
spec = importlib.util.spec_from_file_location("lab_watchdog", SCRIPT)
lw = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = lw  # required by @dataclass for modules loaded from a path
spec.loader.exec_module(lw)

POLL = 30
UP = lambda **kw: lw.Reading(gateway_ok=True, ping_ok=True, tcp_ok={22: True, 443: True}, **kw)  # noqa: E731
DOWN = lw.Reading(gateway_ok=True, ping_ok=False, tcp_ok={22: False, 443: False})
NET_DOWN = lw.Reading(gateway_ok=False)


class FakeNotifier:
    def __init__(self):
        self.sent = []

    def send(self, level, text, silent=False):
        self.sent.append((level, text, silent))

    def texts(self):
        return [t for _, t, _ in self.sent]


class FakeWaker:
    def __init__(self):
        self.calls = []

    def send(self, mac, broadcast):
        self.calls.append((mac, broadcast))


class FakePlug:
    def __init__(self, configured=False, fail=False):
        self.configured, self.fail, self.cycles = configured, fail, 0

    def cycle(self):
        if self.fail:
            raise OSError("plug unreachable")
        self.cycles += 1


def make_cfg(**over):
    cfg = lw.deep_merge(lw.DEFAULTS, {
        "lab": {"mac": "c8:ff:bf:08:5e:db"},
        "notify": {"chat_id": "123456", "daily_summary_hour": None, "relay_enabled": False, "telegram_enabled": True},
        "mode": {"dry_run": False},
    })
    return lw.deep_merge(cfg, over)


class Base(unittest.TestCase):
    def setUp(self):
        self._real_socket = socket.socket

        def blocked(*a, **k):
            raise AssertionError("tests must not open real sockets")
        socket.socket = blocked  # type: ignore[assignment]
        self.addCleanup(lambda: setattr(socket, "socket", self._real_socket))
        self.now = 1_800_000_000.0

    def controller(self, cfg=None, plug=None, maint=None):
        cfg = cfg or make_cfg()
        self.state = lw.new_state()
        self.n, self.w = FakeNotifier(), FakeWaker()
        self.plug = plug or FakePlug()
        return lw.Controller(cfg, self.state, self.n, self.w, self.plug, maint or (lambda now: False))

    def drive(self, ctl, seconds, reading):
        end = self.now + seconds
        while self.now < end:
            ctl.step(reading, self.now)
            self.now += POLL


class StateMachineTests(Base):
    def test_all_up_no_actions(self):
        ctl = self.controller()
        self.drive(ctl, 3600, UP(prom={"temp_c": 45.0, "on_battery": False, "uptime_s": 1000.0}))
        self.assertEqual(self.state["status"], "UP")
        self.assertEqual(self.n.sent, [])
        self.assertEqual(self.w.calls, [])

    def test_gateway_down_does_not_judge_the_lab(self):
        ctl = self.controller()
        self.drive(ctl, 1800, NET_DOWN)
        self.assertEqual(self.w.calls, [])
        self.assertEqual(len(self.n.sent), 1)  # one single "no local network" notice
        self.assertIn("Sin red local", self.n.texts()[0])
        self.drive(ctl, 120, UP())
        self.assertTrue(any("Red local recuperada" in t for t in self.n.texts()))

    def test_down_then_wol_recovers(self):
        ctl = self.controller()
        self.drive(ctl, 30 * 3, DOWN)  # three failed cycles -> DOWN
        self.assertEqual(self.state["status"], "DOWN")
        self.assertEqual(self.w.calls, [])  # grace period: no action yet
        self.drive(ctl, 300 + 60, DOWN)
        self.assertEqual(len(self.w.calls), 1)
        self.assertEqual(self.w.calls[0], ("c8:ff:bf:08:5e:db", "192.168.0.255"))
        self.drive(ctl, 90, UP())
        self.assertEqual(self.state["status"], "UP")
        self.assertTrue(any("recuperado tras 1 paquete" in t for t in self.n.texts()))
        self.assertEqual(self.state["recovery"]["phase"], "IDLE")

    def test_two_wols_then_plug_then_give_up(self):
        cfg = make_cfg(plug={"type": "shelly_gen1", "host": "192.168.0.50"})
        ctl = self.controller(cfg, plug=FakePlug(configured=True))
        self.drive(ctl, 90 + 300 + 30, DOWN)
        self.assertEqual(len(self.w.calls), 1)
        self.drive(ctl, 240, DOWN)
        self.assertEqual(len(self.w.calls), 2)
        self.drive(ctl, 240, DOWN)
        self.assertEqual(self.plug.cycles, 1)
        self.drive(ctl, 240, DOWN)
        self.assertEqual(self.state["recovery"]["phase"], "GAVE_UP")
        self.assertEqual(self.n.sent[-1][0], "crit")
        calls = len(self.w.calls)
        self.drive(ctl, 3 * 3600, DOWN)  # gave up: only reminders, never more actions
        self.assertEqual(len(self.w.calls), calls)
        self.assertEqual(self.plug.cycles, 1)
        self.assertGreaterEqual(sum("ya no act" in t for t in self.n.texts()), 2)

    def test_without_plug_gives_up_after_two_wols(self):
        ctl = self.controller()
        self.drive(ctl, 90 + 300 + 30 + 240 + 240 + 60, DOWN)
        self.assertEqual(len(self.w.calls), 2)
        self.assertEqual(self.state["recovery"]["phase"], "GAVE_UP")
        self.assertIn("no hay enchufe", self.n.texts()[-1])

    def test_plug_failure_gives_up(self):
        cfg = make_cfg(plug={"type": "tasmota", "host": "192.168.0.50"})
        ctl = self.controller(cfg, plug=FakePlug(configured=True, fail=True))
        self.drive(ctl, 90 + 300 + 240 + 240 + 120, DOWN)
        self.assertEqual(self.state["recovery"]["phase"], "GAVE_UP")
        self.assertIn("No pude manejar el enchufe", self.n.texts()[-1])

    def test_hot_before_down_waits_longer(self):
        ctl = self.controller()
        self.drive(ctl, 600, UP(prom={"temp_c": 86.0, "on_battery": False, "uptime_s": 5.0}))
        self.drive(ctl, 90, DOWN)
        self.assertIn("caliente", self.n.texts()[-1])
        self.drive(ctl, 300 + 60, DOWN)  # grace alone is not enough when it was hot
        self.assertEqual(self.w.calls, [])
        self.drive(ctl, 1200, DOWN)
        self.assertEqual(len(self.w.calls), 1)

    def test_cool_lab_waits_only_the_grace(self):
        ctl = self.controller()
        self.drive(ctl, 600, UP(prom={"temp_c": 55.0, "on_battery": False, "uptime_s": 5.0}))
        self.drive(ctl, 90 + 300 + 60, DOWN)
        self.assertEqual(len(self.w.calls), 1)

    def test_on_battery_is_reported(self):
        ctl = self.controller()
        self.drive(ctl, 60, UP(prom={"temp_c": 40.0, "on_battery": True, "uptime_s": 5.0}))
        self.drive(ctl, 90, DOWN)
        self.assertIn("EN BATER", self.n.texts()[-1])
        self.assertEqual(self.n.sent[-1][0], "crit")

    def test_maintenance_suppresses_everything_and_pauses_timers(self):
        flag = {"on": True}
        ctl = self.controller(maint=lambda now: flag["on"])
        self.drive(ctl, 3600, DOWN)
        self.assertEqual(self.n.sent, [])
        self.assertEqual(self.w.calls, [])
        flag["on"] = False
        self.drive(ctl, 30, DOWN)
        self.assertTrue(any("no responde" in t for t in self.n.texts()))  # announced after the window
        self.assertEqual(self.w.calls, [])  # timers restarted after the window
        self.drive(ctl, 300 + 60, DOWN)
        self.assertEqual(len(self.w.calls), 1)

    def test_dry_run_sends_nothing_real(self):
        cfg = make_cfg(mode={"dry_run": True}, plug={"type": "shelly_gen1", "host": "192.168.0.50"})
        ctl = self.controller(cfg, plug=FakePlug(configured=True))
        self.drive(ctl, 90 + 300 + 240 + 240 + 240 + 120, DOWN)
        self.assertEqual(self.w.calls, [])
        self.assertEqual(self.plug.cycles, 0)
        sim = [t for t in self.n.texts() if "SIMULACI" in t]
        self.assertGreaterEqual(len(sim), 3)  # wol1, wol2, plug

    def test_hourly_limit_blocks_a_second_sequence(self):
        cfg = make_cfg(limits={"max_recoveries_per_hour": 1})
        ctl = self.controller(cfg)
        self.drive(ctl, 90 + 300 + 60, DOWN)
        self.assertEqual(len(self.w.calls), 1)
        self.drive(ctl, 60, UP())
        self.drive(ctl, 90 + 300 * 2 + 120, DOWN)  # flapping: waits are doubled, still inside the hour
        self.assertEqual(len(self.w.calls), 1)
        self.assertEqual(self.state["recovery"]["phase"], "GAVE_UP")
        self.assertIn("Límite de intentos", self.n.texts()[-1])

    def test_flapping_doubles_the_wait(self):
        ctl = self.controller()
        self.drive(ctl, 90 + 300 + 60, DOWN)
        self.drive(ctl, 60, UP())
        self.drive(ctl, 90, DOWN)
        self.assertEqual(self.state["flap_mult"], 2)
        self.assertEqual(self.state["recovery"]["wait"], 600)
        self.assertIn("duplico la espera", self.n.texts()[-1])

    def test_slow_recovery_without_action_is_reported_as_self_recovery(self):
        ctl = self.controller()
        self.drive(ctl, 90 + 120, DOWN)
        self.drive(ctl, 60, UP())
        self.assertTrue(any("por sí solo" in t for t in self.n.texts()))
        self.assertEqual(self.w.calls, [])

    def test_daily_summary_is_silent_and_once(self):
        cfg = make_cfg(notify={"daily_summary_hour": time.localtime(self.now).tm_hour})
        ctl = self.controller(cfg)
        self.drive(ctl, 1800, UP(prom={"temp_c": 41.0, "on_battery": False, "uptime_s": 9.0}))
        summaries = [s for s in self.n.sent if "Latido diario" in s[1]]
        self.assertEqual(len(summaries), 1)
        self.assertTrue(summaries[0][2])  # silent


class ComponentTests(Base):
    def test_magic_packet_format(self):
        pkt = lw.magic_packet("c8:ff:bf:08:5e:db")
        self.assertEqual(len(pkt), 102)
        self.assertEqual(pkt[:6], b"\xff" * 6)
        self.assertEqual(pkt[6:12], bytes.fromhex("c8ffbf085edb"))
        self.assertEqual(pkt[6:], bytes.fromhex("c8ffbf085edb") * 16)
        with self.assertRaises(ValueError):
            lw.magic_packet("c8:ff:bf")

    def test_prom_scalar(self):
        ok = {"status": "success", "data": {"resultType": "vector", "result": [{"metric": {}, "value": [1, "39.6"]}]}}
        self.assertAlmostEqual(lw.prom_scalar(ok), 39.6)
        self.assertIsNone(lw.prom_scalar({"data": {"result": []}}))
        self.assertIsNone(lw.prom_scalar({"unexpected": 1}))
        self.assertIsNone(lw.prom_scalar({"data": {"result": [{"value": [1, "NaN-x"]}]}}))

    def test_config_validation(self):
        self.assertEqual(lw.validate_config(make_cfg()), [])
        bad = make_cfg(lab={"mac": "nope", "ip": "999.1.1.1"}, plug={"type": "shelly_gen1", "host": ""},
                       notify={"chat_id": "abc"}, intervals={"poll_seconds": 0})
        errors = " | ".join(lw.validate_config(bad))
        for needle in ("lab.mac", "lab.ip", "plug.host", "notify.chat_id", "poll_seconds"):
            self.assertIn(needle, errors)
        self.assertIn("lab.mac", " ".join(lw.validate_config(lw.copy.deepcopy(lw.DEFAULTS))))  # MAC is mandatory

    def test_outbox_keeps_alerts_until_internet_returns(self):
        cfg, state, t = make_cfg(), lw.new_state(), {"now": 1000.0}
        delivered, up = [], {"ok": False}

        def sender(token, chat, text, silent):
            if not up["ok"]:
                raise RuntimeError("telegram send failed (URLError)")
            delivered.append(text)

        n = lw.Notifier(cfg, state, token="T", sender=sender, now=lambda: t["now"])
        n.send("crit", "primera")
        n.send("warn", "segunda")
        self.assertEqual(delivered, [])
        self.assertEqual(len(state["outbox"]), 2)
        up["ok"], t["now"] = True, 1000.0 + 600
        n.flush()
        self.assertEqual(len(delivered), 2)
        self.assertIn("con retraso", delivered[0])
        self.assertEqual(state["outbox"], [])

    def test_notifier_never_sends_without_token(self):
        sent = []
        n = lw.Notifier(make_cfg(), lw.new_state(), token="", sender=lambda *a: sent.append(a))
        n.send("info", "hola")
        self.assertEqual(sent, [])

    def test_relay_keeps_the_report_while_the_lab_is_down_and_delivers_it_after_recovery(self):
        cfg = make_cfg(notify={"relay_enabled": True})
        state, t = lw.new_state(), {"now": 5000.0}
        got, lab_up = [], {"ok": False}

        def relay(c, level, text, when):
            if not lab_up["ok"]:
                raise RuntimeError("relay failed (ConnectionRefusedError)")
            got.append((level, text, when))

        n = lw.Notifier(cfg, state, relay=relay, now=lambda: t["now"])
        n.send("crit", "El lab no responde desde las 03:10")
        self.assertEqual(got, [])
        self.assertEqual(len(state["outbox"]), 1)
        lab_up["ok"], t["now"] = True, 5000.0 + 900
        n.flush()
        self.assertEqual(got[0][2], 5000.0)       # the event keeps the time it was detected
        self.assertIn("ocurri", got[0][1])
        self.assertEqual(state["outbox"], [])

    def test_relay_payload_is_an_alertmanager_event(self):
        a = lw.relay_alert("crit", "linea 1\nlinea 2", 1700000000.0)[0]
        self.assertEqual(a["labels"]["severity"], "critical")
        self.assertEqual(a["labels"]["alertname"], "LabWatchdogEvento")
        self.assertEqual(a["annotations"]["summary"], "linea 1")
        self.assertEqual(a["annotations"]["description"], "linea 1\nlinea 2")
        self.assertTrue(a["startsAt"].endswith("Z") and a["endsAt"] > a["startsAt"])

    def test_relay_errors_are_runtime_errors_without_secrets(self):
        cfg = make_cfg(notify={"relay_enabled": True})
        with self.assertRaises(RuntimeError):
            lw.relay_send(dict(cfg, lab=dict(cfg["lab"], ip="127.0.0.1"), prometheus=dict(cfg["prometheus"], port=9)),
                          "info", "x", 1.0)

    def test_default_channel_is_relay_not_telegram(self):
        self.assertTrue(lw.DEFAULTS["notify"]["relay_enabled"])
        self.assertFalse(lw.DEFAULTS["notify"]["telegram_enabled"])

    def test_telegram_errors_do_not_leak_the_token(self):
        secret = "123456:SUPERSECRETTOKEN"
        original = lw.urllib.request.urlopen

        def boom(*a, **k):
            raise OSError("connection failed for https://api.telegram.org/bot%s/sendMessage" % secret)
        lw.urllib.request.urlopen = boom
        try:
            with self.assertRaises(RuntimeError) as ctx:
                lw.telegram_send(secret, "1", "x", False)
        finally:
            lw.urllib.request.urlopen = original
        self.assertNotIn("SUPERSECRET", str(ctx.exception))

    def test_state_roundtrip_is_atomic_and_tolerates_garbage(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "sub", "state.json")
            state = lw.new_state()
            state["status"], state["history"] = "DOWN", [1.0, 2.0]
            lw.save_state(path, state)
            self.assertEqual(lw.load_state(path)["history"], [1.0, 2.0])
            self.assertEqual([f for f in os.listdir(os.path.dirname(path)) if f.startswith(".state-")], [])
            with open(path, "w") as handle:
                handle.write("{not json")
            self.assertEqual(lw.load_state(path)["status"], "UNKNOWN")

    def test_maintenance_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "m")
            active = lw.maintenance_checker(path)
            self.assertFalse(active(100.0))
            with open(path, "w") as handle:
                handle.write("200")
            self.assertTrue(active(100.0))
            self.assertFalse(active(300.0))

    def test_plug_urls(self):
        calls = []
        for kind, expect in (("shelly_gen1", "/relay/0?turn=off"), ("shelly_gen2", "/rpc/Switch.Set?id=0&on=false"),
                             ("tasmota", "/cm?cmnd=Power%20Off")):
            plug = lw.Plug({"plug": {"type": kind, "host": "10.0.0.5", "off_seconds": 0}})
            plug._get = calls.append  # type: ignore[assignment]
            plug.cycle(sleep=lambda s: None)
            self.assertTrue(calls[-2].endswith(expect), calls[-2])
        self.assertFalse(lw.Plug({"plug": {"type": "none"}}).configured)

    def test_plug_status_is_read_only(self):
        urls = []
        for kind, expect in (("shelly_gen1", "/relay/0"), ("shelly_gen2", "/rpc/Switch.GetStatus?id=0"),
                             ("tasmota", "/cm?cmnd=Power")):
            plug = lw.Plug({"plug": {"type": kind, "host": "10.0.0.5"}})
            original = lw.urllib.request.urlopen

            class Resp:
                def __enter__(self): return self
                def __exit__(self, *a): return False
                def read(self): return b'{"ison":true}'

            lw.urllib.request.urlopen = lambda url, timeout=0: (urls.append(url), Resp())[1]
            try:
                self.assertIn("ison", plug.status())
            finally:
                lw.urllib.request.urlopen = original
            self.assertTrue(urls[-1].endswith(expect), urls[-1])
            self.assertNotIn("turn=", urls[-1])
            self.assertNotIn("on=", urls[-1].replace("Switch.GetStatus", ""))

    def test_env_file_loader_does_not_override(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "secrets.env")
            with open(path, "w") as handle:
                handle.write('# c\nLW_TEST_A="from-file"\nLW_TEST_B=from-file\n')
            os.environ["LW_TEST_B"] = "already"
            self.addCleanup(lambda: [os.environ.pop(k, None) for k in ("LW_TEST_A", "LW_TEST_B")])
            lw.load_env_file(path)
            self.assertEqual(os.environ["LW_TEST_A"], "from-file")
            self.assertEqual(os.environ["LW_TEST_B"], "already")


class IncidentReplay(Base):
    """Replay of the 2026-10-16 incident: the lab was hot, vanished at 23:05 and stayed off 9 hours."""

    def test_replay(self):
        cfg = make_cfg(plug={"type": "shelly_gen1", "host": "192.168.0.50"})
        ctl = self.controller(cfg, plug=FakePlug(configured=True))
        self.drive(ctl, 1200, UP(prom={"temp_c": 81.0, "on_battery": False, "uptime_s": 99999.0}))
        self.drive(ctl, 9 * 3600, DOWN)
        first = self.n.texts()[0]
        self.assertIn("81", first)
        self.assertIn("caliente", first)
        # it waited the cooldown before the first Wake-on-LAN and never exceeded the hourly limit
        self.assertEqual(len(self.w.calls), 2)
        self.assertEqual(self.plug.cycles, 1)
        self.assertEqual(self.state["recovery"]["phase"], "GAVE_UP")
        self.assertEqual(len(self.state["history"]), 1)


class ImmediatePolicyTests(Base):
    """'Wake it as soon as it is surely off, whatever happens': no grace, no heat wait, keep retrying."""

    def cfg_now(self, **extra):
        return make_cfg(intervals={"grace_seconds": 0, "cooldown_seconds": 0, "down_after_failures": 2,
                                   "retry_seconds": 600, **extra},
                        limits={"max_recoveries_per_hour": 2, "max_recoveries_per_day": 4})

    def test_wakes_in_the_very_cycle_it_is_declared_down(self):
        ctl = self.controller(self.cfg_now())
        self.drive(ctl, 30, DOWN)  # first failed cycle: only a suspicion
        self.assertEqual(self.w.calls, [])
        self.drive(ctl, 30, DOWN)  # second: surely down -> Wake-on-LAN right away
        self.assertEqual(len(self.w.calls), 1)
        self.assertEqual(self.state["recovery"]["phase"], "WAIT1")

    def test_the_message_does_not_promise_a_wait_that_is_not_there(self):
        ctl = self.controller(self.cfg_now())
        self.drive(ctl, 60, DOWN)
        down_text = [t for t in self.n.texts() if "no responde" in t][0]
        self.assertIn("Lo enciendo ahora", down_text)
        self.assertNotIn("Espero", down_text)
        self.assertNotIn("duplico", down_text)

    def test_a_hot_lab_is_not_made_to_wait(self):
        ctl = self.controller(self.cfg_now())
        self.drive(ctl, 600, UP(prom={"temp_c": 85.0, "on_battery": False, "uptime_s": 9999.0}))
        self.drive(ctl, 60, DOWN)
        self.assertEqual(len(self.w.calls), 1)

    def test_flapping_does_not_slow_it_down(self):
        cfg = self.cfg_now()
        cfg["limits"] = {"max_recoveries_per_hour": 100, "max_recoveries_per_day": 100, "flap_window_seconds": 1800}
        ctl = self.controller(cfg)
        self.drive(ctl, 60, DOWN)
        self.drive(ctl, 60, UP())  # recovered
        self.drive(ctl, 60, DOWN)  # falls again right after: still no extra wait
        self.assertEqual(len(self.w.calls), 2)

    def test_keeps_trying_after_giving_up_even_past_the_limits(self):
        ctl = self.controller(self.cfg_now())
        self.drive(ctl, 3 * 3600, DOWN)
        self.assertEqual(self.state["recovery"]["phase"], "GAVE_UP")
        self.assertGreater(len(self.w.calls), 10)  # 2 normal packets, then a retry every 10 minutes
        self.assertTrue(any("reintento" in t for t in self.n.texts()))
        self.assertEqual(self.state["recovery"]["retries"], len(self.w.calls) - 2)

    def test_retries_stop_when_the_lab_is_back(self):
        ctl = self.controller(self.cfg_now())
        self.drive(ctl, 2 * 3600, DOWN)
        calls = len(self.w.calls)
        self.drive(ctl, 90, UP())
        self.assertEqual(self.state["status"], "UP")
        self.drive(ctl, 3600, UP())
        self.assertEqual(len(self.w.calls), calls)

    def test_retries_are_only_simulated_in_dry_run(self):
        cfg = self.cfg_now()
        cfg["mode"]["dry_run"] = True
        ctl = self.controller(cfg)
        self.drive(ctl, 3 * 3600, DOWN)
        self.assertEqual(self.w.calls, [])
        self.assertTrue(any("habría reintentado" in t for t in self.n.texts()))

    def cfg_fast_giveup(self, **over):
        cfg = self.cfg_now(boot_window_seconds=60, retry_seconds=60)
        cfg["limits"]["give_up_at_second_wake"] = True
        return lw.deep_merge(cfg, over)

    def test_gives_up_as_soon_as_the_second_packet_is_sent_then_retries_every_minute(self):
        ctl = self.controller(self.cfg_fast_giveup())
        self.drive(ctl, 60, DOWN)  # two failed cycles: down, packet 1
        self.assertEqual(len(self.w.calls), 1)
        self.assertEqual(self.state["recovery"]["phase"], "WAIT1")
        self.drive(ctl, 60, DOWN)  # a minute later: packet 2, and the alarm goes off at once
        self.assertEqual(len(self.w.calls), 2)
        self.assertEqual(self.state["recovery"]["phase"], "GAVE_UP")
        self.assertEqual(self.n.sent[-1][0], "crit")
        self.assertIn("segundo paquete", self.n.texts()[-1])
        self.drive(ctl, 600, DOWN)  # then one packet per minute
        self.assertEqual(len(self.w.calls), 2 + 10)
        self.assertEqual(self.state["recovery"]["retries"], 10)

    def test_retries_do_not_flood_the_notifications(self):
        ctl = self.controller(self.cfg_fast_giveup())
        self.drive(ctl, 2 * 3600, DOWN)
        self.assertGreater(len(self.w.calls), 100)  # a packet a minute, for two hours
        self.assertEqual(sum("reintento" in t for t in self.n.texts()), 1)  # but a single "retries begin" message
        self.assertLessEqual(len(self.n.sent), 6)  # down, packet 1, give up, first retry, hourly reminders

    def test_a_configured_plug_still_gets_its_turn_before_giving_up(self):
        cfg = self.cfg_fast_giveup(plug={"type": "shelly_gen1", "host": "192.168.0.50"})
        ctl = self.controller(cfg, plug=FakePlug(configured=True))
        self.drive(ctl, 120, DOWN)
        self.assertEqual(len(self.w.calls), 2)
        self.assertEqual(self.state["recovery"]["phase"], "WAIT2")  # not given up yet: the plug cycle comes next
        self.drive(ctl, 60, DOWN)
        self.assertEqual(self.plug.cycles, 1)

    def test_the_second_packet_alarm_is_off_by_default(self):
        ctl = self.controller(self.cfg_now())  # give_up_at_second_wake not set
        self.drive(ctl, 60, DOWN)
        self.drive(ctl, 240, DOWN)
        self.assertEqual(len(self.w.calls), 2)
        self.assertEqual(self.state["recovery"]["phase"], "WAIT2")

    def test_the_old_policy_is_still_the_default(self):
        ctl = self.controller()  # grace 300 s, no retries
        self.drive(ctl, 90, DOWN)
        self.assertEqual(self.w.calls, [])  # still inside the grace period
        self.drive(ctl, 6 * 3600, DOWN)
        self.assertEqual(len(self.w.calls), 2)  # gave up for good after two packets

    def test_config_validation_for_the_new_waits(self):
        def errors(**intervals):
            cfg = lw.deep_merge(make_cfg(), {"intervals": intervals})
            return [e for e in lw.validate_config(cfg) if "intervals" in e]
        self.assertEqual(errors(grace_seconds=0, cooldown_seconds=0, retry_seconds=600), [])
        self.assertTrue(errors(grace_seconds=-1))
        self.assertTrue(errors(retry_seconds=-5))
        self.assertTrue(errors(poll_seconds=0))
        cfg = lw.deep_merge(make_cfg(), {"limits": {"give_up_at_second_wake": "yes"}})
        self.assertTrue([e for e in lw.validate_config(cfg) if "give_up_at_second_wake" in e])


class ChannelAndExpiryTests(Base):
    """Telegram goes out directly (it works with the lab down); the lab relay is only the fallback."""

    def notifier(self, telegram_ok=True, relay_ok=True, **notify):
        cfg = make_cfg(notify=dict({"telegram_enabled": True, "relay_enabled": True, "chat_id": "42"}, **notify))
        self.state, self.t, self.via = lw.new_state(), {"now": 10_000.0}, []

        def sender(token, chat, text, silent):
            if not telegram_ok:
                raise RuntimeError("telegram send failed (URLError)")
            self.via.append(("telegram", chat, text))

        def relay(c, level, text, when):
            if not relay_ok:
                raise RuntimeError("relay failed (ConnectionRefusedError)")
            self.via.append(("relay", level, text))
        return lw.Notifier(cfg, self.state, token="T", sender=sender, relay=relay, now=lambda: self.t["now"])

    def test_telegram_is_used_first_and_the_relay_stays_quiet(self):
        n = self.notifier()
        n.send("crit", "El lab no responde")
        self.assertEqual([v[0] for v in self.via], ["telegram"])
        self.assertEqual(self.via[0][1], "42")
        self.assertEqual(self.state["outbox"], [])

    def test_the_relay_takes_over_when_telegram_fails(self):
        n = self.notifier(telegram_ok=False)
        n.send("crit", "El lab no responde")
        self.assertEqual([v[0] for v in self.via], ["relay"])
        self.assertEqual(self.state["outbox"], [])

    def test_the_message_waits_when_every_channel_fails(self):
        n = self.notifier(telegram_ok=False, relay_ok=False)
        n.send("crit", "El lab no responde")
        self.assertEqual(self.via, [])
        self.assertEqual(len(self.state["outbox"]), 1)

    def test_without_a_token_only_the_relay_is_tried(self):
        n = self.notifier()
        n.token = ""
        n.send("info", "hola")
        self.assertEqual([v[0] for v in self.via], ["relay"])

    def test_old_queued_events_are_dropped_not_sent_as_a_burst(self):
        n = self.notifier(telegram_ok=False, relay_ok=False, outbox_max_age_seconds=6 * 3600)
        n.send("warn", "vieja 1")
        n.send("warn", "vieja 2")
        self.t["now"] += 7 * 3600  # seven hours later
        n.send("crit", "reciente")  # arrives while still offline: queued with the old ones
        self.assertEqual([i["text"] for i in self.state["outbox"]], ["reciente"])
        n.sender = lambda token, chat, text, silent: self.via.append(("telegram", chat, text))
        n.flush()
        self.assertEqual([v[2].split(" ", 1)[1] for v in self.via], ["reciente"])

    def test_without_an_age_limit_nothing_is_dropped(self):
        n = self.notifier(telegram_ok=False, relay_ok=False)
        n.send("warn", "vieja")
        self.t["now"] += 30 * 24 * 3600
        n.flush()
        self.assertEqual(len(self.state["outbox"]), 1)

    def test_a_telegram_http_error_shows_the_status_but_never_the_token(self):
        secret = "123456:SUPERSECRETTOKEN"
        original = lw.urllib.request.urlopen

        def refused(req, timeout=0):
            raise lw.urllib.error.HTTPError("https://api.telegram.org/bot%s/sendMessage" % secret, 400, "Bad Request", {}, None)
        lw.urllib.request.urlopen = refused
        try:
            with self.assertRaises(RuntimeError) as ctx:
                lw.telegram_send(secret, "42", "hola", False)
        finally:
            lw.urllib.request.urlopen = original
        self.assertIn("telegram HTTP 400", str(ctx.exception))
        self.assertIn("Start", str(ctx.exception))
        self.assertNotIn(secret, str(ctx.exception))
        self.assertNotIn("SUPERSECRET", repr(ctx.exception.__cause__))

    def test_when_every_channel_fails_the_log_names_each_reason(self):
        n = self.notifier(telegram_ok=False, relay_ok=False)
        with self.assertLogs(level="WARNING") as logs:
            n.send("crit", "El lab no responde")
        line = " ".join(logs.output)
        self.assertIn("telegram send failed", line)
        self.assertIn("relay failed", line)

    def test_the_queue_can_be_purged_before_a_new_channel_is_switched_on(self):
        n = self.notifier(telegram_ok=False, relay_ok=False)
        for text in ("a", "b", "c"):
            n.send("warn", text)
        self.assertEqual(lw.purge_outbox(self.state), 3)
        self.assertEqual(self.state["outbox"], [])
        self.assertEqual(lw.purge_outbox(self.state), 0)

    def test_age_limit_validation(self):
        cfg = lw.deep_merge(make_cfg(), {"notify": {"outbox_max_age_seconds": -1}})
        self.assertTrue([e for e in lw.validate_config(cfg) if "outbox_max_age_seconds" in e])
        cfg = lw.deep_merge(make_cfg(), {"notify": {"outbox_max_age_seconds": 21600}})
        self.assertEqual([e for e in lw.validate_config(cfg) if "outbox_max_age_seconds" in e], [])


class FakeTelegramApi:
    """The Bot API: a list of updates to hand out and a record of what the bot answered."""

    def __init__(self):
        self.updates, self.sent, self.calls, self.fail, self.next_id = [], [], [], None, 1

    def add(self, text, chat=42, kind="private", date=1_800_000_000.0):
        self.updates.append({"update_id": self.next_id, "message": {
            "text": text, "date": date, "chat": {"id": chat, "type": kind}}})
        self.next_id += 1

    def __call__(self, token, method, payload, timeout):
        self.calls.append((method, dict(payload)))
        if self.fail:
            raise RuntimeError(self.fail)
        if method == "getUpdates":
            if payload.get("offset") == -1:
                return {"ok": True, "result": self.updates[-1:]}
            return {"ok": True, "result": [u for u in self.updates if u["update_id"] >= payload.get("offset", 0)]}
        if method == "sendMessage":
            self.sent.append(payload["text"])
            return {"ok": True, "result": {}}
        raise AssertionError("unexpected method %s" % method)


class FakeIndicator:
    def __init__(self, sounding=True):
        self.sounding, self.silenced = sounding, 0

    def silence(self):
        self.silenced += 1
        return self.sounding


class CommandBotTests(Base):
    NOW = 1_800_000_000.0 + 10

    def setUp(self):
        super().setUp()
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.maint = os.path.join(self.tmp.name, "maintenance_until")
        self.api, self.waker, self.state = FakeTelegramApi(), FakeWaker(), lw.new_state()
        self.make_bot()

    def make_bot(self, dry_run=False, indicator=None, **commands):
        cfg = make_cfg(notify={"chat_id": "42", "telegram_enabled": True}, mode={"dry_run": dry_run},
                       commands=dict({"enabled": True}, **commands), paths={"maintenance": self.maint})
        self.bot = lw.CommandBot(cfg, self.state, "T0KEN", api=self.api, waker=self.waker, indicator=indicator)

    def ask(self, text, **kw):
        """Send one command through a real poll and return the answer (None = no answer)."""
        self.api.sent.clear()
        self.api.add(text, **kw)
        self.bot.state["tg_offset"] = self.bot.state.get("tg_offset") or 0
        self.bot.poll_once(self.NOW)
        return self.api.sent[-1] if self.api.sent else None

    def test_commands_that_were_queued_before_the_start_are_never_run(self):
        self.api.add("/encender")
        self.api.add("/encender")
        self.assertEqual(self.bot.poll_once(self.NOW), 0)  # first poll after a fresh start: drop the backlog
        self.assertEqual(self.bot.poll_once(self.NOW), 0)
        self.assertEqual(self.waker.calls, [])
        self.assertEqual(self.api.sent, [])

    def test_estado_when_the_lab_is_up(self):
        self.state.update(status="UP", last_known={"temp_c": 35.4, "on_battery": False, "at": self.NOW})
        reply = self.ask("/estado")
        self.assertIn("responde", reply)
        self.assertIn("35 °C", reply)
        self.assertIn("SAI en red", reply)
        self.assertIn("modo real", reply)

    def test_estado_when_the_lab_is_down_and_the_watchdog_keeps_retrying(self):
        self.state.update(status="DOWN", down_since=self.NOW - 600, recovery={"phase": "GAVE_UP"})
        self.bot.cfg["intervals"]["retry_seconds"] = 60
        reply = self.ask("/estado")
        self.assertIn("CAÍDO", reply)
        self.assertIn("10 min", reply)
        self.assertIn("reintentando el Wake-on-LAN cada 1 min", reply)

    def test_estado_mentions_maintenance_and_the_queue(self):
        self.state.update(status="UP", outbox=[{}, {}])
        self.ask("/mantenimiento 30")
        reply = self.ask("/estado")
        self.assertIn("mantenimiento", reply.lower())
        self.assertIn("pendientes de enviar: 2", reply)

    def test_encender_sends_a_wake_on_lan_when_the_lab_is_down(self):
        self.state.update(status="DOWN")
        reply = self.ask("/encender")
        self.assertEqual(self.waker.calls, [("c8:ff:bf:08:5e:db", "192.168.0.255")])
        self.assertIn("enviado", reply)

    def test_encender_does_nothing_when_the_lab_answers_unless_forced(self):
        self.state.update(status="UP")
        self.assertIn("responde ahora mismo", self.ask("/encender"))
        self.assertEqual(self.waker.calls, [])
        self.ask("/encender forzar")
        self.assertEqual(len(self.waker.calls), 1)

    def test_encender_has_a_cooldown(self):
        self.state.update(status="DOWN")
        self.ask("/encender")
        self.assertIn("espera", self.ask("/encender"))
        self.assertEqual(len(self.waker.calls), 1)

    def test_encender_sends_nothing_in_simulation_mode(self):
        self.make_bot(dry_run=True)
        self.state.update(status="DOWN")
        self.assertIn("SIMULACIÓN", self.ask("/encender forzar"))
        self.assertEqual(self.waker.calls, [])

    def test_mantenimiento_pauses_and_reanudar_resumes(self):
        reply = self.ask("/mantenimiento 30")
        self.assertIn("Mantenimiento hasta", reply)
        self.assertTrue(lw.maintenance_checker(self.maint)(self.NOW + 60))
        self.assertFalse(lw.maintenance_checker(self.maint)(self.NOW + 31 * 60))
        self.assertIn("terminado", self.ask("/reanudar"))
        self.assertFalse(os.path.exists(self.maint))

    def test_mantenimiento_rejects_nonsense(self):
        for arg in ("", "abc", "0", "-5", "721", "99999"):
            self.assertIn("Uso:", self.ask("/mantenimiento " + arg), arg)
        self.assertFalse(os.path.exists(self.maint))

    def test_silenciar_with_and_without_an_indicator(self):
        self.assertIn("no está activado", self.ask("/silenciar"))
        quiet = FakeIndicator(sounding=False)
        self.make_bot(indicator=quiet)
        self.assertIn("nada sonando", self.ask("/silenciar"))
        loud = FakeIndicator(sounding=True)
        self.make_bot(indicator=loud)
        self.assertEqual(self.ask("/silenciar"), "Zumbador silenciado.")
        self.assertEqual(loud.silenced, 1)

    def test_other_chats_get_no_answer_and_cannot_do_anything(self):
        self.state.update(status="DOWN")
        self.assertIsNone(self.ask("/encender", chat=999))
        self.assertIsNone(self.ask("/mantenimiento 30", chat=999))
        self.assertIsNone(self.ask("/estado", chat=999))
        self.assertEqual(self.waker.calls, [])
        self.assertFalse(os.path.exists(self.maint))

    def test_a_group_with_the_same_id_is_not_accepted(self):
        self.state.update(status="DOWN")
        self.assertIsNone(self.ask("/encender", chat=42, kind="group"))
        self.assertEqual(self.waker.calls, [])

    def test_old_commands_are_ignored(self):
        self.state.update(status="DOWN")
        self.assertIsNone(self.ask("/encender", date=self.NOW - 301))
        self.assertEqual(self.waker.calls, [])

    def test_each_command_runs_once_even_if_polled_again(self):
        self.state.update(status="DOWN")
        self.ask("/encender")
        self.bot.cooldown = 0
        self.bot.poll_once(self.NOW + 1)
        self.bot.poll_once(self.NOW + 2)
        self.assertEqual(len(self.waker.calls), 1)  # the offset moved past it before it ran

    def test_plain_text_is_ignored_and_unknown_commands_get_the_help(self):
        self.assertIsNone(self.ask("hola"))
        reply = self.ask("/borrarlotodo")
        self.assertIn("No conozco esa orden", reply)
        self.assertIn("/estado", reply)
        self.assertIn("/ayuda", self.ask("/ayuda"))

    def test_the_bot_name_suffix_and_capitals_are_accepted(self):
        self.state.update(status="UP")
        self.assertIn("responde", self.ask("/Estado@StrateCodeWatchdogBot"))

    def test_there_is_no_command_that_could_shut_the_lab_down(self):
        for name in ("/apagar", "/reiniciar", "/ssh", "/shell", "/ejecutar"):
            self.assertIn("No conozco", self.ask(name))

    def test_a_failing_api_never_stops_the_loop_nor_leaks_the_token(self):
        stop = threading.Event()
        calls = []

        def failing(token, method, payload, timeout):
            calls.append(method)
            stop.set()  # one failed attempt is enough for the test
            raise RuntimeError("telegram getUpdates failed (URLError)")
        self.bot.api = failing
        self.state["tg_offset"] = 0
        with self.assertLogs(level="WARNING") as logs:
            self.bot.run(stop)
        self.assertEqual(calls, ["getUpdates"])
        self.assertNotIn("T0KEN", " ".join(logs.output))

    def test_a_failed_answer_does_not_raise(self):
        self.state.update(status="UP")
        self.api.add("/estado")
        self.state["tg_offset"] = 0
        original = self.api.__call__

        def broken_send(token, method, payload, timeout):
            if method == "sendMessage":
                raise RuntimeError("telegram sendMessage failed (URLError)")
            return original(token, method, payload, timeout)
        self.bot.api = broken_send
        with self.assertLogs(level="WARNING") as logs:
            self.bot.poll_once(self.NOW)  # must not raise
        self.assertIn("could not answer", " ".join(logs.output))
        self.assertEqual(self.state["tg_offset"], 2)  # the command was still consumed: it will not run twice

    def test_config_validation(self):
        def errors(telegram=True, **commands):
            cfg = lw.deep_merge(make_cfg(), {"commands": commands, "notify": {"telegram_enabled": telegram}})
            return [e for e in lw.validate_config(cfg) if "commands" in e]
        self.assertEqual(errors(enabled=True), [])
        self.assertTrue(errors(telegram=False, enabled=True))  # commands need the Telegram bot
        self.assertTrue(errors(enabled=True, poll_timeout_seconds=0))
        self.assertTrue(errors(enabled=True, poll_timeout_seconds=99))
        self.assertTrue(errors(enabled=True, max_age_seconds=-1))
        self.assertTrue(errors(enabled="yes"))

    def test_the_real_api_call_hides_the_token_in_errors(self):
        original = lw.urllib.request.urlopen

        def refused(req, timeout=0):
            raise lw.urllib.error.HTTPError(req.full_url, 401, "Unauthorized", {}, None)
        lw.urllib.request.urlopen = refused
        try:
            with self.assertRaises(RuntimeError) as ctx:
                lw.telegram_api("123456:SUPERSECRETTOKEN", "getUpdates", {}, 5)
        finally:
            lw.urllib.request.urlopen = original
        self.assertEqual(str(ctx.exception), "telegram getUpdates HTTP 401")
        self.assertNotIn("SUPERSECRET", repr(ctx.exception.__cause__))


class FakeGpio:
    """Stands in for PinctrlBackend: records levels, a settable button, and failures on demand."""

    def __init__(self):
        self.fail, self.levels, self.pressed = False, {}, False
        self.setups = self.reads = self.cleanups = 0

    def _check(self):
        if self.fail:
            raise OSError("gpio unavailable")

    def setup(self, outputs, inputs):
        self._check()
        self.setups += 1
        self.levels.update({pin: False for pin in outputs})

    def write(self, pin, level):
        self._check()
        self.levels[pin] = level

    def read(self, pin):
        self._check()
        self.reads += 1
        return not self.pressed  # pull-up: low means pressed

    def cleanup(self, outputs):
        self.cleanups += 1


class IndicatorTests(Base):
    GREEN, RED, BUZZER, STEP = 27, 22, 17, 0.1

    def setUp(self):
        super().setUp()
        self.cfg = make_cfg(indicator={"enabled": True})
        self.noon = time.mktime((2026, 10, 18, 12, 0, 0, 0, 0, -1))
        self.night = time.mktime((2026, 10, 18, 23, 30, 0, 0, 0, -1))
        self.new_indicator()

    def new_indicator(self):
        self.gpio, self.t, self.samples, self.attempts = FakeGpio(), 1000.0, [], 0
        self.ready_samples = []

        def factory():
            self.attempts += 1
            return self.gpio
        self.ind = lw.Indicator(self.cfg, backend_factory=factory)

    def run_for(self, seconds):
        end = self.t + seconds
        while self.t < end - 1e-9:
            self.ind.tick(self.t)
            lv = self.gpio.levels
            self.samples.append((self.t, lv.get(self.GREEN, False), lv.get(self.RED, False), lv.get(self.BUZZER, False)))
            self.ready_samples.append(lv.get(24, False))
            self.t += self.STEP

    def feed(self, status, phase="IDLE", actions=(), net_down=False, maint=False, wall=None):
        state = {"status": status, "net_down": net_down, "recovery": {"phase": phase, "actions": list(actions)}}
        self.ind.update(state, maint, wall or self.noon, self.t)

    def column(self, idx, since=0):
        return [s[idx] for s in self.samples if s[0] >= since]

    def pulses(self):
        """Lengths (s) of every buzzer-on stretch."""
        out, run = [], 0
        for _, _, _, buzzer in self.samples:
            if buzzer:
                run += 1
            elif run:
                out.append(run * self.STEP)
                run = 0
        return out + ([run * self.STEP] if run else [])

    @staticmethod
    def edges(values):
        return sum(1 for a, b in zip(values, values[1:]) if a != b)

    # ---- state -> mode
    def test_mode_table(self):
        rec = lambda phase: {"phase": phase}  # noqa: E731
        cases = [
            ({"status": "UP"}, False, "UP"),
            ({"status": "DEGRADED"}, False, "SUSPECT"),
            ({"status": "SUSPECT"}, False, "SUSPECT"),
            ({"status": "DOWN", "recovery": rec("GRACE")}, False, "DOWN"),
            ({"status": "DOWN", "recovery": rec("WAIT1")}, False, "WOL_SENT"),
            ({"status": "DOWN", "recovery": rec("WAIT2")}, False, "WOL_SENT"),
            ({"status": "DOWN", "recovery": rec("WAIT3")}, False, "WOL_SENT"),
            ({"status": "DOWN", "recovery": rec("GAVE_UP")}, False, "GAVE_UP"),
            ({"status": "DOWN", "recovery": rec("GAVE_UP"), "net_down": True}, False, "NETWORK_DOWN"),
            ({"status": "UP", "net_down": True}, True, "MAINTENANCE"),
            ({"status": "UNKNOWN"}, False, None),
            ({}, False, None),
        ]
        for state, maint, expected in cases:
            self.assertEqual(lw.indicator_mode(state, maint), expected, state)

    # ---- one test per row of the signal table
    def test_up_is_steady_green_and_silent(self):
        self.feed("UP")
        self.run_for(5)
        self.assertTrue(all(self.column(1)))
        self.assertFalse(any(self.column(2)))
        self.assertEqual(self.pulses(), [])
        self.assertEqual(self.gpio.reads, 0)  # the button is not polled while nothing sounds

    def test_suspect_blinks_green_slowly(self):
        self.feed("SUSPECT")
        self.run_for(8)
        green = self.column(1)
        self.assertTrue(any(green) and not all(green))
        self.assertIn(self.edges(green), range(6, 10))  # 1 s on / 1 s off
        self.assertFalse(any(self.column(2)))

    def test_down_in_grace_blinks_red_slowly_and_is_silent(self):
        self.feed("UP")
        self.feed("DOWN", "GRACE")
        self.run_for(8)
        red = self.column(2)
        self.assertTrue(any(red) and not all(red))
        self.assertIn(self.edges(red), range(6, 10))
        self.assertFalse(any(self.column(1)))
        self.assertEqual(self.pulses(), [])

    def test_wol_sent_is_solid_red_with_one_short_beep(self):
        self.feed("UP")
        self.feed("DOWN", "WAIT1", ["wol1"])
        self.run_for(3)
        self.assertTrue(all(self.column(2)))
        self.assertFalse(any(self.column(1)))
        pulses = self.pulses()
        self.assertEqual(len(pulses), 1)
        self.assertLessEqual(pulses[0], 0.3)

    def test_second_wol_beeps_again(self):
        self.feed("UP")
        self.feed("DOWN", "WAIT1", ["wol1"])
        self.run_for(3)
        self.feed("DOWN", "WAIT2", ["wol1", "wol2"])
        self.run_for(3)
        self.assertEqual(len(self.pulses()), 2)

    def test_recovered_is_green_with_two_short_beeps(self):
        self.feed("UP")
        self.feed("DOWN", "WAIT1", ["wol1"])
        self.run_for(2)
        self.feed("UP")
        self.run_for(3)
        self.assertTrue(all(self.column(1, since=self.t - 3)))
        self.assertFalse(any(self.column(2, since=self.t - 3)))
        self.assertEqual(len(self.pulses()), 1 + 2)  # the WoL beep, then the two of "recovered"

    def test_gave_up_blinks_red_fast_and_repeats_three_long_beeps(self):
        self.feed("UP")
        self.feed("DOWN", "GAVE_UP")
        self.run_for(5)
        red = self.column(2)
        self.assertGreaterEqual(self.edges(red), 15)  # 0.25 s half period
        self.assertFalse(any(self.column(1)))
        first = self.pulses()
        self.assertEqual(len(first), 3)
        self.assertTrue(all(p >= 0.5 for p in first))
        self.run_for(600)
        self.assertEqual(len(self.pulses()), 6)  # reminder after 10 minutes

    def test_network_down_alternates_leds(self):
        self.feed("UP", net_down=True)
        self.run_for(4)
        green, red = self.column(1), self.column(2)
        self.assertTrue(all(g != r for g, r in zip(green, red)))
        self.assertTrue(any(green) and any(red))
        self.assertEqual(self.pulses(), [])

    def test_maintenance_blinks_both_together_without_beeps(self):
        self.feed("UP")
        self.feed("DOWN", "WAIT1", ["wol1"], maint=True)
        self.run_for(10)
        green, red = self.column(1), self.column(2)
        self.assertEqual(green, red)
        self.assertTrue(any(green) and not all(green))
        self.assertEqual(self.pulses(), [])

    def test_startup_self_test(self):
        start = self.t
        self.ind.start(self.t, self.noon)
        self.run_for(4)
        during = [s for s in self.samples if s[0] > start and s[0] < start + 1.3]
        after = [s for s in self.samples if s[0] > start + 1.8]
        self.assertTrue(all(g and r for _, g, r, _ in during))
        self.assertFalse(any(g or r for _, g, r, _ in after))  # no state yet: dark
        self.assertEqual(len(self.pulses()), 1)

    # ---- the optional blue "Pi ready" LED
    def with_ready_led(self):
        self.cfg = make_cfg(indicator={"enabled": True, "pins": {"ready": 24}})
        self.new_indicator()

    def test_ready_led_is_lit_while_the_loop_reports(self):
        self.with_ready_led()
        self.feed("UP")
        self.run_for(5)
        self.assertTrue(all(self.ready_samples[1:]))

    def test_ready_led_goes_dark_if_the_loop_stalls_and_returns_when_it_reports(self):
        self.with_ready_led()
        self.feed("UP")
        self.run_for(60)
        self.assertTrue(self.ready_samples[-1])
        self.run_for(60)  # nobody calls update(): more than 90 s of silence
        self.assertFalse(self.ready_samples[-1])
        self.feed("UP")
        self.run_for(2)
        self.assertTrue(self.ready_samples[-1])

    def test_ready_led_is_dark_after_shutdown(self):
        self.with_ready_led()
        self.feed("UP")
        self.run_for(2)
        self.assertTrue(self.gpio.levels[24])
        self.ind.shutdown()
        self.assertFalse(self.gpio.levels[24])

    def test_ready_led_is_on_during_the_demo(self):
        self.with_ready_led()
        lw.run_demo(self.ind, 1.0, lambda: self.t, lambda s: setattr(self, "t", self.t + s),
                    lambda text: setattr(self.gpio, "pressed", True) if text.startswith("GAVE_UP") else None)
        self.assertTrue(self.gpio.levels[24])

    def test_without_a_ready_pin_nothing_else_is_driven(self):
        self.feed("UP")
        self.run_for(5)
        self.assertNotIn(24, self.gpio.levels)

    def test_ready_pin_validation(self):
        def errors(**pins):
            cfg = lw.deep_merge(make_cfg(), {"indicator": {"enabled": True, "pins": pins}})
            return [e for e in lw.validate_config(cfg) if "indicator" in e]
        self.assertEqual(errors(ready=24), [])
        self.assertEqual(errors(ready=None), [])
        self.assertTrue(errors(ready=17))  # same pin as the buzzer
        self.assertTrue(errors(ready=1))  # not a usable BCM pin

    def test_startup_beep_can_be_turned_off_but_the_led_test_stays(self):
        self.cfg = make_cfg(indicator={"enabled": True, "startup_beep": False})
        self.new_indicator()
        start = self.t
        self.ind.start(self.t, self.noon)
        self.run_for(4)
        during = [s for s in self.samples if s[0] > start and s[0] < start + 1.3]
        self.assertTrue(all(g and r for _, g, r, _ in during))
        self.assertEqual(self.pulses(), [])

    # ---- quiet hours and the button
    def test_quiet_hours_silence_the_buzzer_but_not_gave_up(self):
        self.feed("UP", wall=self.night)
        self.feed("DOWN", "WAIT1", ["wol1"], wall=self.night)
        self.run_for(3)
        self.feed("UP", wall=self.night)  # "recovered" is also quiet at night
        self.run_for(3)
        self.assertEqual(self.pulses(), [])
        self.assertTrue(all(self.column(1, since=self.t - 3)))  # the LEDs still tell the truth
        self.feed("DOWN", "GAVE_UP", wall=self.night)
        self.run_for(4)
        self.assertEqual(len(self.pulses()), 3)

    def test_beeps_sound_in_the_daytime(self):
        self.feed("UP", wall=self.noon)
        self.feed("DOWN", "WAIT1", ["wol1"], wall=self.noon)
        self.run_for(3)
        self.assertEqual(len(self.pulses()), 1)

    def test_quiet_hours_can_be_disabled(self):
        self.cfg = make_cfg(indicator={"enabled": True, "quiet_start": "", "quiet_end": ""})
        self.new_indicator()
        self.feed("UP", wall=self.night)
        self.feed("DOWN", "WAIT1", ["wol1"], wall=self.night)
        self.run_for(3)
        self.assertEqual(len(self.pulses()), 1)

    def test_silence_does_what_the_button_does(self):
        self.assertFalse(self.ind.silence())  # nothing was sounding
        self.feed("UP")
        self.feed("DOWN", "GAVE_UP")
        self.run_for(0.3)
        self.assertTrue(self.samples[-1][3])
        self.assertTrue(self.ind.silence())  # an alarm was sounding
        self.run_for(0.3)
        self.assertFalse(self.samples[-1][3])
        self.assertTrue(self.ind.acked)
        self.run_for(700)  # past the reminder: the "gave up" alarm stays quiet
        self.assertEqual(len(self.pulses()), 1)

    def test_button_silences_gave_up_until_the_state_changes(self):
        self.feed("UP")
        self.feed("DOWN", "GAVE_UP")
        self.run_for(0.3)
        self.assertTrue(self.samples[-1][3])  # the buzzer is sounding
        self.gpio.pressed = True
        self.run_for(0.3)
        self.gpio.pressed = False
        self.assertFalse(self.samples[-1][3])
        self.assertTrue(self.ind.acked)
        self.run_for(700)  # past the 10-minute reminder: stays quiet
        self.assertEqual(len(self.pulses()), 1)  # only the interrupted first beep
        recent_red = self.column(2)[-60:]
        self.assertTrue(any(recent_red) and not all(recent_red))  # the red LED keeps blinking
        self.feed("UP")  # recovered: two short beeps
        self.assertFalse(self.ind.acked)
        self.feed("DOWN", "GAVE_UP")  # a new "gave up" sounds again
        self.run_for(6)
        self.assertEqual(len(self.pulses()), 1 + 2 + 3)

    # ---- failures never reach the watchdog
    def test_missing_hardware_is_ignored_and_retried_later(self):
        self.gpio.fail = True
        self.feed("DOWN", "GAVE_UP")
        self.run_for(10)  # must not raise
        self.assertEqual(self.attempts, 1)  # does not hammer a missing device every tick
        self.assertEqual(self.gpio.setups, 0)
        self.gpio.fail = False
        self.run_for(310)
        self.assertEqual(self.gpio.setups, 1)
        self.assertGreater(self.edges(self.column(2)), 0)

    def test_write_failures_drop_the_backend_and_it_recovers(self):
        self.feed("SUSPECT")  # blinking, so every tick touches the pins
        self.run_for(1)
        self.assertIn(self.GREEN, self.gpio.levels)
        self.gpio.fail = True
        self.run_for(3)  # several failing ticks: no exception
        self.assertIsNone(self.ind.backend)
        self.assertGreaterEqual(self.gpio.cleanups, 1)  # it tried to leave the outputs low
        self.gpio.fail, self.gpio.levels = False, {}
        self.run_for(310)
        self.assertIsNotNone(self.ind.backend)
        self.assertIn(self.GREEN, self.gpio.levels)  # levels are rewritten after the retry

    def test_update_never_raises(self):
        with self.assertLogs(level="ERROR"):
            self.ind.update({"status": "DOWN", "recovery": 5}, False, self.noon, self.t)
        self.ind.update({}, False, self.noon, self.t)

    def test_shutdown_leaves_every_output_low_and_stays_off(self):
        self.feed("UP")
        self.feed("DOWN", "GAVE_UP")
        self.run_for(0.3)
        self.assertTrue(self.gpio.levels[self.BUZZER])
        self.ind.shutdown()
        self.assertFalse(any(self.gpio.levels[p] for p in (self.GREEN, self.RED, self.BUZZER)))
        self.run_for(2)
        self.assertEqual(self.gpio.setups, 1)  # it does not come back to life

    def test_run_loop_stops_when_asked(self):
        stop = threading.Event()
        self.feed("UP")
        self.ind.tick(self.t)
        stop.set()
        self.ind.run(stop, clock=lambda: self.t, period=0)
        self.assertIsNone(self.ind.backend)

    # ---- the real backend, with the process calls replaced
    def test_pinctrl_backend_commands_and_parsing(self):
        calls = []
        answers = {"get": "23: ip    -- | hi // GPIO23 = input\n"}

        def fake_run(cmd, **kwargs):
            calls.append(cmd[1:])
            return mock.Mock(stdout=answers.get(cmd[1], ""))
        backend = lw.PinctrlBackend(binary="/usr/bin/pinctrl")
        with mock.patch.object(lw.subprocess, "run", fake_run):
            backend.setup([17, 27, 22], [23])
            backend.write(27, True)
            backend.write(27, False)
            self.assertTrue(backend.read(23))
            answers["get"] = "23: ip    -- | lo // GPIO23 = input\n"
            self.assertFalse(backend.read(23))
            answers["get"] = "nonsense"
            with self.assertRaises(OSError):
                backend.read(23)
        self.assertEqual(calls[:3], [["set", "17", "op", "dl"], ["set", "27", "op", "dl"], ["set", "22", "op", "dl"]])
        self.assertIn(["set", "23", "ip", "pu"], calls)
        self.assertIn(["set", "27", "op", "dh"], calls)
        self.assertIn(["set", "27", "op", "dl"], calls)

    def test_pinctrl_missing_means_no_indicator_not_a_crash(self):
        with mock.patch.object(lw.shutil, "which", lambda name: None):
            with self.assertRaises(OSError):
                lw.PinctrlBackend()
            ind = lw.Indicator(self.cfg)
            ind.update({"status": "UP"}, False, self.noon, 0.0)
            ind.tick(0.0)  # must not raise
            self.assertIsNone(ind.backend)

    # ---- configuration and hardware test
    def test_config_validation(self):
        def errors(**indicator):
            cfg = lw.deep_merge(make_cfg(), {"indicator": indicator})
            return [e for e in lw.validate_config(cfg) if "indicator" in e]
        self.assertEqual(errors(enabled=True), [])
        self.assertEqual(errors(enabled=True, quiet_start="", quiet_end=""), [])
        self.assertEqual(errors(enabled=False, pins={"green": 17}), [])  # disabled: not checked
        self.assertTrue(errors(enabled=True, pins={"green": 17}))  # same pin as the buzzer
        self.assertTrue(errors(enabled=True, pins={"red": 1}))  # not a usable BCM pin
        self.assertTrue(errors(enabled=True, quiet_start="25:00"))
        self.assertTrue(errors(enabled=True, quiet_start="22:00", quiet_end=""))
        self.assertTrue(errors(enabled=True, reminder_seconds=0))
        self.assertTrue(errors(enabled="false"))  # a string would be truthy: it must be a real boolean
        self.assertTrue(errors(enabled=True, startup_beep="no"))

    def test_demo_walks_every_state_and_waits_for_the_button(self):
        labels = []

        def sleep(seconds):
            self.t += seconds

        def out(text):
            labels.append(text)
            if text.startswith("GAVE_UP"):
                self.gpio.pressed = True  # the person presses the button
        pressed = lw.run_demo(self.ind, 1.0, lambda: self.t, sleep, out)
        self.assertTrue(pressed)
        self.assertEqual(len(labels), len(lw.DEMO_STEPS) + 2)
        self.assertIn("silenciado", labels[-1])
        self.ind.shutdown()


class IndicatorWithController(Base):
    """The incident replay again, this time watching what a person in the room would see and hear."""

    def test_replay_drives_leds_and_buzzer(self):
        gpio = FakeGpio()
        cfg = make_cfg(indicator={"enabled": True})
        ind = lw.Indicator(cfg, backend_factory=lambda: gpio)
        ctl = self.controller(cfg)
        noon = time.mktime((2026, 10, 18, 12, 0, 0, 0, 0, -1))
        t, beeps, last_buzzer = [1000.0], [0], [False]
        seen = {}

        def cycle(reading):
            ctl.step(reading, self.now)
            ind.update(self.state, False, noon, t[0])
            seen.setdefault(ind.mode, 0)
            seen[ind.mode] += 1
            for _ in range(300):  # the 30 s until the next controller cycle, in 0.1 s ticks
                ind.tick(t[0])
                buzzer = gpio.levels.get(17, False)
                if buzzer and not last_buzzer[0]:
                    beeps[0] += 1
                last_buzzer[0] = buzzer
                t[0] += 0.1
            self.now += POLL

        for _ in range(10):
            cycle(UP(prom={"temp_c": 50.0, "on_battery": False, "uptime_s": 1000.0}))
        self.assertTrue(gpio.levels[27] and not gpio.levels[22])  # all well: steady green
        for _ in range(3 + 9):  # three failed cycles, then the grace period: red blinking, silent
            cycle(DOWN)
        self.assertEqual(beeps[0], 0)
        self.assertEqual(self.state["status"], "DOWN")
        for _ in range(8):  # grace over -> WoL nº 1, boot window
            cycle(DOWN)
        self.assertEqual(len(self.w.calls), 1)
        self.assertEqual(beeps[0], 1)
        self.assertTrue(gpio.levels[22])  # solid red while waiting for the boot
        for _ in range(8):
            cycle(DOWN)
        self.assertEqual(len(self.w.calls), 2)
        self.assertEqual(beeps[0], 2)
        for _ in range(8):  # no plug: it gives up and raises the alarm
            cycle(DOWN)
        self.assertEqual(self.state["recovery"]["phase"], "GAVE_UP")
        self.assertEqual(beeps[0], 2 + 3)
        cycle(UP())  # the lab comes back
        self.assertEqual(beeps[0], 2 + 3 + 2)
        self.assertTrue(gpio.levels[27] and not gpio.levels[22])
        self.assertEqual({"UP", "DOWN", "WOL_SENT", "GAVE_UP"} - set(seen), set())


if __name__ == "__main__":
    unittest.main()
