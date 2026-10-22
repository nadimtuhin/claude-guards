package main

import (
	"path"
	"regexp"
	"strings"
)

var (
	cappedRe    = regexp.MustCompile(`--maxWorkers[= ]1\b|--runInBand\b|(^|\s)-i(\s|$)|singleFork|--no-file-parallelism`)
	bareTestRe  = regexp.MustCompile(`^(pnpm|npm|yarn|bun)(\s+run)?\s+test\s*$`)
	envAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=\S*$`)
	fileLikeRe  = regexp.MustCompile(`[/.]`)
	tscExtRe    = regexp.MustCompile(`\.tsx?$`)
)

var commandWrappers = map[string]bool{"time": true, "nohup": true, "sudo": true, "env": true, "command": true, "exec": true}
var packageExecutors = map[string]bool{"npx": true, "bunx": true, "pnpm": true, "yarn": true, "npm": true}
var executorVerbs = map[string]bool{"exec": true, "dlx": true, "run": true}

// executedProgram returns the program a segment runs and its arguments, looking through env
// assignments, command wrappers and package executors (npx, pnpm exec, ...). Empty when the
// segment runs nothing.
func executedProgram(words []string) (string, []string, bool) {
	i := 0
	for i < len(words) && (envAssignRe.MatchString(words[i]) || commandWrappers[words[i]]) {
		i++
	}
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

func hasHeavyOK(segments [][]string) bool {
	for _, words := range segments {
		for _, w := range words {
			if w == "HEAVY_OK=1" {
				return true
			}
		}
	}
	return false
}

// verdict returns the refusal reason, or "" to allow. Only the program a segment executes counts:
// a tool name appearing as an argument, inside quotes or in a heredoc body is not an invocation.
func verdict(command string) string {
	segments := splitSegments(command)
	if hasHeavyOK(segments) {
		return ""
	}
	for _, words := range segments {
		program, args, ok := executedProgram(words)
		if !ok || program == "ssh" {
			continue
		}
		if bareTestRe.MatchString(strings.Join(withoutEnvAssignments(words), " ")) {
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
