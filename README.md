<div align="center">

<h1>hauth</h1>

<p><strong>Log your AI coding CLIs in once. Every workspace stays authenticated.</strong></p>

<p>Browser-free logins for <code>gh</code>, <code>claude</code>, <code>codex</code>, <code>opencode</code>, and <code>agy</code>,
brokered through a phone-friendly loopback kiosk, synced to a per-user credential store.</p>

<p>
  <a href="https://github.com/aslafy-z/hauth/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/aslafy-z/hauth/actions/workflows/ci.yml/badge.svg" /></a>
  <a href="https://github.com/aslafy-z/hauth/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/aslafy-z/hauth?include_prereleases" /></a>
  <a href="https://pkg.go.dev/github.com/aslafy-z/hauth"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/aslafy-z/hauth.svg" /></a>
  <a href="https://goreportcard.com/report/github.com/aslafy-z/hauth"><img alt="Go Report Card" src="https://goreportcard.com/badge/github.com/aslafy-z/hauth" /></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-blue" /></a>
</p>

</div>

- 🔐 **One login, every workspace.** Credentials land in a per-user file store and are restored into each workspace at start.
- 📱 **Phone-friendly kiosk.** URLs and one-time codes surface in a tap-to-copy loopback UI, fronted by your platform (Coder app, SSH tunnel, Tailscale).
- 🙅 **Do-no-harm detection.** Tokens injected by the platform or logins done by hand show as "managed externally" and are never touched, stored, or revoked.
- 🔁 **Rotation-aware sync.** Rotating token caches get a single lease-holding writer; stable ones sync last-writer-wins. No silent clobbering.
- 🖥️ **UI optional.** `hauth login <harness>` does every flow from a plain terminal.

> [!WARNING]
> This project is highly vibe coded. Review it before trusting it with
> anything you care about.
>
> The kiosk UI has no authentication of its own: it binds loopback only and
> securing access to it is on you (owner-only Coder app, SSH tunnel,
> Tailscale, or similar). Anything that can reach the port can drive logins.
>
> Early v0. Interfaces and CLI flags may change between minor versions. The
> login detectors ship with synthetic transcript fixtures; recording real CLI
> transcripts is tracked in [docs/flows.md](docs/flows.md).

## The problem

Interactive CLIs (`gh`, `claude`, `codex`, `opencode`, `agy`) authenticate via browser OAuth with loopback redirects and cache credentials in the user's home directory. In a cloud workspace (for example a Coder pod) this fails twice: the user's browser cannot reach the pod's loopback, and the credential cache dies with the home volume or is duplicated across workspaces.

hauth makes the browser-free flows the default, brokers the one-time URLs and codes through a loopback web UI (fronted by whatever the platform provides: a Coder app, an SSH tunnel, Tailscale), and keeps the resulting credentials in a per-user store so any workspace of that user starts authenticated.

hauth does not know Coder or any other platform. It sees env vars, files, and a store. If a harness is already authenticated by something else (an env var, a mounted secret, a manual login), hauth reports "managed externally" and stays out of the way.

## How it works

```
+---------------------------------------- workspace pod -----------------------------------+
|  startup script ----(hauth agent)---> hauth agent                                          |
|                                        |  restore   at start                               |
|                                        |  watch     inotify on adapter paths               |
|                                        |  kiosk     http://127.0.0.1:43117                 |
|                                        |  sessions  pty per login attempt                  |
|  claude / codex / opencode / agy / gh  <---- files restored by hauth                       |
+--------------------------------------------------------------------------------------------+
            |                                      ^
            | platform front door (owner only)     | store backend
            v                                      v
   user's browser / phone                 per-user file store
                                          (home volume, or shared storage
                                           to follow the user across workspaces)
```

Core concepts:

- **Adapter**: one per harness CLI. Declares credential file locations, env-var short-circuits, login methods, a status probe, and refresh semantics.
- **Bundle**: versioned tarball of the adapter's credential files plus a manifest with per-file hashes.
- **Store**: key-value of `{harness -> bundle}` per user, with compare-and-swap on a revision.
- **Lease**: which workspace is allowed to refresh a harness right now. Everyone else is read-only for that harness.
- **Source**: where a harness's current auth comes from (`env`, `file-external`, `store`, `none`). Drives what the UI shows and which actions are allowed.

## Install

Binaries for linux and darwin (amd64, arm64) are on [GitHub Releases](https://github.com/aslafy-z/hauth/releases):

```sh
# with the GitHub CLI; or pick the asset for your OS/arch from the releases page
gh release download --repo aslafy-z/hauth --pattern "hauth_*_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz" -O - | tar -xz hauth
sudo install hauth /usr/local/bin/hauth
```

Container image:

```sh
docker run --rm ghcr.io/aslafy-z/hauth version
```

From source:

```sh
go install github.com/aslafy-z/hauth/cmd/hauth@latest
```

## Quickstart

Inside a workspace, start the agent. It restores credentials from the store, watches for changes, and serves the kiosk on loopback:

```sh
hauth agent
```

Open the kiosk through your platform's front door:

- Coder: the `Agent Auth` app from [deploy/coder](deploy/coder), shared with the workspace owner only.
- SSH: `ssh -L 43117:127.0.0.1:43117 <workspace>` then browse to `http://127.0.0.1:43117`.
- Tailscale: `tailscale serve 43117`.

Or skip the UI entirely and log in from a terminal:

```sh
hauth login gh
hauth status
```

Harness CLIs that are not present yet can be installed by hauth itself, with no package manager: `hauth install` installs every missing CLI natively (release binaries or the harness's official install script, into `~/.local/bin`), `hauth agent --install all` does the same in the background at agent start, and the kiosk shows an Install button on any harness whose CLI is missing.

## Harness support

| Harness | Default method | Env short-circuit | Sync policy |
|---|---|---|---|
| gh | device flow (`gh auth login --web`) | `GH_TOKEN`, `GITHUB_TOKEN` | stable |
| claude | paste code, fallback long-lived token | `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY` | rotating |
| codex | device auth | `OPENAI_API_KEY` | rotating |
| opencode | provider paste flow | provider-specific | rotating |
| agy | paste code with remote hints | none documented | stable |

## Source detection

Every status probe resolves a source, in order: `env` (a CLI-honored env var is set), `file-external` (credential files exist but hauth did not write them), `store` (files match a restored bundle), `none`. Example kiosk states:

```
gh       managed externally (env GH_TOKEN)     valid     (log out disabled: env)
claude   managed externally (files)            expired   [log out] [log in]
codex    logged in (store, refreshed 2h ago)   valid     [log out] [re-login]
opencode not logged in                                   [log in]
agy      logged in (store)                     unknown   [check] [log out] [re-login]
```

Rules that follow from the source:

- `env`: hauth cannot unset another process's environment, so logout is disabled with the reason shown. Where the env var beats files (gh, claude, codex), "log in anyway" is hidden too.
- `file-external`: files were not created by hauth. Logout warns; an explicit "adopt into store" action exists, and adoption is never automatic.
- Re-login is logout plus the default login method as one action, so a harness never ends up half logged out.

## Store

The store is a directory (`--store-path`, default `~/.local/share/hauth/store`) with per-user, per-harness entries: bundle, manifest, lease, and a revision under compare-and-swap. On a persistent home volume it survives workspace restarts; on storage mounted into all of a user's workspaces it follows the user across them. `--store none` disables it. Identity comes from `HAUTH_USER` and `HAUTH_INSTANCE` (defaults: `$USER` and `$USER@hostname`). Other backends (a secrets manager, Vault) can slot in behind the same store interface if the need shows up.

## Coder integration

[deploy/coder-hapi](deploy/coder-hapi) is a complete, importable Kubernetes template: workspace pod with persistent home, the `ghcr.io/aslafy-z/hauth-hapi` image, HAPI hub and runner, and hauth as the auth companion, storing credentials on the home volume.

[deploy/coder](deploy/coder) has the minimal snippet for adding hauth to an existing template: a `coder_script` that starts `hauth agent` with `HAUTH_USER`/`HAUTH_INSTANCE`/`HAUTH_ALLOWED_ORIGINS` mapped from Coder workspace variables, and an owner-only `coder_app` for the kiosk. The binary never reads `CODER_*` variables itself. See [docs/coder.md](docs/coder.md).

## HAPI workspace image

`ghcr.io/aslafy-z/hauth-hapi` is a ready-made Coder workspace image that bundles hauth, Node, and [HAPI](https://hapi.run), with a startup script that installs the selected harness CLIs via `hauth install`, starts `hauth agent` on `127.0.0.1:43117`, and starts the HAPI hub and runner. It is the default image of the [deploy/coder-hapi](deploy/coder-hapi) template.

## Security model

The kiosk binds loopback only and has no authentication of its own; whatever fronts it (Coder app, SSH tunnel, Tailscale) is responsible for access control. `Origin` and `Host` headers are checked against `HAUTH_ALLOWED_ORIGINS`. Credential files are written `0600`, store entries are `0600` in `0700` directories, and session output is redacted before it reaches logs or the UI event stream. Details and non-goals: [docs/threat-model.md](docs/threat-model.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Adding a harness adapter is documented in [docs/adapters.md](docs/adapters.md).

## License

[Apache-2.0](LICENSE)
