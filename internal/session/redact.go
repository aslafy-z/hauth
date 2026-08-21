package session

import (
	"bytes"
	"regexp"
)

const redactedMark = "[REDACTED]"

// secretRes match credential shapes we know can appear in login output.
// sk-ant- is listed before the generic sk- pattern only for clarity; both
// replace with the same marker.
var secretRes = []*regexp.Regexp{
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{10,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._+/=-]{8,}`),
}

// Redact replaces recognized secrets in p. Safe on arbitrary byte streams.
func Redact(p []byte) []byte {
	for _, re := range secretRes {
		p = re.ReplaceAll(p, []byte(redactedMark))
	}
	return p
}

// secretMarkers are the prefixes that can start a secret. A stream chunk
// ending in the middle of one must be held back until enough bytes arrive
// to decide, otherwise a token split across two reads escapes redaction.
var secretMarkers = [][]byte{
	[]byte("ghp_"), []byte("gho_"), []byte("ghu_"), []byte("ghs_"), []byte("ghr_"),
	[]byte("sk-"), []byte("eyJ"), []byte("Bearer "), []byte("bearer "),
}

const holdCap = 8192

func isTokenByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '_' || b == '-' || b == '.' || b == '+' || b == '/' || b == '=':
		return true
	}
	return false
}

// holdStart returns the index from which b must be withheld because its
// suffix could be the beginning of a secret (a marker followed only by
// token bytes, or a partial marker cut by the chunk boundary), or -1.
func holdStart(b []byte) int {
	best := -1
	for _, m := range secretMarkers {
		if idx := bytes.LastIndex(b, m); idx > best {
			ok := true
			for _, c := range b[idx+len(m):] {
				if !isTokenByte(c) {
					ok = false
					break
				}
			}
			if ok {
				best = idx
			}
		}
		for k := min(len(m)-1, len(b)); k >= 1; k-- {
			if bytes.HasSuffix(b, m[:k]) {
				if cand := len(b) - k; cand > best {
					best = cand
				}
				break
			}
		}
	}
	if best >= 0 && len(b)-best >= holdCap {
		return -1 // give up holding unreasonably long runs
	}
	return best
}

// StreamRedactor redacts a byte stream incrementally, holding back a tail
// that could be the start of a secret until the next write or Flush.
type StreamRedactor struct {
	held []byte
}

func NewStreamRedactor() *StreamRedactor { return &StreamRedactor{} }

// Write returns the redacted bytes that are safe to emit now.
func (r *StreamRedactor) Write(p []byte) []byte {
	combined := append(r.held, p...)
	start := holdStart(combined)
	if start < 0 {
		r.held = nil
		return Redact(combined)
	}
	r.held = append([]byte(nil), combined[start:]...)
	return Redact(combined[:start])
}

// Flush emits whatever is still held, redacted.
func (r *StreamRedactor) Flush() []byte {
	out := Redact(r.held)
	r.held = nil
	return out
}
