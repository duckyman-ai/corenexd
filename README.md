# corenexd

**Live system stats on the Cooler Master MasterLiquid Core Nex Digital LCD — on Linux, no vendor software required.**

`corenexd` is a small Linux daemon that drives the segment LCD on the pump
head of Cooler Master MasterLiquid Core Nex Digital AIO coolers. It renders
live system telemetry — CPU load, CPU frequency, CPU/GPU temperatures and
fan RPM — directly over the cooler's USB interface.

The official MasterCTRL software is Windows-only, which leaves the pump-head
display permanently blank on Linux. corenexd fills that gap: one static
binary, no runtime dependencies, everything auto-detected.

## Features

- Single static binary, pure Go standard library, zero runtime dependencies
- Auto-detects the cooler, CPU temperature, GPU (AMD **and** NVIDIA) and fan
  headers
- Runs as a systemd service or as your own user via the bundled udev rule
  (no root)
- Alternating CPU/GPU temperature pages, CPU load ring, Celsius or Fahrenheit
- Optional high-temperature alert animations (blink, sweep, heartbeat)
- `--doctor` mode that prints exactly what it detects on your machine —
  attach its output to bug reports

## Supported hardware

| Device | USB ID | Status |
|---|---|---|
| MasterLiquid Core Nex Digital (MLQ-Core-Nex-Digital-WH) | `2516:0234` | tested, daily driven |
| Other Core Nex Digital variants (240/360, black) | likely `2516:*` | protocol probably shared — reports welcome |

Not implemented (and out of scope): ARGB/lighting control, images or GIFs on
the display, fan/pump curves. corenexd only drives the telemetry display.

## Requirements

- A Linux system with `hidraw` support (any mainstream distribution)
- The cooler plugged into an internal USB header — verify with
  `lsusb | grep 2516:0234`
- Optional: `nvidia-smi` on the `PATH` for NVIDIA GPU readings (it is
  installed together with the NVIDIA driver)
- To build from source: Go 1.25 or newer

## Install

### One command (recommended)

From a fresh clone:

```sh
git clone https://github.com/duckyman-ai/corenexd && cd corenexd
sudo ./dist/install.sh
```

The installer copies the binary to `/usr/local/bin`, installs the udev rule
and the systemd service, and starts it — the display comes alive on its own.

Or, without cloning:

```sh
curl -fsSL https://raw.githubusercontent.com/duckyman-ai/corenexd/main/dist/install.sh | sudo sh
```

To undo everything:

```sh
sudo ./dist/install.sh uninstall
```

### Manual install

Use the prebuilt binary (Linux amd64, shipped in [dist/](dist)):

```sh
sudo install -m 755 dist/corenexd-linux-amd64 /usr/local/bin/corenexd
```

or build from source — pure standard library, nothing to download:

```sh
go build
sudo install -m 755 corenexd /usr/local/bin/corenexd
go vet ./... && go test ./...   # optional: lint + tests
```

Cross-compile from any OS:

```sh
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o corenexd .
```

## Quick start

```sh
# one-command install (Linux): binary from GitHub Releases + systemd service
curl -fsSL https://raw.githubusercontent.com/duckyman-ai/corenexd/main/dist/install.sh | sudo sh

# see what corenexd detects on your machine (attach to bug reports)
corenexd --doctor

# render a single frame (needs write access to the hidraw node — see below)
corenexd --once --verbose

# run continuously
corenexd
```

The pump-head display should come alive immediately. If the systemd service
is already running, stop it before driving the display manually
(`sudo systemctl stop corenexd`) — otherwise the two writers make the
display flicker.

### Manual setup: start at boot (systemd service)

```sh
sudo cp dist/corenexd.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now corenexd
```

Customise by editing the `ExecStart` line in the unit, for example
`ExecStart=/usr/local/bin/corenexd --ring gpu --temp-unit f`.

### Manual setup: run without root

Install the bundled udev rule so your desktop user can write the display:

```sh
sudo cp dist/60-corenexd.rules /etc/udev/rules.d/
sudo udevadm control --reload && sudo udevadm trigger
corenexd
```

## What the display shows

| Element | Source (default) |
|---|---|
| Big percentage | CPU usage |
| Frequency | average CPU clock, MHz |
| Temperature | CPU ↔ GPU page, alternating every 15 s |
| Mini bar | current page temperature vs. its max |
| RPM | radiator fan header |
| Outer ring | CPU load (see `--ring`) |

## Options

```
--dev PATH          hidraw node (default: auto-detect 2516:0234)
--interval D        refresh interval (default 5s)
--ring SRC          outer ring source: cpu, gpu, rpm, pump
--temp-mode MODE    centre page: cpu, gpu, cycle (default cycle)
--temp-switch D     seconds per CPU/GPU page in cycle mode (default 15s)
--temp-unit UNIT    c or f (default c)
--gpu WHICH         GPU to display when several are installed:
                    max (hottest, default) or a 0-based nvidia-smi index
--alert-temp C      page temperature that triggers the alert animation
                    (default 85, 0 disables)
--alert-effect F/X  blink, scan or pulse (default blink):
                    blink = full on/off · scan = fills up and restarts ·
                    pulse = two quick flashes then a rest (heartbeat)
--alert-blink D     blink half-period (default 400ms — about as fast as
                    the LCD can follow)
--alert-step D      scan step interval (default 60ms)
--doctor            print detected sensors and exit
--fan-chip NAME     hwmon chip for RPM (default: auto-detect)
--fan-input NAME    radiator fan input (default fan2_input)
--pump-input NAME   pump input (default fan4_input)
--fan-max-rpm N     RPM that fills the ring with --ring rpm (default 2000)
--pump-max-rpm N    RPM that fills the ring with --ring pump (default 3200)
--cpu-max-temp C    CPU °C that fills the mini bar (default 95)
--gpu-max-temp C    GPU °C that fills the mini bar (default 110)
--smooth A          EMA alpha for percentages, 1 disables (default 0.25)
--once              render one frame and exit
--verbose           log every frame
--version           print version
```

## Examples

```sh
# CPU-only rig, everything on defaults
corenexd

# GPU rig: GPU temperature page and GPU load ring
corenexd --temp-mode gpu --ring gpu

# several NVIDIA cards — pin the second one
corenexd --gpu 1 --ring gpu

# Fahrenheit, faster page cycling
corenexd --temp-unit f --temp-switch 8s

# treat the ring as a warning light: filling sweep or heartbeat above 80 °C
corenexd --alert-temp 80 --alert-effect scan
corenexd --alert-temp 80 --alert-effect pulse

# 1 s refresh, no smoothing (raw values)
corenexd --interval 1s --smooth 1
```

## Troubleshooting

**No hidraw node for 2516:0234** — the cooler is not visible over USB. Check
`lsusb` for `2516:0234` and confirm the AIO's USB cable is seated on an
internal header.

**Permission denied writing the device** — `corenexd --doctor` will flag it.
Install the udev rule (see *Run without root*) or run as root.

**RPM shows 0** — fan inputs are motherboard-specific. Run `corenexd --doctor`,
pick the right header from the list it prints and point `--fan-input` /
`--pump-input` at it, e.g. `--fan-input fan1_input`. If no fan inputs are
listed at all, your board's Super I/O driver is not loaded — for many boards
`nct6775` is the one (add it to `/etc/modules-load.d/`).

**GPU temperature never appears** — AMD cards are read straight from sysfs;
NVIDIA cards need `nvidia-smi` on the `PATH`. With several NVIDIA cards the
hottest one is shown by default; pin a specific card with `--gpu 1`.

## How it works

The cooler enumerates as a USB HID device. corenexd writes a 64-byte input
report (report id `0x10`) whose 13-byte payload carries the displayed values;
the byte-by-byte layout is documented in [`frame.go`](frame.go) and pinned by
a byte-exact regression test. Sensors come from standard kernel interfaces:
`/proc/stat` for CPU load, cpufreq sysfs for clocks, hwmon for temperatures
and fan RPM. The hidraw node is re-opened on every refresh, so a replugged
cooler is picked up without restarting the daemon.

## License

MIT — see [LICENSE](LICENSE).
