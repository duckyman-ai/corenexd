// corenexd feeds live system telemetry to the segment LCD of Cooler Master
// MasterLiquid Core Nex Digital AIO coolers over the device's USB HID
// interface. It runs as a daemon (or with --once for a single frame) and
// needs no vendor software.
//
// Tested device: MLQ-Core-Nex-Digital-WH (USB HID 2516:0234).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var version = "dev"

const (
	defaultInterval   = 5 * time.Second
	defaultPageSwing  = 15 * time.Second
	defaultAlertTemp  = 85.0
	defaultBlinkHalf  = 400 * time.Millisecond
	defaultScanStep   = 60 * time.Millisecond
	alertResampleRate = time.Second
)

// options is the resolved CLI configuration.
type options struct {
	interval   time.Duration
	ring       string
	tempMode   string
	tempUnit   string
	pageSwing  time.Duration
	fanChip    string
	fanInput   string
	pumpInput  string
	gpu        string
	maxFanRPM  int
	maxPumpRPM int
	maxCPUTemp float64
	maxGPUTemp float64
	smoothing  float64
	alertTemp  float64
	effect     string // blink, scan or pulse
	alertBlink time.Duration
	alertStep  time.Duration
	verbose    bool
}

// reading is one complete sensor pass: the displayable sample plus the
// context the policy chose it with.
type reading struct {
	sample
	page    string
	pageC   float64 // active page temperature in °C, whatever the display unit
	gpuUtil int
	gpuC    float64
	pumpRPM int
}

// feeder ties sensor discovery to the display policy and carries the
// smoothing state between refreshes.
type feeder struct {
	opts     options
	cpuTemp  string
	fan      string
	pump     string
	gpu      gpuSource
	gpuSel   gpuSelector
	prev     cpuTimes
	haveCPU  bool
	haveRing bool
	emaCPU   float64
	emaRing  float64
	start    time.Time
}

func newFeeder(opts options) *feeder {
	return &feeder{
		opts:    opts,
		cpuTemp: findCPUTempPath(),
		fan:     findFanPath(opts.fanChip, opts.fanInput),
		pump:    findFanPath(opts.fanChip, opts.pumpInput),
		gpu:     newGPUSource(),
		gpuSel:  parseGPUSelector(opts.gpu),
		start:   time.Now(),
	}
}

// sample reads every sensor and applies the page/ring selection policy.
func (f *feeder) sample() reading {
	now := cpuSnapshot()
	pct := cpuUsage(f.prev, now)
	f.prev = now
	f.emaCPU = smooth(f.emaCPU, pct, f.haveCPU, f.opts.smoothing)
	f.haveCPU = true

	gpuUtil, gpuTempC := f.gpu.stats(f.gpuSel)
	cpuTempC := readMilliCelsius(f.cpuTemp)
	fanRPM := readInt(f.fan)
	pumpRPM := readInt(f.pump)

	page := "cpu"
	if want := f.page(); want == "gpu" && f.gpu.present() {
		page = "gpu"
	}
	pageC := cpuTempC
	if page == "gpu" && gpuTempC > 0 {
		pageC = gpuTempC
	}
	shown := pageC
	if f.opts.tempUnit == "f" {
		shown = pageC*9/5 + 32
	}
	scale := f.opts.maxCPUTemp
	if page == "gpu" {
		scale = f.opts.maxGPUTemp
	}

	ring := f.emaCPU
	switch f.opts.ring {
	case "gpu":
		ring = float64(gpuUtil)
	case "rpm":
		if fanRPM > 0 {
			ring = float64(fanRPM) / float64(f.opts.maxFanRPM) * 100
		}
	case "pump":
		if pumpRPM > 0 {
			ring = float64(pumpRPM) / float64(f.opts.maxPumpRPM) * 100
		}
	}
	f.emaRing = smooth(f.emaRing, ring, f.haveRing, f.opts.smoothing)
	f.haveRing = true

	return reading{
		sample: sample{
			cpuPct:     f.emaCPU,
			cpuMHz:     cpuMHz(),
			gpuIcon:    page == "gpu",
			tempShown:  shown,
			fahrenheit: f.opts.tempUnit == "f",
			miniBar:    pageC / scale * 10,
			rpm:        fanRPM,
			ringPct:    f.emaRing,
		},
		page:    page,
		pageC:   pageC,
		gpuUtil: gpuUtil,
		gpuC:    gpuTempC,
		pumpRPM: pumpRPM,
	}
}

// page returns the centre page wanted right now ("cpu" or "gpu"); callers
// confirm a GPU source actually exists before trusting "gpu".
func (f *feeder) page() string {
	if f.opts.tempMode == "gpu" {
		return "gpu"
	}
	if f.opts.tempMode == "cycle" &&
		int(time.Since(f.start)/max(f.opts.pageSwing, time.Second))%2 == 1 {
		return "gpu"
	}
	return "cpu"
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("corenexd: ")

	var (
		devPath    = flag.String("dev", "", "hidraw node to use (default: auto-detect 2516:0234)")
		interval   = flag.Duration("interval", defaultInterval, "refresh interval, e.g. 2s")
		ring       = flag.String("ring", "cpu", "outer ring source: cpu, gpu, rpm, pump")
		tempMode   = flag.String("temp-mode", "cycle", "centre temperature page: cpu, gpu, cycle")
		tempUnit   = flag.String("temp-unit", "c", "displayed temperature unit: c, f")
		pageSwing  = flag.Duration("temp-switch", defaultPageSwing, "seconds per CPU/GPU page in cycle mode, e.g. 8s")
		fanChip    = flag.String("fan-chip", "", "hwmon chip for RPM readings (default: auto-detect)")
		fanInput   = flag.String("fan-input", "fan2_input", "hwmon input for radiator fan RPM")
		pumpInput  = flag.String("pump-input", "fan4_input", "hwmon input for pump RPM")
		gpuSel     = flag.String("gpu", "max", "GPU to display when several are installed: max (hottest) or a 0-based nvidia-smi index")
		maxFanRPM  = flag.Int("fan-max-rpm", 2000, "RPM that fills the ring with --ring rpm")
		maxPumpRPM = flag.Int("pump-max-rpm", 3200, "RPM that fills the ring with --ring pump")
		maxCPUTemp = flag.Float64("cpu-max-temp", 95, "CPU °C that fills the mini bar")
		maxGPUTemp = flag.Float64("gpu-max-temp", 110, "GPU °C that fills the mini bar")
		smoothing  = flag.Float64("smooth", 0.25, "EMA alpha for percentages (1 disables)")
		alertTemp  = flag.Float64("alert-temp", defaultAlertTemp, "page temperature in °C that triggers the alert effect (checked in Celsius; 0 disables)")
		alertEff   = flag.String("alert-effect", "blink", "alert ring animation: blink, scan or pulse")
		alertBlink = flag.Duration("alert-blink", defaultBlinkHalf, "blink half-period, e.g. 400ms — slow enough for the LCD to follow")
		alertStep  = flag.Duration("alert-step", defaultScanStep, "scan step interval, e.g. 60ms")
		once       = flag.Bool("once", false, "render a single frame and exit")
		doctor     = flag.Bool("doctor", false, "print detected sensors and exit")
		verbose    = flag.Bool("verbose", false, "log every frame")
		showVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("corenexd", version)
		return
	}

	opts := options{
		interval:   *interval,
		ring:       *ring,
		tempMode:   *tempMode,
		tempUnit:   *tempUnit,
		pageSwing:  *pageSwing,
		fanChip:    *fanChip,
		fanInput:   *fanInput,
		pumpInput:  *pumpInput,
		gpu:        *gpuSel,
		maxFanRPM:  *maxFanRPM,
		maxPumpRPM: *maxPumpRPM,
		maxCPUTemp: *maxCPUTemp,
		maxGPUTemp: *maxGPUTemp,
		smoothing:  *smoothing,
		alertTemp:  *alertTemp,
		effect:     *alertEff,
		alertBlink: *alertBlink,
		alertStep:  *alertStep,
		verbose:    *verbose,
	}

	if *doctor {
		os.Exit(runDoctor(opts, *devPath))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dev := *devPath
	if dev == "" {
		var err error
		dev, err = findHidraw(usbVID, usbPID)
		if err != nil {
			log.Fatalf("no hidraw node for %s:%s: %v (is the AIO connected?)", usbVID, usbPID, err)
		}
	}

	feed := newFeeder(opts)
	if opts.verbose {
		log.Printf("dev=%s cpu_temp=%s fan=%s pump=%s gpu=%s",
			dev, printable(feed.cpuTemp), printable(feed.fan), printable(feed.pump), feed.gpu)
	}

	// Draw the first frame immediately. CPU usage reads 0% on the very first
	// pass because /proc/stat needs two samples an interval apart.
	if _, err := draw(dev, feed); err != nil {
		log.Printf("device write failed: %v", err)
		dev = rediscover(dev)
	}
	if *once {
		return
	}

	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tempC, err := draw(dev, feed)
			if err != nil {
				log.Printf("device write failed: %v", err)
				dev = rediscover(dev)
				continue
			}
			if opts.alertTemp > 0 && tempC >= opts.alertTemp {
				animateAlert(ctx, dev, feed)
			}
		}
	}
}

// draw samples, writes one frame and returns the active page temperature in
// °C so callers can drive the alert animation.
func draw(dev string, f *feeder) (float64, error) {
	r := f.sample()
	if f.opts.verbose {
		log.Printf("page=%s cpu=%.1f%% %dMHz temp=%.1fC gpu=%d%%/%.0fC rpm=%d pump=%d ring=%.1f%% frame=% x",
			r.page, r.cpuPct, r.cpuMHz, r.pageC, r.gpuUtil, r.gpuC, r.rpm, r.pumpRPM, r.ringPct,
			buildFrame(r.sample))
	}
	return r.pageC, writeReport(dev, buildFrame(r.sample))
}

// animateAlert takes over the outer ring with a blink or scan effect while
// the page temperature stays at or above the alert threshold. Sensors are
// re-sampled once per second; the effect itself runs at animation speed.
func animateAlert(ctx context.Context, dev string, f *feeder) {
	r := f.sample()
	ring := alertRing{
		scan:  f.opts.effect == "scan",
		pulse: f.opts.effect == "pulse",
	}
	step := max(f.opts.alertBlink, 50*time.Millisecond)
	switch f.opts.effect {
	case "scan":
		step = max(f.opts.alertStep, 20*time.Millisecond)
	case "pulse":
		step = max(f.opts.alertBlink/2, 150*time.Millisecond)
	}
	anim := time.NewTicker(step)
	resample := time.NewTicker(alertResampleRate)
	defer anim.Stop()
	defer resample.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-resample.C:
			r = f.sample()
			if f.opts.verbose {
				log.Printf("alert page=%s temp=%.1fC", r.page, r.pageC)
			}
			if r.pageC < f.opts.alertTemp {
				return // cooled down; the main loop resumes normal rendering
			}
		case <-anim.C:
			r.ringPct = ring.next()
			if err := writeReport(dev, buildFrame(r.sample)); err != nil {
				return // the main loop re-reports and re-discovers on its next tick
			}
		}
	}
}

// alertRing produces the ring percentages for the alert effect: blink
// alternates full/off, scan fills the segments from zero and restarts at
// full (never walking backwards), pulse beats like a heart monitor — two
// quick flashes then a rest.
type alertRing struct {
	scan, pulse bool
	seg, phase  int
}

// pulsePattern is one heartbeat: flash, gap, flash, rest.
var pulsePattern = []int{20, 0, 20, 0, 0, 0}

func (a *alertRing) next() float64 {
	switch {
	case a.scan:
		a.seg++
		if a.seg > 20 {
			a.seg = 0
		}
		return float64(a.seg * 5)
	case a.pulse:
		v := pulsePattern[a.phase%len(pulsePattern)]
		a.phase++
		return float64(v * 5)
	case a.seg == 0:
		a.seg = 20
	default:
		a.seg = 0
	}
	return float64(a.seg * 5)
}

// rediscover re-resolves the hidraw node after a write failure (the cooler
// may have been replugged) and returns the node to use.
func rediscover(current string) string {
	if dev, err := findHidraw(usbVID, usbPID); err == nil && dev != current {
		log.Printf("device rediscovered at %s", dev)
		return dev
	}
	return current
}

func printable(path string) string {
	if path == "" {
		return "(none)"
	}
	return path
}
