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
import time
import unittest

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


if __name__ == "__main__":
    unittest.main()
