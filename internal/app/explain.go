package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/state"
)

// Explain answers prompt with claude -p in the story's folder (where its sessions run, so
// --resume finds them). With num it works on a copy of that session's conversation, in the
// model the session uses; with 0, on none. The copy is not saved and can only read files.
func (a *App) Explain(ctx context.Context, p *state.Project, story string, num int, prompt string) (string, error) {
	uuid, model := "", ""
	if num != 0 {
		s := p.Sessions[num]
		if s == nil || s.Story != story {
			return "", fmt.Errorf("no session [%d] in %s", num, story)
		}
		uuid, model = s.UUID, s.Model
	}
	dir := a.Dir(p, story)
	env, err := a.accountEnv(ctx, p.Name, dir)
	if err != nil {
		return "", err
	}
	out, err := a.Claude(ctx, dir, env, prompt, claude.ExplainArgs(uuid, model)...)
	if err != nil {
		return "", err
	}
	return claude.ExplainResult(out)
}

func runClaude(ctx context.Context, dir string, env []string, stdin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && len(out) == 0 { // a JSON reply on failure still says why
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("claude: %s", msg)
	}
	return out, nil
}
