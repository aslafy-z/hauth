package adapter

import (
	"regexp"
	"testing"
)

func TestLineDetectorSubmatchExtraction(t *testing.T) {
	d := NewLineDetector(
		Rule{Pattern: regexp.MustCompile(`code: (?P<code>[A-Z0-9-]+)`), Kind: EventCode},
		Rule{Pattern: regexp.MustCompile(`visit (?P<url>https://\S+)`), Kind: EventURL},
	)
	evs := d.Feed("visit https://example.com/activate and enter code: AB12-CD34")
	if len(evs) != 2 {
		t.Fatalf("events = %d, want 2", len(evs))
	}
	if evs[0].Code != "AB12-CD34" || evs[1].URL != "https://example.com/activate" {
		t.Fatalf("unexpected events %+v", evs)
	}
}

func TestLineDetectorOnce(t *testing.T) {
	d := NewLineDetector(
		Rule{Pattern: regexp.MustCompile(`Paste code`), Kind: EventPrompt, Once: true},
	)
	if evs := d.Feed("Paste code: "); len(evs) != 1 {
		t.Fatalf("first feed: %d events", len(evs))
	}
	// Provisional partial-line re-feeding must not duplicate the event.
	if evs := d.Feed("Paste code: "); len(evs) != 0 {
		t.Fatalf("second feed emitted %d events", len(evs))
	}
}

func TestLineDetectorGenericURLFallback(t *testing.T) {
	d := NewLineDetector()
	evs := d.Feed("see https://fallback.example.com/x?a=b for details")
	if len(evs) != 1 || evs[0].Kind != EventURL || evs[0].URL != "https://fallback.example.com/x?a=b" {
		t.Fatalf("unexpected events %+v", evs)
	}
	// The fallback fires once per detector; session-level dedupe handles
	// distinct URLs from explicit rules.
	if evs := d.Feed("also https://other.example.com"); len(evs) != 0 {
		t.Fatalf("fallback fired twice: %+v", evs)
	}
}

func TestLineDetectorRuleSuppressesFallback(t *testing.T) {
	d := NewLineDetector(
		Rule{Pattern: regexp.MustCompile(`Open (?P<url>https://\S+)`), Kind: EventURL, Once: true},
	)
	evs := d.Feed("Open https://a.example.com now")
	if len(evs) != 1 || evs[0].URL != "https://a.example.com" {
		t.Fatalf("unexpected events %+v", evs)
	}
}

func TestFeedPartialSuppressesCaptures(t *testing.T) {
	d := NewLineDetector(
		Rule{Pattern: regexp.MustCompile(`(?P<url>https://\S+)`), Kind: EventURL, Once: true},
		Rule{Pattern: regexp.MustCompile(`Press Enter to open`), Kind: EventPrompt, Once: true, Respond: "\r"},
	)
	// A partially rendered line must not fire (and permanently claim) the
	// URL rule with a truncated capture.
	if evs := d.FeedPartial("visit https://accounts.example.com/o/oauth2/auth?ac"); len(evs) != 0 {
		t.Fatalf("partial line fired %v", evs)
	}
	evs := d.Feed("visit https://accounts.example.com/o/oauth2/auth?access_type=offline&client_id=x")
	if len(evs) != 1 || evs[0].URL != "https://accounts.example.com/o/oauth2/auth?access_type=offline&client_id=x" {
		t.Fatalf("complete line events = %v", evs)
	}
	// Respond rules do fire on partial lines: gates like gh's Enter prompt
	// often render without a trailing newline.
	evs = d.FeedPartial("! Press Enter to open github.com in your browser... ")
	if len(evs) != 1 || evs[0].Respond != "\r" {
		t.Fatalf("respond rule on partial = %v", evs)
	}
}
