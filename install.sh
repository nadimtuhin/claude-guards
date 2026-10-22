#!/bin/sh
# Builds the hooks into $CLAUDE_GUARDS_DIR (default ~/.claude/hooks) and prints the settings.json
# snippet. It never edits settings.json.
set -eu
dir="${CLAUDE_GUARDS_DIR:-$HOME/.claude/hooks}"
here=$(cd "$(dirname "$0")" && pwd)
command -v go >/dev/null || { echo "install.sh: Go 1.26+ is required" >&2; exit 1; }
mkdir -p "$dir"
for hook in load-guard heavy-run-guard; do
  (cd "$here" && go build -o "$dir/$hook" "./cmd/$hook")
  echo "built $dir/$hook"
done
cat <<EOF

Add this to the "hooks" object in ~/.claude/settings.json (merge with any existing PreToolUse list):

"PreToolUse": [
  {
    "matcher": "Bash",
    "hooks": [
      { "type": "command", "command": "$dir/load-guard" },
      { "type": "command", "command": "$dir/heavy-run-guard" }
    ]
  }
]
EOF
