package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	probeTimeout   = 5 * time.Second
	blockExitCode  = 2
	overrideAdvice = "re-run as: LOAD_GUARD=0 <command>"
)

func main() {
	os.Exit(runGuard(os.Stdin, os.Stderr, osEnv))
}

func commandOf(raw []byte) string {
	var payload struct {
		ToolInput struct {
			Command json.RawMessage `json:"command"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	var command string
	if json.Unmarshal(payload.ToolInput.Command, &command) != nil {
		return ""
	}
	return command
}

func spawnProbe(file string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, file, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func reapStaleWorkers(env envReader) []worker {
	if env("LOAD_GUARD_REAP") == "0" {
		return nil
	}
	ps := spawnProbe("ps", "-Ao", "pid=,ppid=,etime=,command=")
	stale := staleWorkers(ps, reapAgeFrom(env))
	for _, w := range stale {
		_ = syscall.Kill(w.pid, syscall.SIGTERM)
	}
	return stale
}

func reapedNote(reaped []worker, tail string) string {
	if len(reaped) == 0 {
		return ""
	}
	pids := make([]string, len(reaped))
	for i, w := range reaped {
		pids[i] = strconv.Itoa(w.pid)
	}
	return fmt.Sprintf(". Reaped %d orphaned test worker(s): %s%s", len(reaped), strings.Join(pids, ", "), tail)
}

func roundHalfUp(n float64) string { return strconv.FormatFloat(math.Floor(n+0.5), 'f', 0, 64) }

func highLoadMessage(load, limit float64, cores int, c *capacity, others int, reaped []worker, env envReader) string {
	var b strings.Builder
	fmt.Fprintf(&b, "load-guard: 5-min load %.2f is over the %s limit for %d cores", load, formatNumber(limit), cores)
	if c != nil {
		fmt.Fprintf(&b, ", CPU %s%% busy (max %s), memory pressure %d (max %s)",
			roundHalfUp(c.busy), formatNumber(cpuMaxFrom(env)), c.pressure, formatNumber(pressureMaxFrom(env)))
	} else {
		b.WriteString(", and capacity could not be measured")
	}
	if others > 0 {
		fmt.Fprintf(&b, ", and %d heavy job(s) are already running", others)
	}
	b.WriteString(reapedNote(reaped, " -- load should fall shortly"))
	b.WriteString(". Wait for it to drop and retry, or do something cheap meanwhile. ")
	b.WriteString("If this one genuinely cannot wait, re-run it as: LOAD_GUARD=0 <command>. ")
	b.WriteString("Thresholds: LOAD_GUARD_MAX, LOAD_GUARD_CPU_MAX, LOAD_GUARD_MEM_PRESSURE_MAX\n")
	return b.String()
}

func runGuard(stdin io.Reader, stderr io.Writer, env envReader) int {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return 0
	}
	raw := commandOf(input)
	if raw == "" || isOverride(withoutQuotedText(raw)) || !isCostly(raw) {
		return 0
	}
	command := withoutQuotedText(withoutHeredocBodies(raw))

	if problem := workerCapProblem(command, env); problem != "" {
		fmt.Fprintf(stderr, "load-guard: %s. Or %s\n", problem, overrideAdvice)
		return blockExitCode
	}

	running := runningHeavyRoots(spawnProbe("ps", "-Ao", "pid=,ppid=,pcpu=,command="), busyCPUFrom(env))
	maxConcurrent := maxConcurrentFrom(env)
	if float64(running) >= maxConcurrent {
		reaped := reapStaleWorkers(env)
		fmt.Fprintf(stderr, "load-guard: %d heavy job(s) running, at the LOAD_GUARD_MAX_CONCURRENT cap of %s%s. Wait for one to finish and retry, or %s\n",
			running, formatNumber(maxConcurrent), reapedNote(reaped, ""), overrideAdvice)
		return blockExitCode
	}

	load, ok := fiveMinuteLoad()
	cores := runtime.NumCPU()
	limit := limitFrom(env, cores)
	if !ok || !(load > limit) {
		return 0
	}
	c := measureCapacity(cores, spawnProbe)
	if hasSpareCapacity(c, env) {
		return 0
	}
	reaped := reapStaleWorkers(env)
	others := countHeavyLines(spawnProbe("ps", "-Ao", "command="))
	fmt.Fprint(stderr, highLoadMessage(load, limit, cores, c, others, reaped, env))
	return blockExitCode
}
