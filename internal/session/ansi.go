package session

import (
	"regexp"
	"strings"
)

// ansiRe covers CSI sequences (with intermediates), OSC strings terminated
// by BEL or ST, and single-character escapes. Sequences split across read
// chunks are not reassembled; detectors tolerate the resulting noise.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-_]`)

// hMoveRe matches CSI sequences that move the cursor horizontally: CUF (C),
// CUB (D), CHA (G), HPA (`). Ink-based TUIs (Claude Code 2.x) lay words out
// with these instead of literal spaces, so they must become whitespace
// rather than vanish or multi-word patterns never match.
var hMoveRe = regexp.MustCompile("\x1b\\[[0-9;]*[CDG`]")

// StripANSI removes escape sequences and control characters (except tab)
// from a line before it reaches a detector. Horizontal cursor moves are
// replaced by a space to preserve word boundaries.
func StripANSI(s string) string {
	s = hMoveRe.ReplaceAllString(s, " ")
	s = ansiRe.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		if r == 0x7f {
			return -1
		}
		return r
	}, s)
}
