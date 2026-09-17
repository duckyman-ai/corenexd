package main

import (
	"encoding/binary"
	"math"
)

const (
	reportID  = 0x10
	reportLen = 64
)

// sample is everything the LCD can display for one refresh.
type sample struct {
	cpuPct     float64
	cpuMHz     int
	gpuIcon    bool
	tempShown  float64 // already converted to the display unit
	fahrenheit bool
	miniBar    float64
	rpm        int
	ringPct    float64
}

// buildFrame packs a sample into the cooler's 64-byte input report.
//
// Layout (13 payload bytes after the report id, zero-padded to 64):
//
//	off  size  meaning
//	 0    1    display mode: 0x01 = normal (the only mode corenexd uses).
//	          Verified on hardware: 0x02 lights every segment including all
//	          digit strokes (factory test pattern); 0x03 blanks most of the
//	          screen while the ring still renders. Other values unexplored.
//	 1    1    CPU usage, percent 0-100
//	 2    2    CPU frequency, MHz, big-endian
//	 4    1    temperature icon: 0 = CPU, 1 = GPU
//	 5    2    temperature, whole degrees, big-endian
//	 7    1    temperature unit: 0 = Celsius, 1 = Fahrenheit
//	 8    1    thermometer mini bar, 0-10
//	 9    2    fan RPM, big-endian
//	11    1    outer ring load segments, 0-20
//	12    1    constant 0x0d
func buildFrame(s sample) []byte {
	buf := make([]byte, reportLen)
	buf[0] = reportID
	p := buf[1:]
	p[0] = 0x01
	p[1] = clampByte(s.cpuPct, 0, 100)
	binary.BigEndian.PutUint16(p[2:4], uint16(clampInt(s.cpuMHz, 0, 9999)))
	if s.gpuIcon {
		p[4] = 1
	}
	binary.BigEndian.PutUint16(p[5:7], uint16(clampInt(int(math.Round(s.tempShown)), 0, 999)))
	if s.fahrenheit {
		p[7] = 1
	}
	p[8] = clampByte(s.miniBar, 0, 10)
	binary.BigEndian.PutUint16(p[9:11], uint16(clampInt(s.rpm, 0, 9999)))
	p[11] = clampByte(s.ringPct/5, 0, 20)
	p[12] = 0x0d
	return buf
}

func clampByte(value float64, low, high int) byte {
	return byte(clampInt(int(math.Round(value)), low, high))
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
