# Verification

Don't run browser or end-to-end checks (Playwright, driving the TUI in tmux, starting `bx serve` with live Claude sessions) unless the user asks for them. The user verifies those by hand.

Verification stops at `go vet ./...`, `go test ./...`, the linters and code review. When a change has something worth checking by hand, list it in the final summary instead.
