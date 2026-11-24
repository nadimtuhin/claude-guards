package main

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

var (
	cappedRe    = regexp.MustCompile(`--maxWorkers[= ]1\b|--runInBand\b|(^|\s)-i(\s|$)|singleFork|--no-file-parallelism`)
	bareTestRe  = regexp.MustCompile(`^(pnpm|npm|yarn|bun)(\s+run)?\s+test\s*$`)
	envAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=\S*$`)
	fileLikeRe  = regexp.MustCompile(`[/.]`)
	tscExtRe    = regexp.MustCompile(`\.tsx?$`)
)

var commandWrappers = map[string]bool{
	"time": true, "nohup": true, "sudo": true, "env": true, "command": true, "exec": true,
	"timeout": true, "nice": true, "xargs": true, "caffeinate": true, "stdbuf": true,
}

// shellKeywords precede a command without being the program it runs.
var shellKeywords = map[string]bool{"!": true, "{": true, "if": true, "then": true, "elif": true, "else": true, "while": true, "until": true, "do": true}

// wrapperValuedFlags take the next word as their value.
var wrapperValuedFlags = map[string]bool{"-n": true, "-u": true, "-g": true, "-s": true, "-k": true, "-I": true, "-L": true, "-P": true, "-C": true, "--signal": true, "--kill-after": true}

var (
	durationRe   = regexp.MustCompile(`^\d+(\.\d+)?[smhd]?$`)
	localShells  = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true, "fish": true, "ksh": true}
	shellCFlagRe = regexp.MustCompile(`^-[A-Za-z]*c[A-Za-z]*$`)
)
var packageExecutors = map[string]bool{"npx": true, "bunx": true, "pnpm": true, "yarn": true, "npm": true}
var executorVerbs = map[string]bool{"exec": true, "dlx": true, "run": true}

// executedProgram returns the program a segment runs and its arguments, looking through env
// assignments, command wrappers and package executors (npx, pnpm exec, ...). Empty when the
// segment runs nothing.
func executedProgram(words []string) (string, []string, bool) {
	i := commandStart(words)
	if i < len(words) && packageExecutors[path.Base(words[i])] {
		i++
		for i < len(words) && (strings.HasPrefix(words[i], "-") || executorVerbs[words[i]]) {
			i++
		}
	}
	if i >= len(words) {
		return "", nil, false
	}
	return path.Base(words[i]), words[i+1:], true
}

// commandStart returns the index of the first word after shell keywords, env assignments and
// command wrappers (with their flags and values).
func commandStart(words []string) int {
	i := 0
	for i < len(words) {
		switch w := words[i]; {
		case shellKeywords[w] || envAssignRe.MatchString(w):
			i++
		case commandWrappers[w]:
			i++
			for i < len(words) && strings.HasPrefix(words[i], "-") && words[i] != "-" {
				if wrapperValuedFlags[words[i]] {
					i++
				}
				i++
			}
			if w == "timeout" && i < len(words) && durationRe.MatchString(words[i]) {
				i++
			}
		default:
			return i
		}
	}
	return i
}

// shellScript returns the script a local shell runs with -c, if any.
func shellScript(program string, args []string) (string, bool) {
	if !localShells[program] {
		return "", false
	}
	sawC := false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-o" || a == "+o":
			i++
		case shellCFlagRe.MatchString(a):
			sawC = true
		case strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+"):
		default:
			return a, sawC
		}
	}
	return "", false
}

// overridden reports whether HEAVY_OK=1 is set for this segment's command, either as a leading
// assignment or by an earlier export.
func overridden(words []string) bool {
	return slices.Contains(words[:commandStart(words)], "HEAVY_OK=1")
}

func exportsOverride(words []string) bool {
	return len(words) > 1 && words[0] == "export" && slices.Contains(words[1:], "HEAVY_OK=1")
}

func withoutEnvAssignments(words []string) []string {
	var kept []string
	for _, w := range words {
		if !envAssignRe.MatchString(w) {
			kept = append(kept, w)
		}
	}
	return kept
}

func checkRunner(args []string) string {
	hasFile := false
	for _, a := range args {
		if a != "run" && !strings.HasPrefix(a, "-") && fileLikeRe.MatchString(a) {
			hasFile = true
		}
	}
	if !hasFile {
		return "whole-suite vitest/jest run: pass the specific test file(s)"
	}
	if !cappedRe.MatchString(strings.Join(args, " ")) {
		return "uncapped test workers: add --maxWorkers=1"
	}
	return ""
}

func checkTsc(args []string) string {
	for _, a := range args {
		if a == "-p" || a == "--project" || strings.HasPrefix(a, "--project=") || tscExtRe.MatchString(a) {
			return ""
		}
	}
	return "repo-wide tsc: pass -p <tsconfig> for the package you touched"
}

// verdict returns the refusal reason, or "" to allow. Only the program a segment executes counts:
// a tool name appearing as an argument, inside quotes or in a heredoc body is not an invocation.
func verdict(command string) string {
	exported := false
	for _, words := range splitSegments(command) {
		if exportsOverride(words) {
			exported = true
		}
		if exported || overridden(words) {
			continue
		}
		program, args, ok := executedProgram(words)
		if !ok || program == "ssh" {
			continue
		}
		if script, ok := shellScript(program, args); ok {
			if reason := verdict(script); reason != "" {
				return reason
			}
			continue
		}
		if bareTestRe.MatchString(strings.Join(withoutEnvAssignments(words[commandStart(words):]), " ")) {
			return "bare package test script runs the whole suite"
		}
		switch program {
		case "vitest", "jest":
			if reason := checkRunner(args); reason != "" {
				return reason
			}
		case "tsc":
			if reason := checkTsc(args); reason != "" {
				return reason
			}
		}
	}
	return ""
}
