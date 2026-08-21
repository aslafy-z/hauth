# Test fixtures

The `*.txt` transcripts are synthetic: they reproduce the documented output
shape of each CLI's browser-free login flow (URLs, one-time codes, prompts,
spinner redraws, ANSI noise) but were not recorded from live sessions.
Replace them with real recordings per the phase 0 spike (`docs/flows.md`)
whenever a CLI's output changes; detector tests replay these files verbatim.

`fakecli/` is a stand-in harness CLI used by end-to-end tests and demo mode.
It implements `login` (paste-code flow writing
`~/.config/fakecli/credentials.json`), `status`, and `logout`.
