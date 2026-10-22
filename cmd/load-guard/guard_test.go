package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var saturated = map[string]string{"LOAD_GUARD_MAX": "0.01", "LOAD_GUARD_CPU_MAX": "0", "LOAD_GUARD_MEM_PRESSURE_MAX": "1"}
var roomy = map[string]string{"LOAD_GUARD_MAX": "0.01", "LOAD_GUARD_CPU_MAX": "100", "LOAD_GUARD_MEM_PRESSURE_MAX": "4"}
var lowLoad = map[string]string{"LOAD_GUARD_MAX": "9999"}

func envOf(overrides map[string]string) envReader {
	merged := map[string]string{"LOAD_GUARD_MAX_CONCURRENT": "9999"}
	for k, v := range overrides {
		merged[k] = v
	}
	return func(key string) string { return merged[key] }
}

func run(t *testing.T, command string, overrides map[string]string) (int, string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": command}})
	var stderr bytes.Buffer
	code := runGuard(bytes.NewReader(payload), &stderr, envOf(overrides))
	return code, stderr.String()
}

func expectCodes(t *testing.T, want int, env map[string]string, commands ...string) {
	t.Helper()
	for _, command := range commands {
		if got, msg := run(t, command, env); got != want {
			t.Errorf("%q: want exit %d, got %d (%s)", command, want, got, msg)
		}
	}
}

func TestBlocksAndEscapeHatch(t *testing.T) {
	code, msg := run(t, "pnpm build", saturated)
	if code != 2 || !strings.Contains(msg, "LOAD_GUARD=0") || !strings.Contains(msg, "5-min load") {
		t.Fatalf("plain build must block with the advertised message, got %d %q", code, msg)
	}
	expectCodes(t, 0, saturated, "LOAD_GUARD=0 pnpm build", "CI=1 LOAD_GUARD=0 pnpm build", "export LOAD_GUARD=0; pnpm build")
	expectCodes(t, 2, saturated, "echo 'LOAD_GUARD=0' && pnpm build", "LOAD_GUARD=0000 pnpm build")
}

func TestQuotedTextIsNotThisMachineBuilding(t *testing.T) {
	expectCodes(t, 0, saturated,
		"echo 'docs say sudo make install'", "echo '(pnpm build)'", "echo 'line1\npnpm build'",
		`ssh host "cd /x && pnpm build"`, `git commit -m "fix: make sure tsc passes"`,
		"grep -r 'pnpm build' .", "cat tsc.log", "git status")
}

func TestLocalShellAndSubstitutionRunQuotedText(t *testing.T) {
	expectCodes(t, 2, saturated,
		`bash -c "pnpm build"`, "sh -c 'pnpm -r build'", `zsh -c "make"`, `bash -lc "pnpm build"`,
		`zsh -ic "pnpm build"`, `sh -ec "pnpm build"`, `bash -o pipefail -c "pnpm build"`, `bash --norc -c "pnpm build"`,
		`echo "$(pnpm build)"`, "echo $(pnpm build)", "echo `pnpm build`",
		"echo don't && pnpm build && echo isn't", `node -e "x" ; pnpm build`)
}

func TestRealInvocationsBlock(t *testing.T) {
	expectCodes(t, 2, saturated,
		"ls\npnpm build", "cd /x &&\n  pnpm -r build", "NODE_ENV=x pnpm build", "(pnpm build)", "pnpm --filter web test",
		"npx vitest run", "make", "pnpm i", "docker compose build", "go test ./...", "cargo run --release",
		"pip install -r r.txt", "time pnpm build", "nohup make", "caffeinate -i pnpm test", "env NODE_OPTIONS=--x pnpm build",
		"timeout 600 pnpm build", "nice -n 5 pnpm build", "node_modules/.bin/vitest run", "./node_modules/.bin/vitest run",
		"/opt/homebrew/bin/pnpm build", "cd /x && node_modules/.bin/tsc -p .", "if true; then pnpm build; fi",
		"for i in 1; do pnpm build; done")
}

func TestPayloadHandling(t *testing.T) {
	var stderr bytes.Buffer
	decoy := `{"tool_input":{"command":"pnpm test","description":"LOAD_GUARD=0 decoy"}}`
	if got := runGuard(strings.NewReader(decoy), &stderr, envOf(saturated)); got != 2 {
		t.Errorf("sibling field cannot disarm, got %d", got)
	}
	for _, garbage := range []string{"nope", "", "{}", `{"tool_input":{}}`, `{"tool_input":{"command":5}}`} {
		if got := runGuard(strings.NewReader(garbage), &stderr, envOf(saturated)); got != 0 {
			t.Errorf("%q must fail open, got %d", garbage, got)
		}
	}
	expectCodes(t, 0, map[string]string{"LOAD_GUARD_MAX": "9999"}, "pnpm build")
}

func TestLimitFrom(t *testing.T) {
	for _, bad := range []string{"", "abc", "-5", "0"} {
		if got := limitFrom(func(string) string { return bad }, 8); got != 12 {
			t.Errorf("LOAD_GUARD_MAX=%q falls back to cores*1.5, got %v", bad, got)
		}
	}
	if got := limitFrom(func(string) string { return "3" }, 8); got != 3 {
		t.Errorf("positive override, got %v", got)
	}
}

func TestRunningHeavyPattern(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"/usr/local/bin/node /repo/node_modules/.bin/vitest run", true},
		{"/Library/PrivilegedHelperTools/com.docker.vmnetd", false},
		{"/x/node_modules/typescript/lib/tsc.js", false},
		{"node /x/node_modules/typescript/lib/tsc.js -p .", true},
		{"/x/node_modules/.bin/tsc -p .", true},
		{"/repo/node_modules/.bin/playwright test e2e/a.spec.ts", true},
		{"npm exec @playwright/mcp@latest", false},
		{"node /u/.npm/_npx/abc/node_modules/.bin/playwright-mcp", false},
		{"node /u/.npm/_npx/abc/node_modules/.bin/playwright-mcp --x && playwright test x", false},
		{"/opt/node/bin/node /repo/node_modules/vitest/vitest.mjs run --maxWorkers=1 a.test.ts", true},
		{"node --max-old-space-size=4096 /repo/node_modules/typescript/bin/tsc -p tsconfig.json", true},
		{"/usr/local/bin/docker compose build server", true},
		{"go build ./...", true},
		{"go vet ./...", false},
		{"/bin/zsh -c setopt nonomatch; { cd ~/wt/x && node_modules/.bin/vitest run --maxWorkers=1 a.test.ts; }", false},
		{"node /u/mise/node/bin/lean-ctx -c cd ~/wt/x && node_modules/.bin/tsc -p tsconfig.json", false},
		{"/u/lean-ctx-bin/bin/lean-ctx -c for i in $(seq 1 4); do pnpm vitest run; done", false},
		{"/bin/zsh -c cd ~/.claude/hooks/src/load-guard && sed -i '' 's|vitest|x|' pattern.go", false},
		{"/r/node_modules/@esbuild/darwin-arm64/bin/esbuild --service=0.28.0 --ping", false},
		{"/r/node_modules/.bin/esbuild src/a.ts --bundle", true},
		{"node /repo/node_modules/.bin/tsgo -p tsconfig.json", true},
		{"/repo/node_modules/@typescript/native-preview-darwin-arm64/lib/tsgo -p tsconfig.json", true},
		{"node /r/node_modules/webpack/bin/webpack.js", true},
		{"node --import tsx /r/node_modules/vitest/vitest.mjs run", true},
		{"node --conditions development node_modules/.bin/vitest run", true},
		{"node -e vitest", false},
		{"go test ./...", true},
		{"go -C cli build ./...", true},
		{"docker run --rm -p 5432:5432 postgres:16", false},
		{"docker compose logs -f build", false},
		{"cargo run --release -- serve", false},
		{"cargo build --release", true},
	}
	for _, c := range cases {
		if got := runningHeavy(c.line); got != c.want {
			t.Errorf("runningHeavy(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

const waitLoop = "zsh -c for i in $(seq 1 40); do ~/.claude/hooks/load-guard x && break; sleep 10; done; node_modules/.bin/vitest run x"
const untilLoop = `zsh -c until echo '{"tool_input":{"command":"node_modules/.bin/tsc -p tsconfig.typecheck.json --noEmit"}}' | ~/.claude/hooks/load-guard; do sleep 10; done`

func TestGuardWaitersAreNotRunningJobs(t *testing.T) {
	if countsAsHeavy(waitLoop) || countsAsHeavy(untilLoop) {
		t.Error("a wait loop polling the guard must not count as heavy")
	}
	if !countsAsHeavy("node /r/node_modules/.bin/vitest run") {
		t.Error("a real vitest run must still count")
	}
	if got := runningHeavyRoots("  50     1 "+waitLoop+"\n  51     1 "+untilLoop, 5); got != 0 {
		t.Errorf("waiters are not roots, got %d", got)
	}
	if got := runningHeavyRoots("  50     1 "+waitLoop+"\n  60     1 node /r/node_modules/.bin/vitest run", 5); got != 1 {
		t.Errorf("a real vitest next to a waiter is exactly 1 root, got %d", got)
	}
	if got := countHeavyLines(waitLoop + "\n" + untilLoop + "\nnode /r/node_modules/.bin/vitest run\n"); got != 1 {
		t.Errorf("the decorating count excludes waiters, got %d", got)
	}
}

const psTable = `  6197     1 22:48:31 node /repo/node_modules/.bin/vitest run
 12345  9999 10:00:00 node /repo/node_modules/.bin/vitest run
 23456     1 00:00:30 node /repo/node_modules/.bin/vitest run
 34567     1 05:00:00 /Applications/Google Chrome.app/Contents/MacOS/Chrome
 45678     1 01-02:03:04 node /repo/node_modules/.bin/jest --ci`

func pids(workers []worker) string {
	var out []string
	for _, w := range workers {
		out = append(out, itoa(w.pid))
	}
	return strings.Join(out, ",")
}

func itoa(n int) string { return formatNumber(float64(n)) }

func TestStaleWorkers(t *testing.T) {
	if got := pids(staleWorkers(psTable, 600)); got != "6197,45678" {
		t.Errorf("kills only the two orphaned runners, got %s", got)
	}
	if got := len(staleWorkers(psTable, 86400*365)); got != 0 {
		t.Errorf("nothing qualifies with a huge threshold, got %d", got)
	}
	if got := pids(staleWorkers(psTable, 0)); got != "6197,23456,45678" {
		t.Errorf("zero threshold still spares live parents, got %s", got)
	}
	if got := reapAgeFrom(func(string) string { return "" }); got != 600 {
		t.Errorf("reap age defaults to 600, got %v", got)
	}
	if got := reapAgeFrom(func(string) string { return "60" }); got != 60 {
		t.Errorf("explicit reap age, got %v", got)
	}
	if got := len(staleWorkers(" 1 1 whenever node vitest run", 600)); got != 0 {
		t.Errorf("garbage age cannot pass a real threshold, got %d", got)
	}
	for _, safe := range []string{"/x/node_modules/.bin/tsc -p .", "docker compose build", "go build ./...", "cargo test"} {
		if reapableRe.MatchString(safe) {
			t.Errorf("must not reap %q", safe)
		}
	}
}

func TestEtimeSeconds(t *testing.T) {
	for in, want := range map[string]int{"01:30": 90, "01:01:01": 3661, "1-02:03:04": 93784, "  01:30": 90, "whenever": 0} {
		if got := etimeSeconds(in); got != want {
			t.Errorf("etimeSeconds(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestCpuBusyPercent(t *testing.T) {
	cases := []struct {
		ps    string
		cores int
		want  float64
		ok    bool
	}{
		{"10.0\n100.0\n120.8\n", 8, 28.85, true},
		{"400.0\n400.0\n", 8, 100, true},
		{"500.0\n400.0\n", 8, 100, true},
		{"0.0\n0.0\n", 8, 0, true},
		{"", 8, 0, false},
		{"%CPU\n", 8, 0, false},
		{"100.0\n", 0, 0, false},
	}
	for _, c := range cases {
		got, ok := cpuBusyPercent(c.ps, c.cores)
		if ok != c.ok || (ok && (got < c.want-0.01 || got > c.want+0.01)) {
			t.Errorf("cpuBusyPercent(%q,%d) = %v,%v want %v,%v", c.ps, c.cores, got, ok, c.want, c.ok)
		}
	}
}

func TestMemoryPressureLevel(t *testing.T) {
	for in, want := range map[string]int{"1\n": 1, "2\n": 2, "4\n": 4} {
		if got, ok := memoryPressureLevel(in); !ok || got != want {
			t.Errorf("memoryPressureLevel(%q) = %d,%v", in, got, ok)
		}
	}
	for _, bad := range []string{"garbage", "", "0"} {
		if _, ok := memoryPressureLevel(bad); ok {
			t.Errorf("%q must be unreadable", bad)
		}
	}
}

func TestSpareCapacityArms(t *testing.T) {
	arms := func(k string) string {
		return map[string]string{"LOAD_GUARD_CPU_MAX": "85", "LOAD_GUARD_MEM_PRESSURE_MAX": "1"}[k]
	}
	cases := []struct {
		name string
		c    *capacity
		want bool
	}{
		{"free and normal", &capacity{30, 1}, true},
		{"memory pressure", &capacity{30, 2}, false},
		{"cpu pegged", &capacity{99, 1}, false},
		{"both saturated", &capacity{99, 4}, false},
		{"unmeasured", nil, false},
		{"inclusive boundary", &capacity{85, 1}, true},
		{"just over", &capacity{85.1, 1}, false},
	}
	for _, c := range cases {
		if got := hasSpareCapacity(c.c, arms); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func envWith(pairs map[string]string) envReader { return func(k string) string { return pairs[k] } }

func TestThresholdKnobs(t *testing.T) {
	cases := []struct {
		name string
		c    capacity
		env  map[string]string
		want bool
	}{
		{"CPU_MAX=0 is saturation", capacity{1, 1}, map[string]string{"LOAD_GUARD_CPU_MAX": "0"}, false},
		{"empty CPU_MAX falls back to 85", capacity{50, 1}, map[string]string{"LOAD_GUARD_CPU_MAX": ""}, true},
		{"abc CPU_MAX under", capacity{84, 1}, map[string]string{"LOAD_GUARD_CPU_MAX": "abc"}, true},
		{"abc CPU_MAX over", capacity{86, 1}, map[string]string{"LOAD_GUARD_CPU_MAX": "abc"}, false},
		{"-5 CPU_MAX under", capacity{84, 1}, map[string]string{"LOAD_GUARD_CPU_MAX": "-5"}, true},
		{"-5 CPU_MAX over", capacity{86, 1}, map[string]string{"LOAD_GUARD_CPU_MAX": "-5"}, false},
		{"PRESSURE_MAX=4 tolerates warn", capacity{10, 2}, map[string]string{"LOAD_GUARD_MEM_PRESSURE_MAX": "4"}, true},
		{"unset pressure tolerates warn", capacity{10, 2}, nil, true},
		{"unset pressure blocks critical", capacity{10, 4}, nil, false},
		{"PRESSURE_MAX=0 blocks normal", capacity{10, 1}, map[string]string{"LOAD_GUARD_MEM_PRESSURE_MAX": "0"}, false},
		{"empty pressure tolerates warn", capacity{10, 2}, map[string]string{"LOAD_GUARD_MEM_PRESSURE_MAX": ""}, true},
		{"empty pressure blocks critical", capacity{10, 4}, map[string]string{"LOAD_GUARD_MEM_PRESSURE_MAX": ""}, false},
		{"abc pressure blocks critical", capacity{10, 4}, map[string]string{"LOAD_GUARD_MEM_PRESSURE_MAX": "abc"}, false},
		{"unset cpu blocks pegged", capacity{86, 1}, nil, false},
		{"unset cpu allows 84.9", capacity{84.9, 1}, nil, true},
	}
	for _, c := range cases {
		c := c
		if got := hasSpareCapacity(&c.c, envWith(c.env)); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestCapacityEndToEnd(t *testing.T) {
	if code, _ := run(t, "pnpm build", roomy); code != 0 {
		t.Errorf("load over limit but capacity free must allow, got %d", code)
	}
	code, msg := run(t, "pnpm build", saturated)
	if code != 2 {
		t.Fatalf("no capacity must block, got %d", code)
	}
	if !strings.Contains(msg, "LOAD_GUARD_CPU_MAX, LOAD_GUARD_MEM_PRESSURE_MAX") {
		t.Errorf("block advertises the knobs: %q", msg)
	}
	if !strings.Contains(msg, "CPU ") || !strings.Contains(msg, "% busy (max 0), memory pressure ") || !strings.Contains(msg, "(max 1)") {
		t.Errorf("block names both arms and limits: %q", msg)
	}
	if _, msg := run(t, "pnpm build", merge(saturated, "LOAD_GUARD_REAP", "0")); strings.Contains(msg, "Reaped") {
		t.Error("LOAD_GUARD_REAP=0 reaps nothing")
	}
}

func merge(base map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func TestUnmeasurableMachineKeepsLoadOnlyBlock(t *testing.T) {
	for _, dir := range []string{"/bin", "/usr/sbin", "/nonexistent"} {
		t.Setenv("PATH", dir)
		code, msg := run(t, "pnpm build", roomy)
		if code != 2 || !strings.Contains(msg, "capacity could not be measured") {
			t.Errorf("PATH=%s: want unmeasured block, got %d %q", dir, code, msg)
		}
	}
}

func TestConcurrencyCountsRunnersNotWorkers(t *testing.T) {
	runner := "  100     1 node /r/node_modules/.bin/vitest run"
	var workers []string
	for _, pid := range []string{"101", "102", "103", "104"} {
		workers = append(workers, " "+pid+"   100 node /r/node_modules/vitest/dist/workers/forks.js")
	}
	noise := "  500     1 /System/Library/CoreServices/Finder.app/Contents/MacOS/Finder\ngarbage\n"
	orphans := strings.ReplaceAll(strings.Join(workers, "\n"), " 100 ", " 900 ")
	cases := []struct {
		name string
		ps   string
		want int
	}{
		{"one parent with 4 workers", runner + "\n" + strings.Join(workers, "\n") + "\n" + noise, 1},
		{"workers with a non-heavy parent each count", orphans, 4},
		{"grandchild is not a root", runner + "\n" + strings.Join(workers, "\n") + "\n 105   101 node /r/node_modules/vitest/dist/workers/forks.js", 1},
		{"two independent runners", runner + "\n" + strings.Join(workers, "\n") + "\n  200     1 node /r/node_modules/.bin/jest --ci\n 201   200 node /r/node_modules/jest-worker/build/child.js jest", 2},
		{"runner under a non-heavy shell", "  300   299 node /r/node_modules/.bin/vitest run", 1},
		{"agent session is not a runner", `  400     1 /home/u/.local/bin/claude --session-id x --model opus "run npx vitest and tsc -p x"`, 0},
		{"runner under an agent still counts", "  400     1 /home/u/.local/bin/claude --session-id x\n  401   400 node /r/node_modules/.bin/vitest run", 1},
		{"no heavy process", noise, 0},
		{"empty", "", 0},
	}
	for _, c := range cases {
		if got := runningHeavyRoots(c.ps, 5); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
	if maxConcurrentFrom(envWith(nil)) != 2 || maxConcurrentFrom(envWith(map[string]string{"LOAD_GUARD_MAX_CONCURRENT": "5"})) != 5 {
		t.Error("concurrency cap default and override")
	}
	for _, bad := range []string{"", "abc", "-1", "0"} {
		if got := maxConcurrentFrom(envWith(map[string]string{"LOAD_GUARD_MAX_CONCURRENT": bad})); got != 2 {
			t.Errorf("MAX_CONCURRENT=%q falls back to 2, got %v", bad, got)
		}
	}
}

func TestConcurrencyCapBlocksAtLowLoad(t *testing.T) {
	fakeRunner := filepath.Join(t.TempDir(), "vitest")
	if err := os.Symlink("/usr/bin/yes", fakeRunner); err != nil {
		t.Fatal(err)
	}
	decoy := exec.Command(fakeRunner)
	if err := decoy.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	defer func() { _ = decoy.Process.Kill(); _ = decoy.Wait() }()
	line, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(decoy.Process.Pid)).Output()
	if err != nil || !countsAsHeavy(strings.TrimSpace(string(line))) {
		t.Fatalf("the guard must see the decoy itself: %q %v", line, err)
	}
	env := merge(lowLoad, "LOAD_GUARD_MAX_CONCURRENT", "1")
	payload := `{"tool_input":{"command":"pnpm build"}}`
	var stderr bytes.Buffer
	got := runGuard(strings.NewReader(payload), &stderr, envWith(env))
	if got != 2 || !strings.Contains(stderr.String(), "heavy job(s) running") || !strings.Contains(stderr.String(), "LOAD_GUARD_MAX_CONCURRENT") {
		t.Errorf("at the cap must block at low load: %d %q", got, stderr.String())
	}
	stderr.Reset()
	if got := runGuard(strings.NewReader(`{"tool_input":{"command":"LOAD_GUARD=0 pnpm build"}}`), &stderr, envWith(env)); got != 0 {
		t.Errorf("override beats the cap: %d", got)
	}
	if got := runGuard(strings.NewReader(`{"tool_input":{"command":"git status"}}`), &stderr, envWith(env)); got != 0 {
		t.Errorf("non-costly never capped: %d", got)
	}
}

func TestWorkerCap(t *testing.T) {
	expectCodes(t, 2, lowLoad,
		"node_modules/.bin/vitest run x.test.ts", "vitest", "npx vitest run",
		"vitest run x --poolOptions.forks.maxForks=8", "vitest run x --maxWorkers=50%", "vitest run x --maxWorkers=0",
		`vitest run x -t "--maxWorkers=1"`)
	expectCodes(t, 0, lowLoad,
		"vitest run x --poolOptions.forks.maxForks=1", "vitest run x --poolOptions.threads.maxThreads=2",
		"vitest run x --maxWorkers=2", "vitest run x --no-file-parallelism", "LOAD_GUARD=0 vitest run x", `ssh host "vitest"`)
	if code, _ := run(t, "vitest run x --poolOptions.forks.maxForks=8", merge(lowLoad, "LOAD_GUARD_MAX_WORKERS", "8")); code != 0 {
		t.Error("a raised LOAD_GUARD_MAX_WORKERS admits it")
	}
	_, msg := run(t, "vitest run a.test.ts", lowLoad)
	if !strings.Contains(msg, "append --maxWorkers=1") {
		t.Errorf("message shows the fixed command: %q", msg)
	}
	_, msg = run(t, "vitest run x --maxWorkers=8", lowLoad)
	if !strings.Contains(msg, "LOAD_GUARD_MAX_WORKERS=2") || !strings.Contains(msg, "--maxWorkers=1") {
		t.Errorf("over-limit message: %q", msg)
	}
	if workerCapProblem("vitest run --maxWorkers=1", envWith(nil)) != "" || workerCapProblem("pnpm build", envWith(nil)) != "" {
		t.Error("capped and non-vitest are clean")
	}
}

func TestExecutedProgramOnlyIsCostly(t *testing.T) {
	expectCodes(t, 2, saturated,
		"npx vitest run x --maxWorkers=1", "pnpm tsc -p y", "pnpm exec tsc -p y",
		"cd x && ./node_modules/.bin/tsc -p .", "echo done\nnpx tsc -p .", "cat <<EOF\nbody\nEOF\nnpx tsc -p .")
	expectCodes(t, 0, saturated,
		`echo "uses vitest"`, "cat <<EOF\ntsc is slow\nvitest run\nEOF", "cat >> notes.md <<'EOF'\nnpx tsc -p .\nEOF",
		"cat <<-EOF\n\tpnpm tsc -p y\n\tEOF", "grep -r vitest .", "sed -i 's/tsc/x/' f")
	expectCodes(t, 2, lowLoad, "vitest run")
	expectCodes(t, 0, lowLoad, "cat <<EOF\nvitest run\nEOF")
}

func TestIdleRunnerTreeIsNotAHeavyJob(t *testing.T) {
	cases := []struct {
		name, ps string
		want     int
	}{
		{"idle runner", "  100     1  0.0 node /r/node_modules/.bin/vitest run", 0},
		{"busy runner", "  100     1 42.5 node /r/node_modules/.bin/vitest run", 1},
		{"idle parent, busy workers sum into one tree", "  100     1  0.0 node /r/node_modules/.bin/vitest run\n  101   100  4.0 node /r/node_modules/vitest/dist/workers/forks.js\n  102   100  4.0 node /r/node_modules/vitest/dist/workers/forks.js", 1},
		{"exactly 5 is not above 5", "  100     1  5.0 node /r/node_modules/.bin/vitest run", 0},
		{"grep naming vitest is no runner", "  100     1 90.0 grep -r vitest .", 0},
		{"two busy trees", "  100     1 10.0 node /r/node_modules/.bin/vitest run\n  200     1 10.0 /r/node_modules/.bin/tsc -p .", 2},
	}
	for _, c := range cases {
		if got := runningHeavyRoots(c.ps, 5); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestBusyCPUThresholdIsConfigurable(t *testing.T) {
	ps := "  100     1  8.0 node /r/node_modules/.bin/vitest run"
	cases := []struct {
		name string
		env  map[string]string
		want int
	}{
		{"default 5% counts an 8% runner", nil, 1},
		{"raised to 10% ignores it", map[string]string{"LOAD_GUARD_BUSY_CPU": "10"}, 0},
		{"garbage falls back to 5%", map[string]string{"LOAD_GUARD_BUSY_CPU": "abc"}, 1},
		{"zero falls back to 5%", map[string]string{"LOAD_GUARD_BUSY_CPU": "0"}, 1},
	}
	for _, c := range cases {
		if got := runningHeavyRoots(ps, busyCPUFrom(envWith(c.env))); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestDefaultsMatchDocumentedValues(t *testing.T) {
	env := envWith(nil)
	if maxConcurrentFrom(env) != 2 || busyCPUFrom(env) != 5 || maxWorkersFrom(env) != 2 {
		t.Errorf("defaults drifted: concurrent=%v busy=%v workers=%v", maxConcurrentFrom(env), busyCPUFrom(env), maxWorkersFrom(env))
	}
}

func TestVitestControls(t *testing.T) {
	expectCodes(t, 2, saturated, "npx vitest run")
	expectCodes(t, 0, saturated, `echo "vitest"`, "echo 'vitest run'", "cat <<EOF\nnpx vitest run\nEOF")
}
