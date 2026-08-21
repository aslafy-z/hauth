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

Verified against agy 1.1.17 on 2026-08-21 (installed via the official script).

- No `login`, `logout`, or `status` subcommands exist; unknown subcommands fall through to the TUI. Method `interactive`: run `agy` plainly in the session pty; on first run the TUI walks through onboarding and sign-in, launching a browser OAuth flow with a loopback callback. The session's BROWSER shim captures the URL.
- Env short-circuit: none documented.
- Files: tokens land in the OS keyring when a D-Bus session bus is reachable; on headless hosts and containers the CLI bypasses the keyring and stores tokens in a token directory under `~/.gemini/antigravity-cli/` (hauth syncs `~/.gemini/antigravity-cli/tokens/**`; directory name inferred from the CLI's file token storage internals, to be confirmed with a recorded pod login). Sync policy: stable.
- Connection test: exit codes and output prove nothing because print mode falls back to an anonymous tier when unauthenticated. The expensive probe runs `agy --log-file <scratch> --print-timeout 45s -p ...` and parses the log: `applyAuthResult: email=...` means authenticated (account extracted); `You are not logged into Antigravity` means not. Both log signatures captured from real runs.
- Logout: removes the file token directory; keyring-held credentials cannot be removed by hauth.
- Fixture status: transcript synthetic but assembled from captured TUI text; log-parsing fixtures are real captures.

## fake (testing only)

Registered only when `HAUTH_ENABLE_FAKE=1`. A shell script standing in for a harness CLI: prints a URL and a device code, waits for a pasted code, writes a credentials file. Exists so the whole pipeline (detector, session, store round-trip) is exercisable in CI and demos without touching a real provider. Fixture status: authoritative by definition.
