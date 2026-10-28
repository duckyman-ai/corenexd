package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// --- small sysfs helpers ------------------------------------------------------

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readInt(path string) int {
	if path == "" {
		return 0
	}
	n, err := strconv.Atoi(readFile(path))
	if err != nil {
		return 0
	}
	return n
}

func readMilliCelsius(path string) float64 {
	if path == "" {
		return 0
	}
	return float64(readInt(path)) / 1000
}

func hwmonsNamed(chip string) []string {
	matches, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	sort.Strings(matches)
	var found []string
	for _, hwmon := range matches {
		if readFile(filepath.Join(hwmon, "name")) == chip {
			found = append(found, hwmon)
		}
	}
	return found
}

// smooth is an exponential moving average; alpha 1 disables smoothing.
func smooth(prev, value float64, have bool, alpha float64) float64 {
	if !have || alpha >= 1 {
		return value
	}
	return prev + (value-prev)*alpha
}

// --- CPU -----------------------------------------------------------------------

var cpuTempChips = []string{"k10temp", "zenpower", "coretemp"}

// findCPUTempPath prefers a labelled Tctl/Tdie/Package input, then falls back
// to temp1_input, across the common AMD and Intel hwmon chips.
func findCPUTempPath() string {
	for _, chip := range cpuTempChips {
		for _, hwmon := range hwmonsNamed(chip) {
			labels, _ := filepath.Glob(filepath.Join(hwmon, "temp*_label"))
			sort.Strings(labels)
			for _, label := range labels {
				name := readFile(label)
				if name == "Tctl" || name == "Tdie" || strings.HasPrefix(name, "Package") {
					input := strings.TrimSuffix(label, "_label") + "_input"
					if fileExists(input) {
						return input
					}
				}
			}
			if input := filepath.Join(hwmon, "temp1_input"); fileExists(input) {
				return input
			}
		}
	}
	return ""
}

type cpuTimes struct{ idle, total uint64 }

func cpuSnapshot() cpuTimes {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuTimes{}
	}
	var t cpuTimes
	for i, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			continue
		}
		t.total += v
		if i == 3 || i == 4 { // idle + iowait
			t.idle += v
		}
	}
	return t
}

func cpuUsage(prev, now cpuTimes) float64 {
	idle := int64(now.idle) - int64(prev.idle)
	total := int64(now.total) - int64(prev.total)
	if total <= 0 {
		return 0
	}
	usage := (1 - float64(idle)/float64(total)) * 100
	if usage < 0 {
		return 0
	}
	if usage > 100 {
		return 100
	}
	return usage
}

// cpuMHz averages per-core scaling_cur_freq, falling back to /proc/cpuinfo.
func cpuMHz() int {
	freqs, _ := filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_cur_freq")
	if len(freqs) > 0 {
		sum, n := 0, 0
		for _, path := range freqs {
			if v := readInt(path); v > 0 {
				sum += v
				n++
			}
		}
		if n > 0 {
			return sum / n / 1000
		}
	}
	// Fallback: average "cpu MHz" lines from /proc/cpuinfo.
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sum, n := 0.0, 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), ":")
		if found && strings.TrimSpace(key) == "cpu MHz" {
			if v, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
				sum += v
				n++
			}
		}
	}
	if n == 0 {
		return 0
	}
	return int(sum / float64(n))
}

// --- GPU ------------------------------------------------------------------------

type gpuSource struct {
	kind    string // "amd", "nvidia" or "" when no GPU source was found
	amdTemp string
	amdBusy string
	smi     string
}

// gpuCard is one GPU as reported by nvidia-smi.
type gpuCard struct {
	index, util, temp int
	name              string // optional, present in --doctor output only
}

// gpuSelector picks which card to display when several are installed.
type gpuSelector struct {
	mode  string // "max" (hottest) or "index"
	index int
}

func parseGPUSelector(spec string) gpuSelector {
	if n, err := strconv.Atoi(spec); err == nil && n >= 0 {
		return gpuSelector{mode: "index", index: n}
	}
	return gpuSelector{mode: "max"}
}

// pick returns the card for the selector: an explicit index when present,
// otherwise the hottest card (ties broken by utilization). An index that no
// longer exists (card removed) is not ok.
func (s gpuSelector) pick(cards []gpuCard) (gpuCard, bool) {
	if len(cards) == 0 {
		return gpuCard{}, false
	}
	if s.mode == "index" {
		for _, c := range cards {
			if c.index == s.index {
				return c, true
			}
		}
		return gpuCard{}, false // pinned card missing: report failure, not a different GPU
	}
	best := cards[0]
	for _, c := range cards[1:] {
		if c.temp > best.temp || (c.temp == best.temp && c.util > best.util) {
			best = c
		}
	}
	return best, true
}

// smiTimeout bounds each nvidia-smi call so a wedged driver stalls one
// refresh instead of the whole daemon.
const smiTimeout = 2 * time.Second

// parseNvidiaSMI parses the (possibly multi-line) output of
// `nvidia-smi --query-gpu=index,utilization.gpu,temperature.gpu[,name]
// --format=csv,noheader,nounits` — one line per installed GPU.
func parseNvidiaSMI(out string) []gpuCard {
	var cards []gpuCard
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimSpace(line), ",")
		if len(fields) < 3 {
			continue
		}
		idx, err1 := strconv.Atoi(strings.TrimSpace(fields[0]))
		util, err2 := strconv.Atoi(strings.TrimSpace(fields[1]))
		temp, err3 := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		card := gpuCard{index: idx, util: util, temp: temp}
		if len(fields) > 3 {
			card.name = strings.TrimSpace(fields[3])
		}
		cards = append(cards, card)
	}
	return cards
}

func (g gpuSource) String() string {
	if g.kind == "" {
		return "(none)"
	}
	return g.kind
}

func (g gpuSource) present() bool { return g.kind != "" }

// stats returns (utilization percent, temperature °C) for the selected GPU;
// zeros when unknown.
func (g gpuSource) stats(sel gpuSelector) (int, float64) {
	switch g.kind {
	case "amd":
		temp := readMilliCelsius(g.amdTemp)
		return readInt(g.amdBusy), temp
	case "nvidia":
		ctx, cancel := context.WithTimeout(context.Background(), smiTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, g.smi,
			"--query-gpu=index,utilization.gpu,temperature.gpu",
			"--format=csv,noheader,nounits").Output()
		if err != nil {
			return 0, 0
		}
		if card, ok := sel.pick(parseNvidiaSMI(string(out))); ok {
			return card.util, float64(card.temp)
		}
	}
	return 0, 0
}

// newGPUSource prefers AMD sysfs (free to poll) and falls back to nvidia-smi.
// A pinned index (--gpu N) is an nvidia-smi index, so it goes straight to
// nvidia-smi — otherwise an AMD iGPU would silently win over the card asked for.
func newGPUSource(sel gpuSelector) gpuSource {
	if sel.mode == "index" {
		if smi, err := exec.LookPath("nvidia-smi"); err == nil {
			return gpuSource{kind: "nvidia", smi: smi}
		}
		return gpuSource{}
	}
	for _, hwmon := range hwmonsNamed("amdgpu") {
		labels, _ := filepath.Glob(filepath.Join(hwmon, "temp*_label"))
		sort.Strings(labels)
		temp := ""
		for _, label := range labels {
			if readFile(label) == "edge" {
				temp = strings.TrimSuffix(label, "_label") + "_input"
				break
			}
		}
		if temp == "" {
			if input := filepath.Join(hwmon, "temp1_input"); fileExists(input) {
				temp = input
			}
		}
		busy, _ := filepath.Glob("/sys/class/drm/card*/device/gpu_busy_percent")
		if temp != "" && len(busy) > 0 {
			sort.Strings(busy)
			return gpuSource{kind: "amd", amdTemp: temp, amdBusy: busy[0]}
		}
	}
	if smi, err := exec.LookPath("nvidia-smi"); err == nil {
		return gpuSource{kind: "nvidia", smi: smi}
	}
	return gpuSource{}
}

// --- fans ------------------------------------------------------------------------

// findFanPath resolves a fan input across hwmon chips. With chip empty it
// scans every hwmon and prefers one that currently reports a non-zero RPM,
// so silent headers are skipped.
func findFanPath(chip, input string) string {
	var hwmons []string
	if chip != "" {
		hwmons = hwmonsNamed(chip)
	} else {
		hwmons, _ = filepath.Glob("/sys/class/hwmon/hwmon*")
		sort.Strings(hwmons)
	}
	fallback := ""
	for _, hwmon := range hwmons {
		path := filepath.Join(hwmon, input)
		if !fileExists(path) {
			continue
		}
		if readInt(path) > 0 {
			return path
		}
		if fallback == "" {
			fallback = path
		}
	}
	return fallback
}
