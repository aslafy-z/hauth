package session

import "testing"

func TestStripANSI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\x1b[1mFoo\x1b[0m bar", "Foo bar"},
		{"\x1b[2K\x1b[0;38;5;208mspinner\x1b[0m", "spinner"},
		{"\x1b]0;window title\x07text", "text"},
		{"plain", "plain"},
		{"tab\tkept", "tab\tkept"},
		{"bell\x07gone", "bellgone"},
	}
	for _, tc := range cases {
		if got := StripANSI(tc.in); got != tc.want {
			t.Errorf("StripANSI(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
