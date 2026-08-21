# Browser-free login flows

One section per harness: the flow hauth drives, the env vars that short-circuit it, and the verification status of the detector fixtures.

> [!WARNING]
> Verification status matters here. Detector rules match exact CLI prompt
> text. The fixtures under `testdata/` are currently synthetic: written from
> upstream documentation and issue trackers, not recorded from live runs.
> Each section states what still needs a recorded transcript. Re-recording in
> a real workspace is the highest-priority follow-up for every adapter.

## gh (GitHub CLI)

- Method `device`: `gh auth login --web --git-protocol https --skip-ssh-key --hostname github.com`. The CLI prints a one-time code and the `https://github.com/login/device` URL, then polls until the user approves. hauth surfaces both; no input is fed back to the CLI besides an Enter keypress when prompted to continue.
- Env short-circuit: `GH_TOKEN`, then `GITHUB_TOKEN`. When set, `gh` ignores `hosts.yml`, so the source is `env` and login is pointless while it is set.
- Files: `~/.config/gh/hosts.yml`. Sync policy: stable.
- Logout: `gh auth logout --hostname github.com`.
- Status probe: cheap probe inspects files/env only; the expensive probe runs `gh auth status`.
- Fixture status: synthetic. Needs a recorded transcript of a full device flow, including the "Press Enter to open" prompt wording.

## claude (Claude Code)

- Method `paste_code`: run `claude` with `BROWSER` pointed at the hauth shim so the OAuth URL is captured instead of opened. The flow prints a URL; after browser-side approval the user pastes the resulting code back into the CLI.
- Method `token`: paste a long-lived token created elsewhere with `claude setup-token`, stored as `CLAUDE_CODE_OAUTH_TOKEN`.
- Env short-circuit: `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY`. Env beats files.
- Files: `~/.claude/.credentials.json` (identity), plus a filtered copy of `~/.claude.json` limited to onboarding and account keys. Project history in that file is never synced. Sync policy: rotating.
- Logout: `/logout` typed into a pty session.
- Fixture status: synthetic. Needs recording of whether `claude` prints a paste prompt when `BROWSER` is a no-op, and the exact prompt wording.

## codex (OpenAI Codex CLI)

- Method `device`: `codex login --device-auth`; prints a URL and user code.
- Method `api_key`: `codex login --with-api-key` reading the key from stdin.
- Env short-circuit: `OPENAI_API_KEY`.
- Files: `~/.codex/auth.json`. Sync policy: rotating (the CLI refreshes tokens on its own).
- Logout: `codex logout`.
- Status probe: expensive probe runs `codex login status`.
- Fixture status: synthetic. Needs a recorded device-auth transcript and confirmation of current flag names.

## opencode

- Method `paste`: `opencode auth login`, an interactive provider picker followed by provider-specific paste flows.
- Env short-circuit: provider-specific (for example `ANTHROPIC_API_KEY` when the Anthropic provider is used). hauth treats none as authoritative.
- Files: `~/.local/share/opencode/auth.json`. Sync policy: rotating (provider-dependent; rotating is the safe assumption).
- Logout: `opencode auth logout`.
- Fixture status: synthetic. The provider list and prompt wording change between releases; needs a recorded transcript per supported provider.

## agy (Antigravity CLI)

- Method `paste_code`: run the CLI with remote-session hints so it prints a URL plus code instead of opening a browser.
- Env short-circuit: none documented.
- Files: `~/.config/agy/**` (file fallback; no OS keyring assumed in workspace images). Sync policy: stable.
- Logout: `/logout` typed into a pty session.
- Fixture status: synthetic and the least certain of the five. Needs empirical answers to: which env makes agy print URL + code inside a pod, and where exactly credentials land without a keyring.

## fake (testing only)

Registered only when `HAUTH_ENABLE_FAKE=1`. A shell script standing in for a harness CLI: prints a URL and a device code, waits for a pasted code, writes a credentials file. Exists so the whole pipeline (detector, session, store round-trip) is exercisable in CI and demos without touching a real provider. Fixture status: authoritative by definition.
