# Test fixtures

The `*.txt` transcripts are synthetic: they reproduce the documented output
shape of each CLI's browser-free login flow (URLs, one-time codes, prompts,
spinner redraws, ANSI noise) but were not recorded from live sessions.
Replace them with real recordings per the phase 0 spike (`docs/flows.md`)
whenever a CLI's output changes; detector tests replay these files verbatim.

Exceptions: `claude-trust.txt` is a real recording of the folder-trust
dialog (claude 2.1.250, captured with `script`); its words are laid out
with cursor-column escapes instead of spaces, which is exactly what its
test exercises. The first line of `gh-device.txt` is the real gh 2.97
preamble (terminal status queries plus the git-credentials gate).

`fakecli/` is a stand-in harness CLI used by end-to-end tests and demo mode.
It implements `login` (paste-code flow writing
`~/.config/fakecli/credentials.json`), `status`, and `logout`.
