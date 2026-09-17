package main

import (
	"reflect"
	"testing"
)

// Two GPUs installed (e.g. GT 1030 + Tesla V100): nvidia-smi returns one line
// per GPU and the display must pick one instead of failing to parse.
func TestParseNvidiaSMIMultiGPU(t *testing.T) {
	got := parseNvidiaSMI("0, 3, 37\n1, 95, 64\n")
	want := []gpuCard{{index: 0, util: 3, temp: 37}, {index: 1, util: 95, temp: 64}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cards = %+v, want %+v", got, want)
	}
}

func TestParseNvidiaSMIJunk(t *testing.T) {
	if got := parseNvidiaSMI(""); len(got) != 0 {
		t.Errorf("empty input produced %+v", got)
	}
	if got := parseNvidiaSMI("N/A, 0, 37\n1, x, 64\n"); len(got) != 0 {
		t.Errorf("bad fields produced %+v", got)
	}
}

// --doctor asks nvidia-smi for card names as a fourth field.
func TestParseNvidiaSMIWithNames(t *testing.T) {
	got := parseNvidiaSMI("0, 3, 37, NVIDIA GeForce GT 1030\n1, 95, 64, Tesla V100-SXM2-16GB\n")
	if len(got) != 2 || got[1].name != "Tesla V100-SXM2-16GB" {
		t.Errorf("cards = %+v, want two cards with names", got)
	}
}

func TestAlertRingPulse(t *testing.T) {
	a := alertRing{pulse: true}
	var seq []float64
	for i := 0; i < 12; i++ {
		v := a.next()
		if v != 0 && v != 100 {
			t.Fatalf("pulse value %v not 0 or 100", v)
		}
		seq = append(seq, v)
	}
	// One heartbeat is flash-gap-flash-rest: 100,0,100,0,0,0 repeating.
	want := []float64{100, 0, 100, 0, 0, 0, 100, 0, 100, 0, 0, 0}
	if !reflect.DeepEqual(seq, want) {
		t.Errorf("pulse sequence = %v, want %v", seq, want)
	}
}
func TestAlertRingBlink(t *testing.T) {
	a := alertRing{}
	var got []float64
	for i := 0; i < 4; i++ {
		got = append(got, a.next())
	}
	want := []float64{100, 0, 100, 0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("blink sequence = %v, want %v", got, want)
	}
}

func TestAlertRingScanFillsAndRestarts(t *testing.T) {
	a := alertRing{scan: true}
	var seq []float64
	for i := 0; i < 45; i++ {
		v := a.next()
		if v < 0 || v > 100 || int(v)%5 != 0 {
			t.Fatalf("ring value %v out of range or not a multiple of 5", v)
		}
		seq = append(seq, v)
	}
	// The sweep fills upward and, once full, restarts from zero — it never
	// walks back down segment by segment.
	wrapped := false
	for i := 1; i < len(seq); i++ {
		if seq[i] < seq[i-1] && seq[i-1] != 100 {
			t.Errorf("sequence walked backwards: %v -> %v", seq[i-1], seq[i])
		}
		if seq[i-1] == 100 && seq[i] == 0 {
			wrapped = true
		}
	}
	if !wrapped {
		t.Error("scan never restarted after filling")
	}
}

func TestGPUPick(t *testing.T) {
	cards := []gpuCard{{index: 0, util: 3, temp: 37}, {index: 1, util: 95, temp: 64}}

	if c, ok := parseGPUSelector("1").pick(cards); !ok || c.index != 1 {
		t.Errorf("index selector picked %+v ok=%v", c, ok)
	}
	// Default picks the hottest card — the one a cooling display should show.
	if c, ok := parseGPUSelector("max").pick(cards); !ok || c.index != 1 {
		t.Errorf("max selector picked %+v ok=%v", c, ok)
	}
	// Ties on temperature break towards the busier card.
	tie := []gpuCard{{index: 0, util: 10, temp: 60}, {index: 1, util: 90, temp: 60}}
	if c, _ := parseGPUSelector("max").pick(tie); c.index != 1 {
		t.Errorf("tie break picked %+v", c)
	}
	// A pinned index that no longer exists (card removed) falls back to max.
	if c, ok := parseGPUSelector("3").pick(cards); !ok || c.index != 1 {
		t.Errorf("missing index fallback picked %+v ok=%v", c, ok)
	}
	if _, ok := (gpuSelector{}).pick(nil); ok {
		t.Error("empty card list should not be ok")
	}
}
