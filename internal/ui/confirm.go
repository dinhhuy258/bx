package ui

import (
	"fmt"
	"strings"
)

// ConfirmBox draws a y/n confirmation in w x h, a rounded box like the Claude sync one: a
// bold title, the lines (as many as fit, the last replaced by "+N more" when cut), and the
// keys, `y <yes>` and `n / esc cancel`.
func ConfirmBox(title string, lines []string, yes string, w, h int) string {
	pal := newPalette(false)
	inner := min(max(w-6, 20), 60)
	avail := max(h-6, 1)
	if len(lines) > avail {
		cut := len(lines) - (avail - 1)
		lines = append(lines[:avail-1:avail-1], fmt.Sprintf("+%d more", cut))
	}
	row := func(text string) string {
		text = truncate(text, inner-2)
		return pal.dim.Render("│") + " " + text + strings.Repeat(" ", max(inner-2-runeWidth(text), 0)) + " " + pal.dim.Render("│")
	}
	out := []string{pal.dim.Render("╭" + strings.Repeat("─", inner) + "╮")}
	out = append(out, row(pal.bold.Render(title)), row(""))
	for _, l := range lines {
		out = append(out, row("  "+l))
	}
	out = append(out, row(""), row("  "+pal.sel.Render(" y "+yes+" ")+"   "+pal.dim.Render("n / esc cancel")))
	out = append(out, pal.dim.Render("╰"+strings.Repeat("─", inner)+"╯"))
	for i := range out {
		out[i] = truncate("  "+out[i], w)
	}
	for len(out) < h {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}
