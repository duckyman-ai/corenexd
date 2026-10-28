package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// runDoctor prints everything corenexd can detect on this machine — device
// access, sensor paths and live readings — then exits. Its output is what a
// bug report should attach.
func runDoctor(opts options, devPath string) int {
	fmt.Println("corenexd", version, "doctor")
	fmt.Println()
	problems := 0

	dev := devPath
	if dev == "" {
		if found, err := findCooler(); err == nil {
			dev = found
		}
	}
	switch {
	case dev == "":
		fmt.Printf("device   : NOT FOUND (USB %s:{%s}) — is the cooler connected?\n", usbVID, strings.Join(usbPIDs, ","))
		problems++
	default:
		note := ""
		if f, err := os.OpenFile(dev, os.O_WRONLY, 0); err != nil {
			note = fmt.Sprintf(" — NOT writable (%v); install dist/60-corenexd.rules or run as root", err)
			problems++
		} else {
			f.Close()
		}
		fmt.Printf("device   : %s%s\n", dev, note)
	}

	if p := findCPUTempPath(); p == "" {
		fmt.Println("cpu temp : NOT FOUND (no k10temp/zenpower/coretemp hwmon)")
		problems++
	} else {
		fmt.Printf("cpu temp : %s = %.1f°C\n", p, readMilliCelsius(p))
	}
	fmt.Printf("cpu clock: %d MHz average\n", cpuMHz())

	fmt.Println("fan inputs (all hwmon readings):")
	hwmons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	sort.Strings(hwmons)
	selFan := findFanPath(opts.fanChip, opts.fanInput)
	selPump := findFanPath(opts.fanChip, opts.pumpInput)
	seen := 0
	for _, hwmon := range hwmons {
		chip := readFile(filepath.Join(hwmon, "name"))
		inputs, _ := filepath.Glob(filepath.Join(hwmon, "fan*_input"))
		sort.Strings(inputs)
		for _, input := range inputs {
			tag := ""
			switch input {
			case selFan:
				tag = "  <- radiator (--fan-input)"
			case selPump:
				tag = "  <- pump (--pump-input)"
			}
			fmt.Printf("  %-12s %-13s %5d RPM%s\n", chip, filepath.Base(input), readInt(input), tag)
			seen++
		}
	}
	switch {
	case seen == 0:
		fmt.Println("  (none — load your board's Super I/O driver, e.g. nct6775)")
		problems++
	case selFan == "":
		fmt.Println("  selected radiator input NOT FOUND — set --fan-input to one of the above")
		problems++
	case selPump == "":
		fmt.Println("  selected pump input NOT FOUND — set --pump-input to one of the above")
		problems++
	}

	switch gpu := newGPUSource(parseGPUSelector(opts.gpu)); gpu.kind {
	case "nvidia":
		fmt.Println("gpu      : nvidia")
		ctx, cancel := context.WithTimeout(context.Background(), smiTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, gpu.smi,
			"--query-gpu=index,utilization.gpu,temperature.gpu,name",
			"--format=csv,noheader,nounits").Output()
		if err != nil {
			fmt.Printf("  nvidia-smi failed: %v\n", err)
			problems++
			break
		}
		cards := parseNvidiaSMI(string(out))
		chosen, ok := parseGPUSelector(opts.gpu).pick(cards)
		if !ok {
			fmt.Printf("  --gpu %s NOT FOUND\n", opts.gpu)
			problems++
		}
		for _, c := range cards {
			tag := ""
			if ok && c.index == chosen.index {
				tag = fmt.Sprintf("  <- displayed (--gpu %s)", opts.gpu)
			}
			fmt.Printf("  [%d] %3d%% %3d°C  %s%s\n", c.index, c.util, c.temp, c.name, tag)
		}
	case "amd":
		fmt.Printf("gpu      : amd (temp=%s busy=%s)\n", gpu.amdTemp, gpu.amdBusy)
	default:
		fmt.Println("gpu      : none detected")
	}

	fmt.Println()
	if problems == 0 {
		fmt.Println("everything detected — looks good")
		return 0
	}
	fmt.Printf("%d problem(s) found — see the hints above each NOT FOUND line\n", problems)
	return 1
}
