package session

import (
	"regexp"
	"strings"
)

// ansiRe covers CSI sequences (with intermediates), OSC strings terminated
// by BEL or ST, and single-character escapes. Sequences split across read
// chunks are not reassembled; detectors tolerate the resulting noise.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-_]`)

// StripANSI removes escape sequences and control characters (except tab)
// from a line before it reaches a detector.
func StripANSI(s string) string {
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
