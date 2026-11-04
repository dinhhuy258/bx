package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ExplainTools are the only tools an explanation has: it reads, it never edits or runs.
// --tools removes the rest (subagents included); --allowedTools would only pre-approve.
const ExplainTools = "Read,Grep,Glob"

// ExplainArgs is the command that answers one question, prompt on stdin. With uuid it works
// on a copy of that session's conversation, which is not saved; without, on no conversation.
// Flags that take a list get one "=" value so they never swallow a later argument.
func ExplainArgs(uuid, model string) []string {
	args := []string{"claude", "-p"}
	if uuid != "" {
		args = append(args, "--resume", uuid, "--fork-session")
	}
	args = append(args, "--no-session-persistence", "--tools="+ExplainTools, "--strict-mcp-config", "--output-format", "json")
	if model != "" {
		args = append(args, "--model", model)
	}
	return args
}

// ExplainResult reads the JSON reply of `claude -p --output-format json`: the answer, or an
// error when Claude says it failed or gave nothing.
func ExplainResult(out []byte) (string, error) {
	var r struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", fmt.Errorf("claude gave no answer: %w", err)
	}
	answer := strings.TrimSpace(r.Result)
	switch {
	case r.IsError && answer != "":
		return "", errors.New(answer)
	case r.IsError, answer == "":
		return "", errors.New("claude gave no answer")
	}
	return answer, nil
}
