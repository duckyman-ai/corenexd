package main

import (
	"bytes"
	"testing"
)

// referenceFrame was captured from a working display update on
// MLQ-Core-Nex-Digital-WH: cpu=0.0%, 3361 MHz, CPU page, 35 °C, mini bar
// 4/10, rpm=0, ring=0%. The Go implementation must reproduce it byte for byte.
var referenceFrame = append(
	[]byte{0x10, 0x01, 0x00, 0x0d, 0x21, 0x00, 0x00, 0x23, 0x00, 0x04, 0x00, 0x00, 0x00, 0x0d},
	make([]byte, 64-14)...,
)

func TestBuildFrameMatchesReference(t *testing.T) {
	got := buildFrame(sample{cpuPct: 0, cpuMHz: 3361, tempShown: 35, miniBar: 4})
	if !bytes.Equal(got, referenceFrame) {
		t.Errorf("frame mismatch\n got  % x\n want % x", got, referenceFrame)
	}
}

func TestBuildFrameGPUPageFahrenheit(t *testing.T) {
	got := buildFrame(sample{
		cpuPct: 42.6, cpuMHz: 3800, gpuIcon: true,
		tempShown: 104, fahrenheit: true, miniBar: 4.9, rpm: 1450, ringPct: 47,
	})
	p := got[1:]
	if p[4] != 1 {
		t.Errorf("gpu icon byte = %#x, want 1", p[4])
	}
	if p[7] != 1 {
		t.Errorf("fahrenheit byte = %#x, want 1", p[7])
	}
	if p[1] != 43 { // 42.6 rounds up
		t.Errorf("cpu percent = %d, want 43", p[1])
	}
	if rpm := int(p[9])<<8 | int(p[10]); rpm != 1450 {
		t.Errorf("rpm = %d, want 1450", rpm)
	}
	if p[11] != 9 { // 47% of 20 ring segments
		t.Errorf("ring segments = %d, want 9", p[11])
	}
	if got[0] != reportID || len(got) != reportLen || p[12] != 0x0d {
		t.Errorf("frame header/tail wrong: % x", got)
	}
}

func TestClamps(t *testing.T) {
	got := buildFrame(sample{cpuPct: 250, cpuMHz: 99999, tempShown: 1000, miniBar: 99, rpm: 99999, ringPct: 500})
	p := got[1:]
	if p[1] != 100 || p[11] != 20 {
		t.Errorf("cpu/ring not clamped: %d/%d", p[1], p[11])
	}
	if mhz := int(p[2])<<8 | int(p[3]); mhz != 9999 {
		t.Errorf("mhz = %d, want 9999", mhz)
	}
}
