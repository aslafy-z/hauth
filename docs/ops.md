# Operations runbook

## Deploying with Coder

Use one of the templates in [deploy/](../deploy) (walkthrough in [coder.md](coder.md)). The moving parts:

- `hauth agent` starts from the workspace startup script, restores credentials, and serves the kiosk on `127.0.0.1:43117`.
- An owner-only `coder_app` fronts the kiosk.
- The store is a directory (`HAUTH_STORE_PATH`); keep it on the persistent home volume, or on shared storage to sync credentials across workspaces.

Health check: `curl -fsS http://127.0.0.1:43117/healthz`.

## Installing harness CLIs

hauth installs missing harness CLIs natively, with no package manager:

- `hauth install` installs every known-but-missing CLI; `hauth install claude codex` targets specific ones; `--force` reinstalls.
- `hauth agent --install all` (or `HAUTH_INSTALL=all`) does the same in the background at agent start without delaying the kiosk.
- The kiosk shows an Install button on harnesses whose CLI is missing; progress streams into the card's terminal panel.

Release binaries land in `~/.local/bin` (add it to PATH); official install scripts put their binaries in the same place. Sources per harness, as verified against each project's documentation or release listing:

| Harness | Method | Source |
|---|---|---|
| gh | latest GitHub release binary | `github.com/cli/cli` (`gh_<v>_linux_<arch>.tar.gz`, `gh_<v>_macOS_<arch>.zip`) |
| claude | official install script | `https://claude.ai/install.sh` (installs to `~/.local/bin/claude`); npm `@anthropic-ai/claude-code` as fallback when bash is missing |
| codex | latest GitHub release binary | `github.com/openai/codex` (`codex-<triple>.tar.gz`, musl builds on Linux) |
| opencode | latest GitHub release binary | `github.com/anomalyco/opencode` (`opencode-<os>-<arch>.tar.gz`/`.zip`); official script `https://opencode.ai/install` as fallback (installs to `~/.opencode/bin`) |
| agy | official install script | `https://antigravity.google/cli/install.sh` (installs to `~/.local/bin/agy`) |
| copilot | npm package | `@github/copilot` (npm is the only documented install channel); install-only, hauth does not manage its credentials |

Install scripts are fetched by hauth itself and run with `bash`; `curl` is not required. `GITHUB_TOKEN`, when set, is used for GitHub API rate limits.

## Log locations

- `hauth agent` logs to stderr; the HAPI image startup script captures them at `~/.hapi/hauth.log`.
- Session output shown in the kiosk passes through redaction first; the same redacted stream is what any log capture sees.
- State records of what hauth restored: `~/.local/state/hauth/`.

## Rotating or revoking a user's credentials

Per harness, from any machine with store access:

```sh
hauth store delete <harness>   # removes bundle and lease from the store
```

Then, in a workspace that still has the credentials on disk, revoke provider-side and clean up local files through the kiosk's log out action or the CLI-native logout. Order matters: CLI-native logout runs first while the token is still readable so provider-side revocation happens.

To rotate: delete, then run the login flow again from any workspace.

## Lease stuck or held by a dead workspace

Leases have a short TTL (10 minutes) renewed by the holder's watcher loop; a stopped workspace loses its lease by expiry without any cleanup. If a harness shows "credentials owned by workspace X" longer than the TTL:

1. Check the lease record: `hauth store list` shows holder and expiry.
2. If the holder is gone and the lease is expired, the next workspace that needs it acquires it automatically; the kiosk also offers an explicit take-over.
3. A take-over acquires the lease and probes; if the probe fails, it starts a fresh login flow.

## Inspecting the store

The file store lives under `~/.local/share/hauth/store` by default (`--store-path` / `HAUTH_STORE_PATH`), one directory per user and harness:

```sh
ls ~/.local/share/hauth/store/<user>/<harness>/
# bundle.tar.gz  manifest.json  lease.json  rev

jq . ~/.local/share/hauth/store/<user>/<harness>/manifest.json
```

The manifest lists file paths, hashes, and which files are identity files. The tarball is the credential material itself: treat access to it as access to the credentials. To share credentials across workspaces, point the store path at storage mounted in all of the user's workspaces.

## Metrics

Planned, not shipped in v0: a Prometheus endpoint on loopback exposing probe results per harness, for fleet-wide auth expiry visibility.
