package session

import (
	"strings"
	"testing"
)

func TestRedactPatterns(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"gh token", "token: ghp_" + strings.Repeat("A", 36) + " ok", "token: [REDACTED] ok"},
		{"gh oauth", "gho_" + strings.Repeat("x", 20), "[REDACTED]"},
		{"anthropic", "key sk-ant-api03-abcdefghijkl", "key [REDACTED]"},
		{"openai", "sk-" + strings.Repeat("b", 24), "[REDACTED]"},
		{"jwt", "id eyJ" + strings.Repeat("a", 20) + "." + strings.Repeat("b", 12) + "." + strings.Repeat("c", 12), "id [REDACTED]"},
		{"bearer", "Authorization: Bearer abc123def456", "Authorization: [REDACTED]"},
		{"clean", "Open https://example.com and enter ABCD-1234", "Open https://example.com and enter ABCD-1234"},
	}
	for _, tc := range cases {
		if got := string(Redact([]byte(tc.in))); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestStreamRedactorBoundary(t *testing.T) {
	chunks := []string{"your token is gh", "p_", strings.Repeat("Z", 30), " done\n"}
	r := NewStreamRedactor()
	var out strings.Builder
	for _, c := range chunks {
		out.Write(r.Write([]byte(c)))
	}
	out.Write(r.Flush())
	got := out.String()
	if strings.Contains(got, "ghp_") {
		t.Fatalf("secret leaked across chunk boundary: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("secret not redacted: %q", got)
	}
	if !strings.HasSuffix(got, " done\n") {
		t.Fatalf("trailing text lost: %q", got)
	}
}

func TestStreamRedactorPassthrough(t *testing.T) {
	r := NewStreamRedactor()
	var out strings.Builder
	out.Write(r.Write([]byte("Paste code: ")))
	out.Write(r.Flush())
	if out.String() != "Paste code: " {
		t.Fatalf("plain prompt mangled: %q", out.String())
	}
}
