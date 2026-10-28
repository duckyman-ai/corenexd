# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`corenexd` is a Linux daemon that feeds live telemetry (CPU load, clock, CPU/GPU temperatures, fan RPM) to the segment LCD of Cooler Master MasterLiquid Core Nex Digital AIO coolers (USB HID `2516:0234` white, `2516:0232` black) by writing 64-byte HID input reports with report id `0x10`. Pure Go standard library — there are intentionally **no external dependencies**; adding one needs a strong reason.

## Commands

```sh
go build                      # build locally
go vet ./... && go test ./... # lint + tests
go test -run TestBuildFrameMatchesReference ./...   # single test

# release binary (cross-compile from any OS)
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=1.0.0" -o dist/corenexd-linux-amd64 .

# on the target Linux machine: install everything in one command
sudo ./dist/install.sh              # binary + udev rule + systemd service, then starts it
sudo ./dist/install.sh uninstall    # undo everything the installer added
```

Dev loop against real hardware: deploy the fresh binary to the target machine and restart the service (adjust `user@host` for your rig):

```sh
scp dist/corenexd-linux-amd64 user@host:/tmp/corenexd
ssh user@host "sudo install -m 755 /tmp/corenexd /usr/local/bin/corenexd && sudo systemctl restart corenexd"
ssh user@host "journalctl -u corenexd -n 20 --no-pager"   # logs; add --verbose to ExecStart for frame dumps
```

A one-shot `sudo corenexd --once --verbose` on the machine prints the exact frame being written — use it to verify changes against the display.

## Architecture

Five files, strictly one-directional data flow:

```
main.go     CLI flags, feeder (sample→policy→EMA), ticker loop, alert animation
  └─ sensors.go   reads /proc/stat, cpufreq sysfs, hwmon, nvidia-smi (pure reads)
       └─ frame.go     packs a `sample` into the 64-byte report — the ONLY file that knows the wire format
            └─ device.go   hidraw discovery by walking USB sysfs + report write
doctor.go   --doctor: prints everything detectable (device access, sensor paths, live readings)
```

Invariants worth knowing before editing:

- **The wire format lives only in `frame.go`**, documented byte-by-byte in its comment. `frame_test.go` pins it with `referenceFrame`, a byte-exact capture from a working display; any change to packing must either keep that test green or consciously update the vector.
- **Clamping happens inside `buildFrame`** (percent 0–100, MHz 0–9999, ring 0–20 segments, …). Callers pass raw values; do not pre-clamp at call sites.
- **Sensor discovery is by hwmon chip *name*, never `hwmonN` numbers** — indices reorder between boots. This applies to CPU temp (`k10temp`/`zenpower`/`coretemp`), fans and the AMD GPU.
- **`writeReport` re-opens the device on every refresh** so a replugged cooler is picked up without restarting; on write failure `main.go` calls `rediscover`.
- **The GPU source is chosen once at startup** (`newGPUSource`: AMD sysfs preferred, `nvidia-smi` fallback). Every `nvidia-smi` call runs under a 2 s timeout (`smiTimeout`) so a wedged driver stalls one refresh, not the daemon. Output can be multi-line (one line per GPU); `parseNvidiaSMI` accepts an optional fourth name field (--doctor uses it) and `gpuSelector.pick` selects — default `--gpu max` shows the hottest card, `--gpu N` pins an index (missing index falls back to max).
- **Alert animation** (`animateAlert`) takes over the ring while the page temperature (always compared in °C) stays ≥ `--alert-temp`; it re-samples sensors once per second but animates at full speed, overriding only `sample.ringPct`. The default effect is blink; scan fills the ring upward and restarts at full (never walks back down — tuned on real hardware; the LCD can't visually follow very fast toggles, so blink defaults to 400 ms). The sequence generator `alertRing` is unit-tested in `sensors_test.go`.
- Fan inputs are motherboard-specific (defaults `fan2_input`/`fan4_input`); identify which header is which with `--doctor`. If the board's Super I/O driver is not auto-loaded (common for Nuvoton chips), load it explicitly, e.g. `echo nct6775 | sudo tee /etc/modules-load.d/nct6775.conf`.

Not implemented (and out of scope unless asked): ARGB control, images/GIFs on the display, fan/pump curves.
