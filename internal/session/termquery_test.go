package session

import (
	"bytes"
	"testing"
)

func TestTermQueryResponses(t *testing.T) {
	// gh's prompter emits exactly this preamble before its Y/n gate.
	in := []byte("\x1b]11;?\x1b\\\x1b[6n\x1b[0G? Authenticate Git\x1b7\x1b[999;999f\x1b[6n")
	resp, _ := termQueryResponses(in, 50, 240)
	for _, want := range []string{"\x1b]11;rgb:", "\x1b[50;240R\x1b[50;240R"} {
		if !bytes.Contains(resp, []byte(want)) {
			t.Errorf("response %q missing %q", resp, want)
		}
	}

	if resp, _ := termQueryResponses([]byte("\x1b]10;?\x07"), 50, 240); !bytes.Contains(resp, []byte("\x1b]10;rgb:")) {
		t.Errorf("OSC10 BEL query unanswered: %q", resp)
	}
	if resp, _ := termQueryResponses([]byte("plain text \x1b[2K"), 50, 240); len(resp) != 0 {
		t.Errorf("responded to non-query output: %q", resp)
	}
}

func TestTermQueryResponsesSplitAcrossChunks(t *testing.T) {
	resp, rest := termQueryResponses([]byte("text\x1b["), 50, 240)
	if len(resp) != 0 {
		t.Fatalf("responded to split sequence: %q", resp)
	}
	resp, _ = termQueryResponses(append(rest, []byte("6n")...), 50, 240)
	if !bytes.Contains(resp, []byte("\x1b[50;240R")) {
		t.Fatalf("reassembled query unanswered: %q", resp)
	}
}
