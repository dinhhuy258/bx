package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/dinhhuy258/bx/internal/cli"
)

func init() {
	cli.Register(cli.Command{Name: "statusline", Help: "internal: Claude Code status line of a bx session (model and usage)", Run: runStatusline})
}

// statuslineInput is the part of Claude Code's status line input the line shows. A nil
// percentage was not sent: rate_limits comes only with a subscription, and each window only
// after the first reply.
type statuslineInput struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	ContextWindow struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
	RateLimits struct {
		FiveHour *rateLimit `json:"five_hour"`
		SevenDay *rateLimit `json:"seven_day"`
	} `json:"rate_limits"`
}

type rateLimit struct {
	UsedPercentage *float64 `json:"used_percentage"`
}

// runStatusline prints the line Claude shows under its input box. Anything it cannot read
// prints nothing, so a broken input leaves the line blank and never shows an error.
func runStatusline(_ []string, out, _ io.Writer) int {
	_, _ = fmt.Fprintln(out, statusline(os.Stdin))
	return 0
}

// statusline is "Opus 5.5 · ctx 34% · 5h 23% · 7d 41%", leaving out each part not sent. It
// stays under 40 columns, so it fits a phone's terminal without cutting.
func statusline(in io.Reader) string {
	var s statuslineInput
	if err := json.NewDecoder(in).Decode(&s); err != nil {
		return ""
	}
	var parts []string
	if name := strings.TrimSpace(s.Model.DisplayName); name != "" {
		parts = append(parts, name)
	}
	pct := func(label string, p *float64) {
		if p != nil {
			parts = append(parts, fmt.Sprintf("%s %d%%", label, int(math.Round(*p))))
		}
	}
	pct("ctx", s.ContextWindow.UsedPercentage)
	if r := s.RateLimits.FiveHour; r != nil {
		pct("5h", r.UsedPercentage)
	}
	if r := s.RateLimits.SevenDay; r != nil {
		pct("7d", r.UsedPercentage)
	}
	return strings.Join(parts, " · ")
}
