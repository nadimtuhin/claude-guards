package main

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	envPrefix = `(?:export\s+)?(?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+)*`
	separator = `(?:^|[;&|\n]\s*|["'` + "`" + `(]\s*|\b(?:then|do)\s+|` +
		`\b(?:sudo|time|nohup|caffeinate|env|timeout|nice|stdbuf|arch|command|eval|xargs|npx|bunx|pnpm(?:\s+(?:dlx|exec))?)` +
		`(?:\s+(?:-\S+|\d+))*\s+)`
	binaryPath    = `(?:\S*/)?`
	packageRunner = binaryPath + `(?:pnpm|npm|yarn|bun|npx)\s+(?:\S+\s+)*?(?:build|test|typecheck|lint|install|ci|i)\b`
	otherHeavy    = binaryPath + `(?:vitest|jest|playwright|tsc|esbuild|webpack|xcodebuild|gradlew|mvn)\b` +
		`|(?:cargo|go)\s+(?:build|test|run)\b` +
		`|docker(?:\s+compose)?\s+build\b` +
		`|(?:pip3?|brew)\s+install\b` +
		`|make\b`
)

var (
	overrideRe     = regexp.MustCompile(`(?m)(?:^|[;&|\n]\s*)` + envPrefix + `LOAD_GUARD=0(?:\s|;|$)`)
	costlyRe       = regexp.MustCompile(`(?m)` + separator + envPrefix + `(?:` + packageRunner + `|` + otherHeavy + `)`)
	localShellRe   = regexp.MustCompile(`\b(?:bash|sh|zsh|dash|fish)\s+(?:\S+\s+)*?-[A-Za-z]*c(?:\s|$)`)
	substitutionRe = regexp.MustCompile("\\$\\(|`")
	runnerWorkerRe = regexp.MustCompile(`/node_modules/(vitest|jest|jest-worker|playwright)/`)
	doubleQuoteRe  = regexp.MustCompile(`"[^"]*"`)
	vitestInvokeRe = regexp.MustCompile(`(?m)` + separator + envPrefix + binaryPath + `vitest\b`)
	workerCapRe    = regexp.MustCompile(`--(?:poolOptions\.forks\.maxForks|poolOptions\.threads\.maxThreads|maxWorkers)[=\s]+(\d+)`)
	noParallelRe   = regexp.MustCompile(`--no-file-parallelism\b`)
)

const suggestedCap = "--maxWorkers=1"

func isLetter(b byte) bool { return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' }

// why: RE2 has no lookaround, so the single-quote rule (not preceded or followed by a letter) is a scan.
func withoutQuotedText(command string) string {
	var out strings.Builder
	for i := 0; i < len(command); {
		if command[i] == '\'' && (i == 0 || !isLetter(command[i-1])) {
			if end := strings.IndexByte(command[i+1:], '\''); end >= 0 {
				closing := i + 1 + end
				if closing+1 == len(command) || !isLetter(command[closing+1]) {
					out.WriteString("''")
					i = closing + 1
					continue
				}
			}
		}
		out.WriteByte(command[i])
		i++
	}
	return doubleQuoteRe.ReplaceAllString(out.String(), `""`)
}

var heredocOpenRe = regexp.MustCompile(`<<(-?)\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// why: a heredoc body is data fed to another program, so a line in it that starts with tsc or
// vitest is not an invocation.
func withoutHeredocBodies(command string) string {
	var kept []string
	terminator, indented := "", false
	for _, line := range strings.Split(command, "\n") {
		if terminator != "" {
			candidate := line
			if indented {
				candidate = strings.TrimLeft(line, "\t")
			}
			if candidate == terminator {
				terminator = ""
			}
			continue
		}
		kept = append(kept, line)
		if m := heredocOpenRe.FindStringSubmatch(line); m != nil {
			terminator, indented = m[2], m[1] == "-"
		}
	}
	return strings.Join(kept, "\n")
}

func isCostly(rawWithHeredocs string) bool {
	raw := withoutHeredocBodies(rawWithHeredocs)
	command := withoutQuotedText(raw)
	runsQuotedTextLocally := localShellRe.MatchString(command) || substitutionRe.MatchString(raw)
	return costlyRe.MatchString(command) || (runsQuotedTextLocally && costlyRe.MatchString(raw))
}

func isOverride(command string) bool { return overrideRe.MatchString(command) }

// why: JS uses playwright(?![-/]mcp); RE2 has no lookahead, so the MCP server tokens are removed first.
// why: only the process's own executable counts. A shell or wrapper whose argv merely mentions a
// heavy tool (wait loops, `zsh -c`, lean-ctx, sed on this file) deadlocked the concurrency cap.
func runningHeavy(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	exe, args := executableOf(fields)
	switch exe {
	case "vitest", "jest", "jest-worker", "playwright", "tsc", "tsgo", "webpack", "xcodebuild", "gradlew", "mvn":
		return true
	case "esbuild":
		return !slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "--service") })
	case "go":
		return slices.Contains([]string{"build", "test", "install"}, subcommand(args, "-C"))
	case "cargo":
		return slices.Contains([]string{"build", "test", "check", "clippy", "bench", "nextest", "install"}, subcommand(args))
	case "docker":
		sub := subcommand(args)
		if sub == "compose" || sub == "buildx" {
			sub = subcommand(args[slices.Index(args, sub)+1:])
		}
		return sub == "build"
	case "docker-compose":
		return subcommand(args) == "build"
	}
	return false
}

// subcommand returns the first non-flag argument, skipping the value of each flag in valued.
func subcommand(args []string, valued ...string) string {
	for i := 0; i < len(args); i++ {
		if slices.Contains(valued, args[i]) {
			i++
			continue
		}
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
	}
	return ""
}

var nodeValuedFlags = []string{"-r", "--require", "--import", "--loader", "--experimental-loader", "-C", "--conditions", "--env-file", "--inspect-port", "--title"}

func executableOf(fields []string) (string, []string) {
	exe, args := path.Base(fields[0]), fields[1:]
	if exe != "node" && exe != "bun" && exe != "deno" {
		return exe, args
	}
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if slices.Contains([]string{"-e", "-p", "--eval", "--print"}, args[0]) {
			return exe, nil
		}
		if slices.Contains(nodeValuedFlags, args[0]) && len(args) > 1 {
			args = args[1:]
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return exe, args
	}
	if m := runnerWorkerRe.FindStringSubmatch(args[0]); m != nil {
		return m[1], args[1:]
	}
	script := path.Base(args[0])
	switch path.Ext(script) {
	case ".js", ".mjs", ".cjs":
		script = strings.TrimSuffix(script, path.Ext(script))
	}
	return script, args[1:]
}

func workerCapProblem(command string, env envReader) string {
	if !vitestInvokeRe.MatchString(command) || noParallelRe.MatchString(command) {
		return ""
	}
	limit := maxWorkersFrom(env)
	caps := workerCaps(command)
	if len(caps) == 0 {
		return "vitest has no worker cap: append " + suggestedCap
	}
	for _, n := range caps {
		if n < 1 || n > limit {
			return "vitest worker cap is over LOAD_GUARD_MAX_WORKERS=" + formatNumber(limit) + ": use " + suggestedCap
		}
	}
	return ""
}

func workerCaps(command string) []float64 {
	var caps []float64
	for _, m := range workerCapRe.FindAllStringSubmatchIndex(command, -1) {
		if m[1] < len(command) && strings.IndexByte("%.", command[m[1]]) >= 0 {
			continue
		}
		n, _ := strconv.ParseFloat(command[m[2]:m[3]], 64)
		caps = append(caps, n)
	}
	return caps
}
