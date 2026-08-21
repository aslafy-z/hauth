# Writing a harness adapter

An adapter teaches hauth one harness CLI: where its credentials live, which env vars short-circuit login, how to drive its browser-free login flows, and how to probe and revoke its state. Adapters live under `internal/adapter/<name>/` and register themselves with the registry.

## The contract

```go
type Adapter interface {
    Name() string
    Paths() []string           // credential globs, relative to $HOME
    IdentityPaths() []string   // subset whose hashes drive source detection
    EnvAuth() []EnvAuthSpec    // env vars that make the CLI skip login
    EnvOverridesFiles() bool   // does a set env var beat credential files
    Methods() []Method         // ordered login methods; first is default
    Probe(ctx, cheap bool) (Status, error)
    Refresh() RefreshPolicy    // None | Stable | Rotating
    Logout(ctx, src Source) error
}
```

Method by method:

- `Paths` is the complete list of files hauth bundles and restores. Keep it minimal; every file here ends up in the store.
- `IdentityPaths` is the subset that answers "who is logged in". Files the CLI rewrites on its own for non-auth reasons (onboarding flags, UI state) stay out, so they cannot flip a harness from `store` to `file-external` on their own.
- `EnvAuth` lists env vars most specific first. If any is set, the source is `env` and hauth keeps its hands off.
- `EnvOverridesFiles` controls whether the kiosk offers "log in anyway" while an env var is set. For gh, claude, and codex the env var wins, so the button would produce a login the CLI then ignores; return true and the kiosk hides it.
- `Methods` are executed in a pty by `internal/session`. Each method carries its own detector.
- `Probe` with `cheap=true` must not touch the network: resolve the source (via `internal/source.Resolve`), read account info from local files if available, and leave validity `unknown` when only a network call can tell. The expensive probe may run the CLI's own status command.
- `Refresh` picks the sync policy: `Rotating` CLIs (token refresh on their own) get a single lease-holding workspace that pushes; `Stable` CLIs sync last-writer-wins on content change.
- `Logout` must attempt CLI-native logout first, while the token is still readable, so provider-side revocation happens; then remove local files. Store cleanup is the caller's job.

## Detectors

A detector turns CLI output into events (`url`, `code`, `prompt`, `success`, `failure`). Build one from line rules:

```go
adapter.NewLineDetector(
    adapter.Rule{Pattern: regexp.MustCompile(`one-time code: (?P<code>[A-Z0-9-]+)`), Kind: adapter.EventCode, Once: true},
    adapter.Rule{Pattern: regexp.MustCompile(`Paste the code`), Kind: adapter.EventPrompt},
)
```

Rules match single lines of the CLI's actual output, as recorded in `testdata/<name>/`. Do not write clever buffer-wide regexes; when the CLI wording changes, a failing fixture test should point at the exact line. A generic URL fallback is built in, and the `BROWSER` shim is the primary URL source anyway: a CLI that shells out to a browser hands hauth the URL directly.

## File filtering

Adapters whose files mix credentials with other state implement `FileFilter`:

```go
func (a *Adapter) FilterFile(rel string, content []byte) ([]byte, bool)
```

Return the filtered content, or `ok=false` to exclude the file from the bundle. The claude adapter uses this to strip `~/.claude.json` down to onboarding and account keys so project history never reaches the store.

## Worked example: the fake adapter

`internal/adapter/fake` plus `testdata/fakecli` is a complete, minimal adapter pair kept deliberately readable:

- `fakecli` is a shell script that prints a URL and a device code, waits for a pasted code, and writes `~/.config/fakecli/credentials.json`.
- The adapter declares that file as its only path, one `paste_code` method with a two-rule detector, and a cheap probe that reads the file.
- It registers only when `HAUTH_ENABLE_FAKE=1`, and the end-to-end tests drive the full pipeline through it: session, detector, store push, restore into a second home directory.

Copy it, rename, and replace each piece with the real CLI's behavior. Record a transcript of the real flow into `testdata/<name>/` first and write the detector rules against that file; the fixture test then locks the wording.

## Checklist for a new adapter

1. Record the browser-free login transcript into `testdata/<name>/`.
2. Implement the interface; wire registration.
3. Fixture test: feed the transcript through the detector, assert events.
4. Probe test: source resolution for env, file-external, store, none.
5. Document the flow in `docs/flows.md` with its verification status.
