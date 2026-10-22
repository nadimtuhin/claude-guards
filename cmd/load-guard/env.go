package main

import (
	"math"
	"os"
	"strconv"
	"strings"
)

type envReader func(key string) string

func osEnv(key string) string { return os.Getenv(key) }

func parseNumber(raw string) (float64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, true
	}
	n, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		return 0, false
	}
	return n, true
}

func positiveOr(raw string, fallback float64) float64 {
	if n, ok := parseNumber(raw); ok && n > 0 {
		return n
	}
	return fallback
}

func nonNegativeOr(raw string, fallback float64) float64 {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	if n, ok := parseNumber(raw); ok && n >= 0 {
		return n
	}
	return fallback
}

func limitFrom(env envReader, cores int) float64 {
	return positiveOr(env("LOAD_GUARD_MAX"), float64(cores)*1.5)
}
func maxConcurrentFrom(env envReader) float64 { return positiveOr(env("LOAD_GUARD_MAX_CONCURRENT"), 2) }
func maxWorkersFrom(env envReader) float64    { return positiveOr(env("LOAD_GUARD_MAX_WORKERS"), 2) }
func reapAgeFrom(env envReader) float64       { return positiveOr(env("LOAD_GUARD_REAP_AGE"), 600) }
func cpuMaxFrom(env envReader) float64        { return nonNegativeOr(env("LOAD_GUARD_CPU_MAX"), 85) }
func busyCPUFrom(env envReader) float64       { return positiveOr(env("LOAD_GUARD_BUSY_CPU"), 5) }
func pressureMaxFrom(env envReader) float64 {
	return nonNegativeOr(env("LOAD_GUARD_MEM_PRESSURE_MAX"), 2)
}

func formatNumber(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }
