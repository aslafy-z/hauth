# Coder integration

hauth never reads `CODER_*` variables. The template maps Coder identity onto hauth's own env vars; the binary stays platform-agnostic.

## Variable mapping

| hauth | from Coder | purpose |
|---|---|---|
| `HAUTH_USER` | `data.coder_workspace_owner.me.name` | store identity: which per-user Secret to use |
| `HAUTH_INSTANCE` | `data.coder_workspace.me.name` | lease identity: which workspace holds a refresh lease |
| `HAUTH_ALLOWED_ORIGINS` | the Coder access URL host | kiosk Origin/Host validation for the proxied app |

## Template snippet

For a complete, importable Kubernetes template that provisions the workspace pod, HAPI, and hauth together, use [deploy/coder-hapi](../deploy/coder-hapi/). The snippet below is for adding hauth to an existing template.

[deploy/coder/main.tf](../deploy/coder/main.tf) contains the resources to paste into an existing template:

- `coder_script` runs `hauth agent` at workspace start with the mapping above.
- `coder_app` exposes the kiosk as an "Agent Auth" app at `http://localhost:43117` with `share = "owner"`. The kiosk has no auth of its own; owner-only sharing is the access control.
- An optional `coder_external_auth` example exports `GH_TOKEN`. hauth detects it as an env-managed credential, reports "managed externally" on the gh row, and touches nothing. This is the intended behavior, not a limitation.

Wire `agent_id` to your `coder_agent` resource and make sure the hauth binary is present in the workspace image (the `ghcr.io/aslafy-z/hauth-hapi` image ships it at `/usr/local/bin/hauth`).

## Store wiring

For the k8s backend the pod must run with a ServiceAccount allowed to read and update the user's Secret. Set on the agent environment:

```
HAUTH_STORE=k8s
HAUTH_NAMESPACE=<workspace namespace>
HAUTH_SECRET_PREFIX=harness-creds-
```

RBAC examples and the create-by-name caveat are in [deploy/k8s/rbac.yaml](../deploy/k8s/rbac.yaml). If your template does not attach a ServiceAccount to workspace pods today, that is the one template change the k8s backend requires. The file backend works without any of this for single-workspace use, but does not survive home-volume loss and does not share across workspaces unless the store path is on shared storage.

## Non-Coder front doors

Nothing in the binary assumes Coder. Equivalent front doors:

```sh
# SSH tunnel
ssh -L 43117:127.0.0.1:43117 user@workspace
# then open http://127.0.0.1:43117

# Tailscale
tailscale serve 43117
```
