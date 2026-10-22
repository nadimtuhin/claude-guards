package main

import (
	"os"
	"strconv"
	"strings"
)

func fiveMinuteLoad() (float64, bool) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0, false
	}
	n, err := strconv.ParseFloat(fields[1], 64)
	return n, err == nil
}
