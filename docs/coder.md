# Coder integration

hauth never reads `CODER_*` variables. The template maps Coder identity onto hauth's own env vars; the binary stays platform-agnostic.

## Variable mapping

| hauth | from Coder | purpose |
|---|---|---|
| `HAUTH_USER` | `data.coder_workspace_owner.me.name` | store identity: which per-user store entry to use |
| `HAUTH_INSTANCE` | `data.coder_workspace.me.name` | lease identity: which workspace holds a refresh lease |
| `HAUTH_ALLOWED_ORIGINS` | the Coder access URL host | kiosk Origin/Host validation for the proxied app |

## Template snippet

For a complete, importable Kubernetes template that provisions the workspace pod, HAPI, and hauth together, use [deploy/coder-hapi](../deploy/coder-hapi/), also shipped on every release as `hauth-coder-hapi-template.zip` for direct upload in Coder's template creation UI. The snippet below is for adding hauth to an existing template.

[deploy/coder/main.tf](../deploy/coder/main.tf) contains the resources to paste into an existing template (also on each release as `hauth-coder-snippet.zip`; unlike the template zip it is not importable on its own):

- `coder_script` runs `hauth agent` at workspace start with the mapping above.
- `coder_app` exposes the kiosk as an "Agent Auth" app at `http://localhost:43117` with `share = "owner"`. The kiosk has no auth of its own; owner-only sharing is the access control.
- An optional `coder_external_auth` example exports `GH_TOKEN`. hauth detects it as an env-managed credential, reports "managed externally" on the gh row, and touches nothing. This is the intended behavior, not a limitation.

Wire `agent_id` to your `coder_agent` resource and make sure the hauth binary is present in the workspace image (the `ghcr.io/aslafy-z/hauth-hapi` image ships it at `/usr/local/bin/hauth`).

## Store wiring

The store is a directory. Keep the default (`~/.local/share/hauth/store`) on a persistent home volume so credentials survive workspace restarts:

```
HAUTH_STORE=file
HAUTH_STORE_PATH=/home/coder/.local/share/hauth/store
```

To share credentials across all of a user's workspaces, mount per-user shared storage into each workspace and point `HAUTH_STORE_PATH` at it. The lease and compare-and-swap semantics are designed for exactly that concurrent access.

## Non-Coder front doors

Nothing in the binary assumes Coder. Equivalent front doors:

```sh
# SSH tunnel
ssh -L 43117:127.0.0.1:43117 user@workspace
# then open http://127.0.0.1:43117

# Tailscale
tailscale serve 43117
```
