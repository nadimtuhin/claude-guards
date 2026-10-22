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
