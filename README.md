# claude-guards

Claude Code `PreToolUse` hooks that stop parallel agents from running test suites and builds at
the same time and overloading a laptop.

- `load-guard`: blocks a heavy command (build, test, typecheck, install) when too many heavy jobs
  are already running, or when the 5-minute load is high and CPU or memory has no spare room.
  Requires a vitest worker cap no higher than `LOAD_GUARD_MAX_WORKERS`. Reaps orphaned test workers.
- `heavy-run-guard`: blocks whole-suite `vitest`/`jest`/`tsc`/bare `pnpm test` runs; asks for
  specific test files and a worker cap.

Only the program a command actually executes counts. `echo "vitest"`, quoted text, heredoc bodies
and `grep vitest` are not runs. A running tree counts as a heavy job only while its CPU is above
the busy threshold.

## Install

```sh
git clone <this repo> claude-guards && cd claude-guards
./install.sh
```

`install.sh` builds the hooks into `~/.claude/hooks` (override with `CLAUDE_GUARDS_DIR`) and prints
the snippet below. It does not touch your settings. Needs Go 1.26+. `make dist` cross-builds for
darwin/linux on amd64/arm64.

## settings.json

```json
"PreToolUse": [
  {
    "matcher": "Bash",
    "hooks": [
      { "type": "command", "command": "~/.claude/hooks/load-guard" },
      { "type": "command", "command": "~/.claude/hooks/heavy-run-guard" }
    ]
  }
]
```

Use absolute paths if your shell does not expand `~`. A blocked command exits 2 with the reason on
stderr. Override one command with `LOAD_GUARD=0 <command>` or `HEAVY_OK=1 <command>`.

## Config

Environment variables, read on every hook call.

| Variable | Default | Meaning |
|---|---|---|
| `LOAD_GUARD_MAX_CONCURRENT` | `2` | Heavy job trees allowed at once |
| `LOAD_GUARD_BUSY_CPU` | `5` | Percent CPU a runner tree must exceed to count as running |
| `LOAD_GUARD_MAX_WORKERS` | `2` | Largest vitest worker cap accepted |
| `LOAD_GUARD_MAX` | `1.5 x cores` | 5-minute load above which capacity is checked |
| `LOAD_GUARD_CPU_MAX` | `85` | Machine CPU percent considered spare capacity |
| `LOAD_GUARD_MEM_PRESSURE_MAX` | `2` | macOS memory pressure level considered spare (ignored on Linux) |
| `LOAD_GUARD_REAP_AGE` | `600` | Seconds before an orphaned runner is reaped |
| `LOAD_GUARD_REAP` | on | Set `0` to disable reaping |

Bad or non-positive values fall back to the default.

## Test

```sh
make test
```

## License

MIT
