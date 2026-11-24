package main

import "testing"

func TestBlocked(t *testing.T) {
	for _, c := range []string{
		"npx vitest run", "pnpm vitest", "cd x && vitest run --reporter dot", "npx vitest run cli/lib/a.test.ts",
		"jest src/foo.test.ts", "pnpm test", "npm run test", "npx tsc --noEmit",
		"pnpm exec vitest run", "./node_modules/.bin/vitest run", "cd x; pnpm tsc", "echo hi | vitest run",
		"cat <<EOF\nbody\nEOF\nnpx vitest run", "echo 'a' && npx tsc --noEmit",
	} {
		if verdict(c) == "" {
			t.Errorf("must block: %q", c)
		}
	}
}

func TestAllowed(t *testing.T) {
	for _, c := range []string{
		"npx vitest run cli/lib/a.test.ts --maxWorkers=1", "vitest run a.test.ts --pool=forks --poolOptions.forks.singleFork",
		"jest src/foo.test.ts --runInBand", "pnpm test cli/lib/a.test.ts", "tsc -p tsconfig.cli.json --noEmit",
		"HEAVY_OK=1 npx vitest run", "ssh host 'pnpm test'", "git status", "",
		`echo "uses vitest"`, `echo "pnpm test"`, "echo 'run vitest and tsc'", "cat <<EOF\nvitest run\ntsc\npnpm test\nEOF",
		"cat >> notes.md <<'EOF'\nnpx tsc --noEmit\nEOF", "cat <<-EOF\n\tnpx vitest\n\tEOF", "grep -r vitest .",
		"sed -i 's/tsc/x/' f", "git commit -m \"fix tsc and vitest\"", "ls node_modules/.bin/vitest", "cat jest.config.js",
		"pnpm install", "ssh host pnpm test", "echo hi # vitest run",
	} {
		if reason := verdict(c); reason != "" {
			t.Errorf("must allow %q, got %q", c, reason)
		}
	}
}

func TestHeavyOKOnlyAsLeadingAssignment(t *testing.T) {
	cases := []struct {
		command string
		blocked bool
	}{
		{"HEAVY_OK=1 npx vitest run", false},
		{"CI=1 HEAVY_OK=1 npx vitest run", false},
		{"export HEAVY_OK=1; npx vitest run", false},
		{"echo HEAVY_OK=1; npx vitest run", true},
		{`git commit -m "HEAVY_OK=1" && npx vitest run`, true},
		{"grep HEAVY_OK=1 README.md && npx tsc --noEmit", true},
		{"HEAVY_OK=1 true; npx vitest run", true},
	}
	for _, c := range cases {
		if got := verdict(c.command) != ""; got != c.blocked {
			t.Errorf("verdict(%q) blocked=%v, want %v", c.command, got, c.blocked)
		}
	}
}

func TestShellSyntaxDoesNotHideRuns(t *testing.T) {
	cases := []struct {
		command string
		blocked bool
	}{
		{"(npx vitest run)", true},
		{"{ npx vitest run; }", true},
		{"! npx vitest run", true},
		{"if true; then npx vitest run; fi", true},
		{"for f in a; do npx tsc --noEmit; done", true},
		{"if false; then :; else npx vitest run; fi", true},
		{"echo x & npx vitest run", true},
		{"timeout 60 npx vitest run", true},
		{"nice -n 5 npx vitest run", true},
		{"env -i npx vitest run", true},
		{"xargs npx vitest run", true},
		{`bash -c "npx vitest run"`, true},
		{`sh -lc 'npx tsc --noEmit'`, true},
		{"echo $(npx vitest run)", true},
		{"echo `npx vitest run`", true},
		{"(npx vitest run a.test.ts --maxWorkers=1)", false},
		{`bash -c "echo vitest"`, false},
		{"echo '$(npx vitest run)'", false},
		{"if git diff --quiet; then echo clean; fi", false},
		{"sleep 1 & wait", false},
		{`ssh host "bash -c 'npx vitest run'"`, false},
	}
	for _, c := range cases {
		if got := verdict(c.command) != ""; got != c.blocked {
			t.Errorf("verdict(%q) blocked=%v, want %v", c.command, got, c.blocked)
		}
	}
}
