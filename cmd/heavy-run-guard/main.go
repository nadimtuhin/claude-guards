package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

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

func main() {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return
	}
	reason := verdict(commandOf(input))
	if reason == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "heavy-run-guard: %s. Keeps the Mac cool. Override: HEAVY_OK=1 <command>\n", reason)
	os.Exit(2)
}
