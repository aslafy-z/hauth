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
// an answer is outstanding, so the session answers them itself.
//
// The session must also be the ONLY responder: an attached kiosk terminal
// (xterm.js) auto-answers the same queries — and re-answers them on every
// reconnect when the ring buffer is replayed — and surplus reports leak
// into line-reading prompts as typed text (gh: `";48R8R" is not a valid
// answer`). The queries are therefore stripped from the stream delivered
// to clients; they carry no visual content.
var termQueryRe = regexp.MustCompile(`\x1b\]1[01];\?(?:\x07|\x1b\\)|\x1b\[6n`)

// termQueries are the literal byte forms termQueryRe can match; a suffix of
// delivered output that is a proper prefix of one of these may be a query
// split across reads and is withheld until the next chunk decides.
var termQueries = [][]byte{
	[]byte("\x1b]10;?\x07"),
	[]byte("\x1b]10;?\x1b\\"),
	[]byte("\x1b]11;?\x07"),
	[]byte("\x1b]11;?\x1b\\"),
	[]byte("\x1b[6n"),
}

// stripTermQueries answers every terminal status query in buf and removes
// it. It returns the canned responses, the cleaned output to deliver, and
// the tail withheld as a possible split query (at most a few bytes).
func stripTermQueries(buf []byte, rows, cols uint16) (resp, out, carry []byte) {
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
		buf = append(buf[:m[0]:m[0]], buf[m[1]:]...)
	}
	for l := min(len(buf), 7); l > 0; l-- {
		tail := buf[len(buf)-l:]
		for _, q := range termQueries {
			if bytes.HasPrefix(q, tail) {
				return resp, buf[:len(buf)-l], tail
			}
		}
	}
	return resp, buf, nil
}

// interceptTermQueries answers and removes terminal queries from a raw pty
// chunk, returning what should flow on to redaction, the ring buffer, and
// detectors.
func (s *Session) interceptTermQueries(p []byte) []byte {
	s.mu.Lock()
	rows, cols := s.rows, s.cols
	s.mu.Unlock()
	buf := append(s.qryBuf, p...)
	resp, out, carry := stripTermQueries(buf, rows, cols)
	s.qryBuf = append(s.qryBuf[:0], carry...)
	if len(resp) > 0 {
		_, _ = s.ptmx.Write(resp)
	}
	return out
}
