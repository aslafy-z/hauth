package session

import (
	"bytes"
	"testing"
)

func TestStripTermQueries(t *testing.T) {
	// gh's prompter emits exactly this preamble before its Y/n gate.
	in := []byte("\x1b]11;?\x1b\\\x1b[6n\x1b[0G? Authenticate Git\x1b7\x1b[999;999f\x1b[6n")
	resp, out, carry := stripTermQueries(in, 50, 240)
	for _, want := range []string{"\x1b]11;rgb:", "\x1b[50;240R\x1b[50;240R"} {
		if !bytes.Contains(resp, []byte(want)) {
			t.Errorf("response %q missing %q", resp, want)
		}
	}
	// The queries must not reach clients (xterm.js would answer them too,
	// and surplus reports leak into prompts as typed text), but everything
	// else must pass through untouched.
	if want := "\x1b[0G? Authenticate Git\x1b7\x1b[999;999f"; string(out) != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	if len(carry) != 0 {
		t.Errorf("carry = %q, want empty", carry)
	}

	if resp, _, _ := stripTermQueries([]byte("\x1b]10;?\x07"), 50, 240); !bytes.Contains(resp, []byte("\x1b]10;rgb:")) {
		t.Errorf("OSC10 BEL query unanswered: %q", resp)
	}
	if resp, out, _ := stripTermQueries([]byte("plain text \x1b[2K"), 50, 240); len(resp) != 0 || string(out) != "plain text \x1b[2K" {
		t.Errorf("non-query output mangled: resp=%q out=%q", resp, out)
	}
}

func TestStripTermQueriesSplitAcrossChunks(t *testing.T) {
	resp, out, carry := stripTermQueries([]byte("text\x1b["), 50, 240)
	if len(resp) != 0 || string(out) != "text" || string(carry) != "\x1b[" {
		t.Fatalf("split handling: resp=%q out=%q carry=%q", resp, out, carry)
	}
	resp, out, carry = stripTermQueries(append(carry, []byte("6nmore")...), 50, 240)
	if !bytes.Contains(resp, []byte("\x1b[50;240R")) {
		t.Fatalf("reassembled query unanswered: %q", resp)
	}
	if string(out) != "more" || len(carry) != 0 {
		t.Fatalf("reassembled output: out=%q carry=%q", out, carry)
	}

	// A trailing complete non-query escape must be delivered, not withheld.
	if _, out, carry := stripTermQueries([]byte("x\x1b[2K"), 50, 240); string(out) != "x\x1b[2K" || len(carry) != 0 {
		t.Fatalf("held back non-query tail: out=%q carry=%q", out, carry)
	}
}
