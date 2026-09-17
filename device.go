package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	usbVID = "2516" // Cooler Master Co., Ltd.
	usbPID = "0234" // MLQ-Core-Nex-Digital
)

// findHidraw returns the /dev/hidraw* node whose USB ancestry matches the
// cooler's vendor/product id.
func findHidraw(vid, pid string) (string, error) {
	entries, err := os.ReadDir("/sys/class/hidraw")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		node, err := filepath.EvalSymlinks(filepath.Join("/sys/class/hidraw", e.Name(), "device"))
		if err != nil {
			continue
		}
		for hop := 0; hop < 8; hop++ {
			if matchIDs(node, vid, pid) {
				return filepath.Join("/dev", e.Name()), nil
			}
			parent := filepath.Dir(node)
			if parent == node {
				break
			}
			node = parent
		}
	}
	return "", errors.New("no matching device under /sys/class/hidraw")
}

func matchIDs(node, vid, pid string) bool {
	v, err1 := os.ReadFile(filepath.Join(node, "idVendor"))
	p, err2 := os.ReadFile(filepath.Join(node, "idProduct"))
	if err1 != nil || err2 != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(string(v)), vid) &&
		strings.EqualFold(strings.TrimSpace(string(p)), pid)
}

// writeReport sends one report to the device, re-opening the node each time
// so a replugged cooler is picked up without restarting.
func writeReport(dev string, frame []byte) error {
	f, err := os.OpenFile(dev, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", dev, err)
	}
	defer f.Close()
	if _, err := f.Write(frame); err != nil {
		return fmt.Errorf("write %s: %w", dev, err)
	}
	return nil
}
