package main

import (
	"math"
	"runtime"
	"strconv"
	"strings"
)

const pressureSysctl = "kern.memorystatus_vm_pressure_level"

type capacity struct {
	busy     float64
	pressure int
}

type probeFunc func(file string, args ...string) string

// ponytail: sums per-process %CPU from ps; returns ok=false on no samples so an unreadable ps is never idle.
func cpuBusyPercent(psOutput string, cores int) (float64, bool) {
	if cores <= 0 {
		return 0, false
	}
	total, samples := 0.0, 0
	for _, line := range strings.Split(psOutput, "\n") {
		if n, ok := leadingFloat(line); ok {
			total += n
			samples++
		}
	}
	if samples == 0 {
		return 0, false
	}
	return math.Min(100, total/float64(cores)), true
}

func leadingFloat(line string) (float64, bool) {
	s := strings.TrimLeft(line, " \t")
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.' || (end == 0 && (s[end] == '-' || s[end] == '+'))) {
		end++
	}
	n, err := strconv.ParseFloat(s[:end], 64)
	return n, err == nil
}

func memoryPressureLevel(sysctlOutput string) (int, bool) {
	level, err := strconv.Atoi(strings.TrimSpace(sysctlOutput))
	return level, err == nil && level > 0
}

func hasSpareCapacity(c *capacity, env envReader) bool {
	return c != nil && c.busy <= cpuMaxFrom(env) && float64(c.pressure) <= pressureMaxFrom(env)
}

func measureCapacity(cores int, probe probeFunc) *capacity {
	busy, busyOK := cpuBusyPercent(probe("ps", "-Ao", "pcpu="), cores)
	pressure, pressureOK := readPressure(probe)
	if !busyOK || !pressureOK {
		return nil
	}
	return &capacity{busy: busy, pressure: pressure}
}

// why: kern.memorystatus_vm_pressure_level exists only on macOS; elsewhere memory pressure is not gated.
func readPressure(probe probeFunc) (int, bool) {
	if runtime.GOOS != "darwin" {
		return 0, true
	}
	return memoryPressureLevel(probe("sysctl", "-n", pressureSysctl))
}
