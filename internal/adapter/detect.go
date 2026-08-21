package adapter

import "regexp"

// EventKind classifies something a detector recognized in CLI output.
type EventKind string

const (
	// EventURL: the CLI produced a URL the user must open.
	EventURL EventKind = "url"
	// EventCode: the CLI displayed a one-time code the user must enter on
	// the provider's page (device-flow user code).
	EventCode EventKind = "code"
	// EventPrompt: the CLI is waiting for input (paste a code, a token...).
	EventPrompt EventKind = "prompt"
	// EventSuccess: the CLI reported a completed login.
	EventSuccess EventKind = "success"
	// EventFailure: the CLI reported a failed login.
	EventFailure EventKind = "failure"
)

// Event is one detection produced from a line of CLI output.
type Event struct {
	Kind   EventKind
	URL    string
	Code   string
	Prompt string
}

// Detector consumes CLI output line by line and emits events. Detectors are
// adapter-owned and match the CLI's actual prompt text as recorded in
// testdata/, not a global regex across the whole buffer. A detector is used
// by a single session and may keep state.
type Detector interface {
	Feed(line string) []Event
}

// Rule matches one line pattern and produces an event. Submatch names "url"
// and "code" populate the corresponding Event fields.
type Rule struct {
	Pattern *regexp.Regexp
	Kind    EventKind
	// Once suppresses repeat emissions for spinner-redrawn lines.
	Once bool
}

// LineDetector applies ordered rules to each line. A generic URL fallback is
// appended unless disabled; the BROWSER shim remains the primary URL source.
type LineDetector struct {
	Rules         []Rule
	NoURLFallback bool

	fired map[int]bool
}

var genericURL = regexp.MustCompile(`(?P<url>https://[^\s'"\)\]]+)`)

// NewLineDetector builds a LineDetector from rules.
func NewLineDetector(rules ...Rule) *LineDetector {
	return &LineDetector{Rules: rules, fired: map[int]bool{}}
}

func (d *LineDetector) Feed(line string) []Event {
	if d.fired == nil {
		d.fired = map[int]bool{}
	}
	var events []Event
	matched := false
	for i, r := range d.Rules {
		m := r.Pattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		matched = true
		if r.Once && d.fired[i] {
			continue
		}
		d.fired[i] = true
		ev := Event{Kind: r.Kind, Prompt: line}
		for j, name := range r.Pattern.SubexpNames() {
			switch name {
			case "url":
				ev.URL = m[j]
			case "code":
				ev.Code = m[j]
			}
		}
		events = append(events, ev)
	}
	if !matched && !d.NoURLFallback {
		if m := genericURL.FindStringSubmatch(line); m != nil && !d.fired[-1] {
			d.fired[-1] = true
			events = append(events, Event{Kind: EventURL, URL: m[1], Prompt: line})
		}
	}
	return events
}
