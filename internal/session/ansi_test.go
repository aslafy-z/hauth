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
		// Horizontal cursor moves separate words in Ink TUI output; they
		// must become spaces, not disappear.
		{"\x1b[4GYes,\x1b[9GI\x1b[11Gtrust\x1b[17Gthis\x1b[22Gfolder", " Yes, I trust this folder"},
		{"fwd\x1b[3Cback\x1b[2Dcol\x1b[5`done", "fwd back col done"},
		// Vertical moves and erases still vanish outright.
		{"\x1b[2Aup\x1b[Kerased", "uperased"},
	}
	for _, tc := range cases {
		if got := StripANSI(tc.in); got != tc.want {
			t.Errorf("StripANSI(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
