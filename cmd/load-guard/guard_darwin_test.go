//go:build darwin

package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPressureProbeArgv(t *testing.T) {
	if pressureSysctl != "kern.memorystatus_vm_pressure_level" {
		t.Fatalf("wrong sysctl key %q", pressureSysctl)
	}
	var calls []string
	spy := func(file string, args ...string) string {
		calls = append(calls, file+" "+strings.Join(args, " "))
		if file == "ps" {
			return "10.0\n20.0\n"
		}
		return "2\n"
	}
	c := measureCapacity(8, spy)
	if len(calls) != 2 || calls[0] != "ps -Ao pcpu=" || calls[1] != "sysctl -n kern.memorystatus_vm_pressure_level" {
		t.Errorf("probe argv: %v", calls)
	}
	if c == nil || c.busy != 3.75 || c.pressure != 2 {
		t.Errorf("each reading goes to its own parser: %+v", c)
	}
	out, err := exec.Command("sysctl", "-n", pressureSysctl).Output()
	if level, ok := memoryPressureLevel(string(out)); err != nil || !ok || level < 1 {
		t.Errorf("positive control: the key must read on this machine: %q %v", out, err)
	}
}

func TestFiveMinuteLoadMatchesSysctl(t *testing.T) {
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	fields := strings.Fields(strings.Trim(strings.TrimSpace(string(out)), "{}"))
	if err != nil || len(fields) < 3 {
		t.Fatalf("positive control: sysctl vm.loadavg must read: %q %v", out, err)
	}
	reference, _ := parseNumber(fields[1])
	got, ok := fiveMinuteLoad()
	if !ok || got < 0 || got > reference*3+5 || reference > got*3+5 {
		t.Errorf("fiveMinuteLoad = %v,%v but sysctl says 5-min %v", got, ok, reference)
	}
}
