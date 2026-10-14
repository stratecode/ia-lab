# UPS operation

The lab UPS is identified over USB as a CyberPower VP700ELCD (`0764:0501`,
USB HID Power Device Class). Use Network UPS Tools (NUT) and its `usbhid-ups`
driver; do not use the serial `powerpanel` driver for this USB connection.

Install the packaged driver and discovery utilities with:

```bash
ansible-playbook playbooks/install-nut-ups-support.yml
```

This playbook installs `nut-server` and `nut-client` only. It does not configure
or enable NUT monitoring, battery tests, power-off commands, or automatic host
shutdown. The initial USB discovery can be run on the lab with:

```bash
sudo nut-scanner -U
```

On 2026-10-14, the lab's Ubuntu 26.04 host installed NUT `2.8.4+really-2` and
`nut-scanner -U` detected `CPS VP700ELCD` (`0764:0501`) with `usbhid-ups`.
The NUT daemons and driver enumerators remain stopped and disabled, so this
keeps automatic shutdown disabled. A one-shot, read-only driver query succeeded
as the restricted `nut` account after udev applied the package's matching USB
permission rule. It reported `ups.status: OL`, battery charge `100%`, load
`4%`, and an estimated runtime of `7053` seconds. Runtime is the UPS's estimate,
not a guaranteed outage duration. No battery test was run.
The scanner also warned that optional SNMP, XML and IPMI libraries are absent;
those are unrelated to this USB-HID connection.

Before enabling monitoring or shutdown, configure and verify NUT's UPS entry,
monitor account, shutdown thresholds, recovery behavior, and notification path.
CyberPower devices can interpret shutdown delays differently, so retain a safe
`offdelay` value and do not test power-loss behavior until the shutdown policy
has been reviewed.
