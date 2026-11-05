package ui

import (
	"fmt"
	"strings"
)

// prBadgeText is a story's PR segment: the PR (or how many), then CI, threads waiting for me,
// the review state and the merge state. "" when the story has no PR.
func prBadgeText(b PRBadge, pal palette) string {
	return strings.Join(prBadgeParts(b, pal), " ")
}

// prBadgeShort is the first two parts of the badge, for a narrow screen.
func prBadgeShort(b PRBadge, pal palette) string {
	parts := prBadgeParts(b, pal)
	return strings.Join(parts[:min(len(parts), 2)], " ")
}

func prBadgeParts(b PRBadge, pal palette) []string {
	if b.Count == 0 {
		return nil
	}
	done := b.Merged + b.Closed
	if done == b.Count { // every PR is finished
		switch {
		case b.Count == 1 && b.Merged == 1:
			return []string{pal.green.Render(fmt.Sprintf("merged #%d", b.Number))}
		case b.Count == 1:
			return []string{pal.dim.Render(fmt.Sprintf("closed #%d", b.Number))}
		}
		if b.Merged == 0 {
			return []string{pal.dim.Render(fmt.Sprintf("closed %d/%d", b.Closed, b.Count))}
		}
		return []string{pal.green.Render(fmt.Sprintf("merged %d/%d", b.Merged, b.Count))}
	}
	parts := []string{pal.blue.Render(fmt.Sprintf("#%d", b.Number))}
	if b.Count > 1 {
		parts = []string{pal.blue.Render(fmt.Sprintf("%d PRs", b.Count))}
		if b.Merged > 0 {
			parts = append(parts, pal.green.Render(fmt.Sprintf("merged %d/%d", b.Merged, b.Count)))
		}
	}
	switch {
	case b.Fail:
		parts = append(parts, pal.red.Render("✗ ci"))
	case b.Pending:
		parts = append(parts, pal.amber.Render("◌ ci"))
	case b.Ready:
		parts = append(parts, pal.green.Render("✓ ready"))
	case b.Pass:
		parts = append(parts, pal.green.Render("✓ ci"))
	}
	if b.Threads > 0 {
		parts = append(parts, pal.amber.Render(fmt.Sprintf("💬 %d", b.Threads)))
	}
	switch {
	case b.Changes:
		parts = append(parts, pal.red.Render("changes"))
	case b.Review:
		parts = append(parts, pal.dim.Render("review"))
	}
	switch {
	case b.Conflict:
		parts = append(parts, pal.red.Render("⇄ conflict"))
	case b.Behind:
		parts = append(parts, pal.dim.Render("⇄"))
	}
	return parts
}
