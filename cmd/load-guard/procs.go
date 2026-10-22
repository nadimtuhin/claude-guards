package main

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	rootLineRe  = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+(?:(\d+(?:\.\d+)?)\s+)?(.*)$`)
	staleLineRe = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+(\S+)\s+(.*)$`)
	etimeRe     = regexp.MustCompile(`^(?:(\d+)-)?(?:(\d+):)?(\d+):(\d+)$`)
	reapableRe  = regexp.MustCompile(`\b(?:vitest|jest|playwright|tinypool)\b`)
)

const guardWaiterMarker = "hooks/load-guard"

// why: an agent launched as `claude "<prompt>"` carries the prompt in argv.
func isAgentSession(command string) bool {
	fields := strings.Fields(command)
	return len(fields) > 0 && (fields[0] == "claude" || strings.HasSuffix(fields[0], "/claude"))
}

// why: a wait loop polling the guard names the heavy tool in its own argv and would count itself.
func isGuardWaiter(command string) bool { return strings.Contains(command, guardWaiterMarker) }

func countsAsHeavy(command string) bool {
	return !isAgentSession(command) && !isGuardWaiter(command) && runningHeavy(command)
}

func countHeavyLines(psOutput string) int {
	count := 0
	for _, line := range strings.Split(psOutput, "\n") {
		if countsAsHeavy(line) {
			count++
		}
	}
	return count
}

type heavyProc struct {
	ppid int
	cpu  float64
}

// why: a process whose argv names a runner but sits idle costs nothing, so a runner tree counts
// only while its processes together use more than the busy-CPU threshold. A line without a %CPU column
// is treated as busy.
func runningHeavyRoots(psOutput string, busyCPUPercent float64) int {
	heavy := map[int]heavyProc{}
	for _, line := range strings.Split(psOutput, "\n") {
		m := rootLineRe.FindStringSubmatch(line)
		if m == nil || !countsAsHeavy(m[4]) {
			continue
		}
		pid, _ := strconv.Atoi(m[1])
		ppid, _ := strconv.Atoi(m[2])
		cpu := busyCPUPercent + 1
		if m[3] != "" {
			cpu, _ = strconv.ParseFloat(m[3], 64)
		}
		heavy[pid] = heavyProc{ppid, cpu}
	}
	treeCPU := map[int]float64{}
	for pid, proc := range heavy {
		root := pid
		for {
			_, ok := heavy[heavy[root].ppid]
			if !ok || heavy[root].ppid == root {
				break
			}
			root = heavy[root].ppid
		}
		treeCPU[root] += proc.cpu
	}
	roots := 0
	for _, cpu := range treeCPU {
		if cpu > busyCPUPercent {
			roots++
		}
	}
	return roots
}

func etimeSeconds(etime string) int {
	m := etimeRe.FindStringSubmatch(strings.TrimSpace(etime))
	if m == nil {
		return 0
	}
	part := func(s string) int { n, _ := strconv.Atoi(s); return n }
	return part(m[1])*86400 + part(m[2])*3600 + part(m[3])*60 + part(m[4])
}

type worker struct {
	pid, ppid, age int
	command        string
}

func staleWorkers(psOutput string, minAgeSeconds float64) []worker {
	var stale []worker
	for _, line := range strings.Split(psOutput, "\n") {
		m := staleLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		w := worker{command: m[4], age: etimeSeconds(m[3])}
		w.pid, _ = strconv.Atoi(m[1])
		w.ppid, _ = strconv.Atoi(m[2])
		if w.ppid == 1 && float64(w.age) >= minAgeSeconds && reapableRe.MatchString(w.command) {
			stale = append(stale, w)
		}
	}
	return stale
}
