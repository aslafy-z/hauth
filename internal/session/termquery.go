package session

import (
	"bytes"
	"fmt"
	"regexp"
)

// TUI prompt libraries query the terminal before reading input: OSC 10/11
// for the color scheme and DSR (ESC[6n) for the cursor position, usually
// after jumping to the bottom-right corner to measure the screen. A real
// terminal emulator answers these automatically; a headless pty has nobody
// to answer, and some libraries (gh's prompter) never process input while
// an answer is outstanding. The session answers them itself so auto-replies
// and pasted codes work without a kiosk terminal attached. An attached
// terminal answering too is harmless: prompt libraries ignore reports they
// did not ask for.
var termQueryRe = regexp.MustCompile(`\x1b\]1[01];\?(?:\x07|\x1b\\)|\x1b\[6n`)

// termQueryResponses returns the canned answers for every terminal status
// query in buf, plus the tail to carry into the next scan (a possibly
// unterminated trailing escape sequence).
func termQueryResponses(buf []byte, rows, cols uint16) (resp, rest []byte) {
	for {
		m := termQueryRe.FindIndex(buf)
		if m == nil {
			break
		}
		q := buf[m[0]:m[1]]
		switch {
		case bytes.HasPrefix(q, []byte("\x1b]10")):
			resp = append(resp, "\x1b]10;rgb:ffff/ffff/ffff\x1b\\"...)
		case bytes.HasPrefix(q, []byte("\x1b]11")):
			resp = append(resp, "\x1b]11;rgb:0000/0000/0000\x1b\\"...)
		default: // cursor position; bottom-right doubles as the size probe
			resp = append(resp, fmt.Sprintf("\x1b[%d;%dR", rows, cols)...)
		}
		buf = append(buf[:m[0]], buf[m[1]:]...)
	}
	if idx := bytes.LastIndexByte(buf, 0x1b); idx >= 0 {
		rest = buf[idx:]
	}
	if len(rest) > 64 {
		rest = rest[len(rest)-64:]
	}
	return resp, rest
}

func (s *Session) answerTermQueries(p []byte) {
	s.mu.Lock()
	rows, cols := s.rows, s.cols
	s.mu.Unlock()
	resp, rest := termQueryResponses(append(s.qryBuf, p...), rows, cols)
	s.qryBuf = append(s.qryBuf[:0], rest...)
	if len(resp) > 0 {
		_, _ = s.ptmx.Write(resp)
	}
}
