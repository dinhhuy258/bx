package claude

import (
	"context"
	"os/exec"
	"time"
)

// Lister returns the live agents. The real one runs the CLI; tests use a fake.
type Lister interface {
	Agents(ctx context.Context) ([]Agent, error)
}

// CLI runs `claude agents --json --all`. Bin defaults to "claude" on PATH.
type CLI struct {
	Bin     string
	Timeout time.Duration
}

// Agents runs the CLI and decodes its output.
func (c CLI) Agents(ctx context.Context) ([]Agent, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "agents", "--json", "--all").Output()
	if err != nil {
		return nil, err
	}
	return DecodeAgents(out)
}
