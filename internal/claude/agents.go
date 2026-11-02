// Package claude talks to the Claude Code CLI: it decodes `claude agents --json`
// and builds the start and resume command lines. It never reads pane text.
package claude

import (
	"encoding/json"
	"fmt"
	"strings"
)

// State is the three-way status bx shows.
type State int

const (
	Idle State = iota
	Running
	Waiting
)

func (s State) String() string {
	switch s {
	case Running:
		return "running"
	case Waiting:
		return "waiting"
	}
	return "idle"
}

// Agent is one row of `claude agents --json`. Unknown fields are ignored on purpose.
type Agent struct {
	PID        int    `json:"pid"`
	ID         string `json:"id"`
	Cwd        string `json:"cwd"`
	Kind       string `json:"kind"`
	StartedAt  int64  `json:"startedAt"` // Unix milliseconds in claude 2.1.x
	SessionID  string `json:"sessionId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	BgState    string `json:"state"`
	WaitingFor string `json:"waitingFor"`
}

// State maps the CLI status to bx's three states. Anything unknown is Running, never a crash.
func (a Agent) State() State {
	switch a.Status {
	case "waiting":
		return Waiting
	case "idle":
		return Idle
	case "busy":
		return Running
	}
	switch a.BgState {
	case "blocked":
		return Waiting
	case "done", "failed", "stopped":
		return Idle
	}
	return Running
}

// DecodeAgents parses the JSON array printed by `claude agents --json`.
func DecodeAgents(data []byte) ([]Agent, error) {
	var out []Agent
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode claude agents: %w", err)
	}
	return out, nil
}

// IndexByUUID keys agents by session id. A duplicate keeps the first and marks it in Dups.
func IndexByUUID(agents []Agent) map[string]Agent {
	m := make(map[string]Agent, len(agents))
	for _, a := range agents {
		if _, seen := m[a.SessionID]; !seen {
			m[a.SessionID] = a
		}
	}
	return m
}

// Duplicates returns the session ids that appear more than once.
func Duplicates(agents []Agent) map[string]bool {
	count := map[string]int{}
	for _, a := range agents {
		count[a.SessionID]++
	}
	d := map[string]bool{}
	for id, n := range count {
		if n > 1 {
			d[id] = true
		}
	}
	return d
}

// Options are the settings a session starts with. "" leaves the flag out: Claude's own default.
type Options struct {
	Model, Effort, PermissionMode string
	Settings                      string   // extra settings JSON for --settings, such as hooks
	DisallowedTools               []string // --disallowed-tools, comma-joined: one value, so it never swallows a later argument
	PluginDirs                    []string // --plugin-dir, one per folder
	AddDirs                       []string // --add-dir, one per folder
	AppendSystemPrompt            string   // --append-system-prompt
}

// StartArgs builds the command for a new or resumed session. Run it in the target folder.
// No -n: bx shows the title in its own pane header, and Claude would repeat it on the input box.
// A resume passes every flag again: --resume does not remember them.
func StartArgs(uuid string, resume bool, o Options) []string {
	args := []string{"claude", "--session-id", uuid}
	if resume {
		args = []string{"claude", "--resume", uuid}
	}
	for _, f := range [][2]string{{"--permission-mode", o.PermissionMode}, {"--model", o.Model}, {"--effort", o.Effort}, {"--settings", o.Settings}} {
		if f[1] != "" {
			args = append(args, f[0], f[1])
		}
	}
	if len(o.DisallowedTools) > 0 {
		args = append(args, "--disallowed-tools", strings.Join(o.DisallowedTools, ","))
	}
	for _, d := range o.PluginDirs {
		args = append(args, "--plugin-dir", d)
	}
	for _, d := range o.AddDirs {
		args = append(args, "--add-dir", d)
	}
	if o.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", o.AppendSystemPrompt)
	}
	return args
}
