# Operations runbook

## Deploying with Coder

Use the snippet in [deploy/coder](../deploy/coder) (walkthrough in [coder.md](coder.md)) or the prebuilt workspace image `ghcr.io/aslafy-z/hauth-hapi`. The moving parts:

- `hauth agent` starts from the workspace startup script, restores credentials, and serves the kiosk on `127.0.0.1:43117`.
- An owner-only `coder_app` fronts the kiosk.
- For the k8s store, the workspace pod needs a ServiceAccount with access to the user's Secret; see [deploy/k8s](../deploy/k8s).

Health check: `curl -fsS http://127.0.0.1:43117/healthz`.

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

## Inspecting the k8s store

```sh
kubectl get secret harness-creds-<user> -o json \
  | jq -r '.data | keys[]'
# <harness>.tar.gz, <harness>.manifest.json, <harness>.lease.json per harness

kubectl get secret harness-creds-<user> -o jsonpath='{.data.gh\.manifest\.json}' \
  | base64 -d | jq .
```

The manifest lists file paths, hashes, and which files are identity files. The tarball is the credential material itself: treat access to it as access to the credentials.

## Metrics

Planned, not shipped in v0: a Prometheus endpoint on loopback exposing probe results per harness, for fleet-wide auth expiry visibility.
