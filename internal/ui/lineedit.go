package ui

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

// editLine applies a line-editing key to text, whose cursor sits back runes from its end, the
// way a shell does: ←→ (C-b C-f) move, M-←→ (M-b M-f) jump words, C-a/Home and C-e/End go to
// the ends, BSpace and Delete (C-d) delete a rune, C-w the word before the cursor up to a
// space, M-BSpace the word before it up to punctuation, C-u to the start and C-k to the end.
// Typed text and Space insert at the cursor. ok is false for keys that are not edits.
func editLine(text string, back int, key, typed string) (string, int, bool) {
	r := []rune(text)
	at := len(r) - min(max(back, 0), len(r))
	cut := func(from, to int) string { return string(r[:from]) + string(r[to:]) }
	switch key {
	case "Left", "C-b":
		at = max(at-1, 0)
	case "Right", "C-f":
		at = min(at+1, len(r))
	case "M-Left", "M-b":
		at = wordStart(r, at, isWordRune)
	case "M-Right", "M-f":
		at = wordEnd(r, at)
	case "C-a", "Home":
		at = 0
	case "C-e", "End":
		at = len(r)
	case "BSpace", "C-h":
		if at > 0 {
			text, at = cut(at-1, at), at-1
		}
	case "Delete", "C-d":
		if at < len(r) {
			text = cut(at, at+1)
		}
	case "C-w":
		from := wordStart(r, at, func(c rune) bool { return !unicode.IsSpace(c) })
		text, at = cut(from, at), from
	case "M-BSpace":
		from := wordStart(r, at, isWordRune)
		text, at = cut(from, at), from
	case "C-u":
		text, at = string(r[at:]), 0
	case "C-k":
		text = string(r[:at])
	case "Space":
		typed = " "
		fallthrough
	default:
		if typed == "" {
			return text, back, false
		}
		text = string(r[:at]) + typed + string(r[at:])
		at += len([]rune(typed))
	}
	return text, len([]rune(text)) - at, true
}

func isWordRune(c rune) bool { return unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' }

// wordStart is where the word before at starts: back over runes that are not in the word, then
// over the word.
func wordStart(r []rune, at int, in func(rune) bool) int {
	for at > 0 && !in(r[at-1]) {
		at--
	}
	for at > 0 && in(r[at-1]) {
		at--
	}
	return at
}

// wordEnd is where the word at or after at ends.
func wordEnd(r []rune, at int) int {
	for at < len(r) && !isWordRune(r[at]) {
		at++
	}
	for at < len(r) && isWordRune(r[at]) {
		at++
	}
	return at
}

// fieldLines is a text field's value wrapped to w columns, at most rows lines, with the cursor
// (back runes from the end) drawn by cursor. The lines follow the cursor when the value is
// longer; above and below say whether lines were left out there.
func fieldLines(value string, back, w, rows int, cursor func(rune) string) (lines []string, above, below bool) {
	r := []rune(value)
	at := len(r) - min(max(back, 0), len(r))
	w = max(w, 4)

	// wrap: break after the last space that fits, else where the line is full; a cursor at the
	// very end needs a column, so a full last line gets an empty one after it
	type span struct{ from, to int }
	var spans []span
	for from := 0; ; {
		width, to, space := 0, from, -1
		for to < len(r) {
			cw := lipgloss.Width(string(r[to]))
			if width+cw > w {
				break
			}
			if r[to] == ' ' {
				space = to + 1
			}
			width += cw
			to++
		}
		if to == len(r) && (width < w || len(r) == 0 || at < len(r)) {
			spans = append(spans, span{from, to})
			break
		}
		if space > from && to < len(r) {
			to = space
		}
		spans = append(spans, span{from, to})
		from = to
	}
	cur := len(spans) - 1
	for i, s := range spans {
		if at < s.to || at == s.to && i == len(spans)-1 {
			cur = i
			break
		}
	}
	first := max(min(cur-rows+1, len(spans)-rows), 0) // the cursor's line last, else as far down as fits
	last := min(first+rows, len(spans))
	for _, s := range spans[first:last] {
		var b strings.Builder
		for i := s.from; i < s.to; i++ {
			if i == at {
				b.WriteString(cursor(r[i]))
				continue
			}
			b.WriteRune(r[i])
		}
		if at == s.to && at == len(r) {
			b.WriteString(cursor(0))
		}
		lines = append(lines, b.String())
	}
	return lines, first > 0, last < len(spans)
}
