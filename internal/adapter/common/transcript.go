package common

import (
	"bytes"
	"strings"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/session"
)

// ReplayTranscript feeds a recorded terminal transcript through a detector
// the same way a live session would: split on CR and NL, ANSI-stripped,
// blank lines skipped. Used by adapter tests against testdata/.
func ReplayTranscript(det adapter.Detector, raw []byte) []adapter.Event {
	var events []adapter.Event
	for _, line := range bytes.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' }) {
		clean := session.StripANSI(string(line))
		if strings.TrimSpace(clean) == "" {
			continue
		}
		events = append(events, det.Feed(clean)...)
	}
	return events
}
