package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Tone is how a dialog looks: a question, a question that loses something, or a form.
type Tone int

const (
	ToneConfirm Tone = iota // blue frame
	ToneDanger              // red frame; Enter picks Cancel
	ToneForm                // blue frame with fields
)

// fieldRows is how many lines a long text field wraps onto before it scrolls.
const fieldRows = 4

// Field is one input of a form dialog: a text (Value) or a choice (Choices, Choice).
type Field struct {
	Label   string
	Value   string
	Back    int // the cursor: runes of Value after it, 0 = at the end
	Choices []string
	Choice  int
}

// Button is one key of a dialog's footer: "y  Stop". Default marks the one Enter picks.
type Button struct {
	Key, Label string
	Default    bool
	Danger     bool // red: the action of a danger dialog
}

// Dialog is a framed box drawn over the faded screen: a title in the top border, body lines,
// fields, a notice, then a hint and the buttons.
type Dialog struct {
	Title   string
	Tone    Tone
	Body    []string
	Fields  []Field
	Focus   int      // the field with the cursor
	After   []string // lines under the fields: what the form will make ("Branch fix/x")
	Mark    int      // 1 + the index of the After line drawn selected, across the box; 0 none
	Note    string   // amber, under those: what Enter will do differently ("branch exists…")
	Hint    string   // dim, left of the buttons: the form's keys, or what it is busy with
	Buttons []Button
}

// dialog colours, the list's palette plus the panel and the buttons
var (
	dlgPanel   = "\x1b[48;2;31;35;53m" // #1f2335
	dlgInput   = "\x1b[48;2;42;47;69m" // #2a2f45
	dlgMuted   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8089b3"))
	dlgOnColor = lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1b26")).Bold(true)
)

// Render draws the box for a screen of w x h, as lines of the same width. It is at most 72
// wide and at most w-2, and cuts the body with "+N more" when it does not fit in h.
func (d Dialog) Render(w, h int, plain bool) string {
	pal := newPalette(plain)
	frame, title := pal.blue, pal.bold.Inherit(pal.blue)
	if d.Tone == ToneDanger {
		frame, title = pal.red, pal.bold.Inherit(pal.red)
	}
	muted, panel, input := dlgMuted, dlgPanel, dlgInput
	if plain {
		muted, panel, input = lipgloss.NewStyle(), "", ""
	}

	// the inner width: what the content needs, within 40..66 and the screen
	need := lipgloss.Width(d.Title) + 4
	for _, l := range append(append([]string(nil), d.Body...), d.After...) {
		need = max(need, lipgloss.Width(l))
	}
	need = max(need, lipgloss.Width(d.Note), lipgloss.Width(d.Hint)+2+d.buttonsWidth())
	for _, f := range d.Fields {
		need = max(need, lipgloss.Width(f.Value)+3, 8+choicesWidth(f.Choices))
	}
	iw := min(max(need, 40), 66, w-8)
	bw := iw + 6

	row := func(s string) string {
		s = truncate(s, iw)
		return frame.Render("│") + band("  "+s+strings.Repeat(" ", max(iw-lipgloss.Width(s), 0))+"  ", 0, panel) + frame.Render("│")
	}
	var mid []string
	body := d.Body
	// the frame, the title's blank line and the footer take 6 rows; fields 3 each, and a long
	// value wraps onto up to fieldRows lines
	room := max(h-8-3*len(d.Fields), 1)
	for _, f := range d.Fields {
		if len(f.Choices) == 0 {
			n, _, _ := fieldLines(f.Value, f.Back, iw-2, fieldRows, func(rune) string { return " " })
			room = max(room-(len(n)-1), 1)
		}
	}
	if len(body) > room {
		cut := len(body) - (room - 1)
		body = append(body[:room-1:room-1], fmt.Sprintf("+%d more", cut))
	}
	mid = append(mid, row(""))
	for _, l := range body {
		mid = append(mid, row(l))
	}
	if len(body) > 0 && len(d.Fields) > 0 {
		mid = append(mid, row(""))
	}
	for i, f := range d.Fields {
		focused := i == d.Focus
		label := muted.Render(f.Label)
		if focused {
			label = pal.bold.Inherit(pal.blue).Render(f.Label)
		}
		if len(f.Choices) > 0 {
			mid = append(mid, row(choiceLine(label, f, focused, pal, plain)), row(""))
			continue
		}
		// the cursor: plain draws ▏ before its rune; else the rune (a space at the end) reversed
		cursor := func(c rune) string {
			switch {
			case !focused && c == 0:
				return ""
			case !focused:
				return string(c)
			case plain && c == 0:
				return "▏"
			case plain:
				return "▏" + string(c)
			case c == 0:
				c = ' '
			}
			return "\x1b[7m" + string(c) + "\x1b[m" + input
		}
		lines, above, below := fieldLines(f.Value, f.Back, iw-2, fieldRows, cursor)
		switch {
		case above && below:
			label += muted.Render("  ↑↓ more")
		case above:
			label += muted.Render("  ↑ more")
		case below:
			label += muted.Render("  ↓ more")
		}
		mid = append(mid, row(label))
		for _, l := range lines {
			in := " " + l
			in += strings.Repeat(" ", max(iw-lipgloss.Width(in), 0))
			mid = append(mid, frame.Render("│")+band("  ", 0, panel)+band(in, 0, input)+band("  ", 0, panel)+frame.Render("│"))
		}
		mid = append(mid, row(""))
	}
	for i, l := range d.After {
		if i+1 == d.Mark && !plain {
			l = truncate(l, iw)
			mid = append(mid, frame.Render("│")+band("  ", 0, panel)+band(l, iw, pal.selBG)+band("  ", 0, panel)+frame.Render("│"))
			continue
		}
		mid = append(mid, row(l))
	}
	if len(d.After) > 0 {
		mid = append(mid, row(""))
	}
	if d.Note != "" {
		if len(d.Fields) == 0 {
			mid = append(mid, row(""))
		}
		mid = append(mid, row(pal.amber.Render(d.Note)))
	}
	if len(d.Fields) == 0 || d.Note != "" {
		mid = append(mid, row(""))
	}
	buttons := d.buttons(pal, plain)
	hint := truncate(pal.dim.Render(d.Hint), max(iw-lipgloss.Width(buttons)-2, 0))
	mid = append(mid, row(hint+strings.Repeat(" ", max(iw-lipgloss.Width(hint)-lipgloss.Width(buttons), 1))+buttons), row(""))

	top := frame.Render("╭─ ") + title.Render(truncate(d.Title, bw-6)) + " "
	top += frame.Render(strings.Repeat("─", max(bw-1-lipgloss.Width(top), 0)) + "╮")
	out := append([]string{top}, mid...)
	out = append(out, frame.Render("╰"+strings.Repeat("─", bw-2)+"╯"))
	return strings.Join(out, "\n")
}

func (d Dialog) buttonsWidth() int {
	n := 0
	for i, b := range d.Buttons {
		if i > 0 {
			n += 2
		}
		n += lipgloss.Width(b.Key) + lipgloss.Width(b.Label) + 4
	}
	return n
}

// buttons is the footer's keys, right to left in importance: " n  Cancel   y  Stop ".
func (d Dialog) buttons(pal palette, plain bool) string {
	var parts []string
	for _, b := range d.Buttons {
		text := " " + b.Key + "  " + b.Label + " "
		switch {
		case plain && b.Default:
			text = "[" + strings.TrimSpace(text) + "]"
		case plain:
		case b.Danger:
			text = band(dlgOnColor.Render(text), 0, "\x1b[48;2;247;118;142m") // #f7768e
		case b.Default:
			text = band(dlgOnColor.Render(text), 0, "\x1b[48;2;122;162;247m") // #7aa2f7
		default:
			text = band(dlgMuted.Render(text), 0, pal.selBG)
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "  ")
}

func choicesWidth(c []string) int {
	n := 0
	for _, s := range c {
		n += lipgloss.Width(s) + 2
	}
	return n
}

// choiceLine is "Type    feat  fix  chore", the chosen one on a band: blue while the field has
// the cursor, grey when not.
func choiceLine(label string, f Field, focused bool, pal palette, plain bool) string {
	line := label + strings.Repeat(" ", max(8-lipgloss.Width(label), 1)-1)
	for i, c := range f.Choices {
		text := " " + c + " "
		switch {
		case i != f.Choice:
			text = pal.dim.Render(text)
		case plain:
			text = "[" + c + "]"
		case focused:
			text = band(dlgOnColor.Render(text), 0, "\x1b[48;2;122;162;247m")
		default:
			text = band(pal.bold.Render(text), 0, pal.selBG)
		}
		line += text
	}
	return line
}

// Fade draws s faint: the screen behind a dialog, the list while Claude has the keys, a shell
// that ended. It renews the faint after every reset inside, so it holds across s's own styling.
func Fade(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "\x1b[2m" + strings.ReplaceAll(l, "\x1b[m", "\x1b[m\x1b[2m") + "\x1b[m"
	}
	return strings.Join(lines, "\n")
}

// Overlay fades the first h lines of screen, w wide, and draws box centred on them. Lines
// from h on (the key bar) stay as they are.
func Overlay(screen, box string, w, h int) string {
	lines := strings.Split(screen, "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	head := strings.Split(Fade(strings.Join(lines[:h], "\n")), "\n")
	rows := strings.Split(box, "\n")
	bw := 0
	for _, r := range rows {
		bw = max(bw, lipgloss.Width(r))
	}
	x, y := max((w-bw)/2, 0), max((h-len(rows))/2, 0)
	for i, r := range rows {
		if y+i >= h {
			break
		}
		l := head[y+i]
		left := ansi.Truncate(l, x, "")
		left += strings.Repeat(" ", max(x-lipgloss.Width(left), 0))
		right := ansi.TruncateLeft(l, x+bw, "")
		head[y+i] = left + "\x1b[m" + r + "\x1b[m\x1b[2m" + right + "\x1b[m"
	}
	return strings.Join(append(head, lines[h:]...), "\n")
}
