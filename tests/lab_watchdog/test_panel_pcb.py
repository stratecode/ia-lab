"""Keeps docs/hardware (netlist, bill of materials) consistent with each other and with the pins the software uses.

Run:  python3 -m unittest discover -s tests/lab_watchdog
"""
import csv
import importlib.util
import os
import re
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.join(HERE, "..", "..")
HW = os.path.join(ROOT, "docs", "hardware")
spec = importlib.util.spec_from_file_location("lab_watchdog_pcb", os.path.join(ROOT, "roles", "lab_watchdog", "files", "lab-watchdog.py"))
lw = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = lw
spec.loader.exec_module(lw)

# Raspberry Pi 40-pin header: physical pin -> function (BCM number for GPIO pins).
HEADER = {1: "+3V3", 17: "+3V3", 2: "+5V", 4: "+5V",
          3: 2, 5: 3, 7: 4, 8: 14, 10: 15, 11: 17, 12: 18, 13: 27, 15: 22, 16: 23, 18: 24, 19: 10, 21: 9, 22: 25,
          23: 11, 24: 8, 26: 7, 29: 5, 31: 6, 32: 12, 33: 13, 35: 19, 36: 16, 37: 26, 38: 20, 40: 21,
          6: "GND", 9: "GND", 14: "GND", 20: "GND", 25: "GND", 30: "GND", 34: "GND", 39: "GND"}
EXPECTED_PINS = {"R1": "12", "R2": "12", "R3": "12", "R4": "12", "R5": "12", "R6": "12", "R7": "12", "C1": "12", "SW1": "12", "SW2": "12",
                 "D1": "AK", "D2": "AK", "D3": "AK", "D4": "AK", "Q1": "BCE", "LS1": "+-", "J2": "123"}


def read_netlist():
    with open(os.path.join(HW, "panel-pcb-netlist.csv"), newline="", encoding="utf-8") as handle:
        return {row["Net"]: row["Conexiones"].split() for row in csv.DictReader(handle)}


def read_bom_refs():
    with open(os.path.join(HW, "panel-pcb-bom.csv"), newline="", encoding="utf-8") as handle:
        return [ref for row in csv.DictReader(handle) for ref in row["Referencia"].split()]


class PanelPcbTests(unittest.TestCase):
    def setUp(self):
        self.nets = read_netlist()
        self.pins = {}  # "R1.1" -> net
        for net, members in self.nets.items():
            for member in members:
                self.assertNotIn(member, self.pins, "%s is in two nets: %s and %s" % (member, self.pins.get(member), net))
                self.pins[member] = net

    def test_every_header_pin_is_a_real_pin_used_at_most_once_and_on_the_right_net(self):
        for member, net in self.pins.items():
            ref, _, pin = member.partition(".")
            if ref != "J1":
                continue
            function = HEADER[int(pin)]
            if isinstance(function, int):
                self.assertTrue(net.startswith("GPIO%d" % function), "J1.%s is GPIO%d but sits on net %s" % (pin, function, net))
            else:
                self.assertEqual(net, function, "J1.%s is %s but sits on net %s" % (pin, function, net))

    def test_all_the_ground_and_power_header_pins_are_connected(self):
        for pin, function in HEADER.items():
            if function in ("GND", "+5V", "+3V3"):
                self.assertIn("J1.%d" % pin, self.pins, "J1.%d (%s) is left floating" % (pin, function))

    def test_the_software_pins_match_the_board(self):
        board = {name: int(re.match(r"GPIO(\d+)", net).group(1)) for name, net in (
            ("buzzer", self.pins["R5.1"]), ("green", self.pins["R1.1"]), ("red", self.pins["R2.1"]),
            ("button", self.pins["SW1.1"]), ("ready", self.pins["R3.1"]))}
        software = dict(lw.DEFAULTS["indicator"]["pins"], ready=24)  # "ready" is optional (None) in the defaults; the role sets 24
        for name, bcm in board.items():
            self.assertEqual(bcm, software[name], "%s: the board uses GPIO%d, the software GPIO%s" % (name, bcm, software[name]))

    def test_every_component_pin_is_connected_and_every_reference_is_in_the_bill_of_materials(self):
        refs = read_bom_refs()
        for ref, pins in EXPECTED_PINS.items():
            self.assertIn(ref, refs, "%s is in the netlist but not in the BOM" % ref)
            for pin in pins:
                self.assertIn("%s.%s" % (ref, pin), self.pins, "%s.%s is not connected" % (ref, pin))
        used = {member.split(".")[0] for member in self.pins}
        for ref in used:
            self.assertIn(ref, refs, "%s is in the netlist but not in the BOM" % ref)
        for ref in refs:
            self.assertIn(ref, used, "%s is in the BOM but not in the netlist" % ref)

    def test_the_buzzer_circuit_is_a_proper_low_side_switch(self):
        self.assertEqual(self.pins["Q1.E"], "GND")
        self.assertEqual(self.pins["LS1.+"], "+5V")
        self.assertEqual(self.pins["LS1.-"], self.pins["Q1.C"])
        self.assertEqual(self.pins["R5.2"], self.pins["Q1.B"])
        self.assertEqual(self.pins["R6.1"], self.pins["Q1.B"])  # pull-down on the base
        self.assertEqual(self.pins["R6.2"], "GND")

    def test_each_led_has_its_resistor_and_the_cathode_on_ground(self):
        for led, resistor in (("D1", "R1"), ("D2", "R2"), ("D3", "R3"), ("D4", "R4")):
            self.assertEqual(self.pins[led + ".K"], "GND")
            self.assertEqual(self.pins[resistor + ".2"], self.pins[led + ".A"])
        self.assertEqual(self.pins["R4.1"], "+3V3")  # the power witness hangs from 3V3, not from a GPIO

    def test_the_ones_wire_pull_up_and_the_buttons(self):
        self.assertEqual(self.pins["R7.1"], "+3V3")
        self.assertEqual(self.pins["R7.2"], "GPIO4")
        self.assertEqual(self.pins["J2.2"], "GPIO4")
        for button in ("SW1", "SW2"):
            self.assertEqual(self.pins[button + ".2"], "GND")

    def test_nothing_that_the_pi_drives_needs_more_current_than_a_gpio_can_give(self):
        # 3.3 V through the resistors, forward voltage assumed 2.0 V (green/red) and 3.0 V (blue): well under the 16 mA/pin limit.
        for resistor, ohms, vf in (("R1", 330, 2.0), ("R2", 330, 2.0), ("R3", 100, 3.0), ("R4", 100, 3.0)):
            self.assertLess((3.3 - vf) / ohms * 1000, 16.0, resistor)
        self.assertLess((3.3 - 0.7) / 1000 * 1000, 16.0)  # base current through R5

    def test_the_schematic_svg_is_well_formed_and_mentions_every_reference(self):
        import xml.dom.minidom
        path = os.path.join(HW, "panel-pcb-esquema.svg")
        xml.dom.minidom.parse(path)
        with open(path, encoding="utf-8") as handle:
            svg = handle.read()
        for ref in ("R1", "R2", "R3", "R4", "R5", "R6", "R7", "C1", "D1", "D2", "D3", "D4", "Q1", "LS1", "SW1", "SW2", "J2", "J3"):
            self.assertIn(ref, svg, "%s is missing from the schematic" % ref)


if __name__ == "__main__":
    unittest.main()
