---
display_name: Coder HAPI + hauth Workspace Template
description: Provision Kubernetes pods with persistent home volumes, HAPI, hauth-brokered harness authentication, and optional AI agent harnesses
icon: /icon/k8s.svg
verified: false
tags: [kubernetes, hapi, hauth, ai, agents]
---

# Coder HAPI + hauth Workspace Template

This template provisions a Kubernetes pod as a Coder workspace with a persistent home volume, a per-workspace [HAPI](https://hapi.run) hub and runner, and [hauth](https://github.com/aslafy-z/hauth) as the authentication companion for the AI agent harness CLIs: browser-free login brokering plus credential persistence across workspaces.

## Prerequisites

You need an existing Kubernetes cluster and namespace for workspace resources. The namespace must exist before workspaces are created.

The Coder provisioner must be able to authenticate to the target cluster. If Coder runs inside the same cluster, use in-cluster ServiceAccount credentials (`use_kubeconfig = false`). If Coder runs outside the cluster, set `use_kubeconfig = true` and provide a valid `~/.kube/config` on the Coder host.

The default workspace image is `ghcr.io/aslafy-z/hauth-hapi:latest`, built from [deploy/docker/Dockerfile.hapi](../docker/Dockerfile.hapi). Any replacement image must ship `mise`, the hauth binary at `/usr/local/bin/hauth`, and the startup script at `/opt/hauth/start-hapi.sh`.

Set the `workspace_apps_domain` template variable to your Coder wildcard access domain without the leading `*.` (for example `apps.coder.example.com`). The hauth kiosk validates `Host` and `Origin` headers by exact hostname; this variable lets the template compute the Agent Auth app hostname and pass it through `HAUTH_ALLOWED_ORIGINS`. Without it, the kiosk is reachable through an SSH tunnel (`coder ssh` port-forward) but rejects the proxied Coder app.

## Architecture

Workspace creation creates a persistent volume claim for `/home/coder` and a Kubernetes Deployment for the running workspace pod. The persistent volume keeps the user's home directory, tool caches, HAPI state, and locally restored credentials across workspace restarts. The Deployment is ephemeral and is recreated when the workspace starts.

At startup, `/opt/hauth/start-hapi.sh` (baked into the image) installs the selected harness CLIs through the user's global `mise` config, starts `hauth agent` on `127.0.0.1:43117`, starts the HAPI hub on `127.0.0.1:3006`, and starts a HAPI runner rooted at `/home/coder/project`. The hauth agent restores stored credentials before the harnesses are first used, watches for changes, and pushes them back to the credential store.

## The Agent Auth app

The **Agent Auth** app is the hauth kiosk: a mobile-friendly loopback UI showing the authentication state of `gh`, `claude`, `codex`, `opencode`, and `agy`, with one button per browser-free login method. URLs and one-time codes are tap-to-copy; paste-code flows have an input field; a terminal panel is available but hidden by default.

hauth also persists credentials: after a login, the credential files are bundled and written to a per-user store, and any other workspace of the same user restores them at start. Tokens injected by the platform (for example `GH_TOKEN` from external auth) or logins done by hand are reported as "managed externally" and never touched.

The kiosk has no authentication of its own. The Coder app is `share = "owner"`; that owner-only sharing is the access control.

## Credential store modes

The **Credential store** parameter selects where hauth keeps bundles:

- `file` (default): under `/home/coder/.local/share/hauth/store` on the home volume. Zero setup, but per-workspace only: credentials do not follow the user to other workspaces and die with the volume.
- `kubernetes`: in a per-user Secret (`harness-creds-<user>`) in the workspace namespace, shared by all of the user's workspaces. Requires workspace pods to run with a ServiceAccount allowed to read and write those Secrets: set the `workspace_service_account` template variable and apply RBAC from [deploy/k8s/rbac.yaml](../k8s/rbac.yaml).

## Parameters

| Parameter | Default | Notes |
|---|---|---|
| Workspace image | `ghcr.io/aslafy-z/hauth-hapi:latest` | must ship hauth and the startup script |
| CPU / Memory / Home disk size | 2 cores / 2 GB / 10 GB | pod limits and PVC size |
| Agent harness | `all` | which harness CLIs `mise` installs at startup; `none` skips HAPI and hauth |
| HAPI CLI API token | `token` | passed to HAPI as `CLI_API_TOKEN`; change it for shared or sensitive environments |
| Credential store | `file` | see above; immutable per workspace |

Template variables: `namespace` (required), `use_kubeconfig`, `workspace_apps_domain`, `workspace_service_account`, `secret_prefix`, `enable_github_external_auth`, `github_external_auth_id`.

## GitHub external auth

With `enable_github_external_auth = true`, the template reads the token from the configured Coder external auth provider and exports it as `GH_TOKEN`. hauth detects the env var, shows the `gh` row as "managed externally", and does not store, refresh, or revoke it. This is the intended division of labor, not a limitation.

## Opening the apps

After the workspace starts with any harness mode except `none`, open **HAPI** from the Coder dashboard to drive agent sessions, and **Agent Auth** to authenticate GitHub or an AI harness before launching sessions.

## Credentials

No provider credentials are committed by this template. Users authenticate through each tool's normal login flow, brokered by hauth, or provide per-user environment variables and mounted secrets, which hauth detects and leaves alone. With the `kubernetes` store, bundles live in per-user Secrets guarded by RBAC; with the `file` store, they stay on the user's home volume.

## Known limitations

Live HAPI sessions do not survive pod deletion; this template runs a per-workspace HAPI hub only. Tool installation depends on the configured `mise` aliases. The `file` store does not synchronize credentials across workspaces; use the `kubernetes` store for that.
