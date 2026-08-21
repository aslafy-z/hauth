---
display_name: Coder HAPI + hauth Workspace Template
description: Provision Kubernetes pods with persistent home volumes, HAPI, hauth-brokered harness authentication, and optional AI agent harnesses
icon: /icon/k8s.svg
verified: false
tags: [kubernetes, hapi, hauth, ai, agents]
---

# Coder HAPI + hauth Workspace Template

This template provisions a Kubernetes pod as a Coder workspace with a persistent home volume, a per-workspace [HAPI](https://hapi.run) hub and runner, and [hauth](https://github.com/aslafy-z/hauth) as the authentication companion for the AI agent harness CLIs: browser-free login brokering plus credential persistence across workspaces.

## Importing the template

Two equivalent paths:

- Download `hauth-coder-hapi-template.zip` from the [latest release](https://github.com/aslafy-z/hauth/releases/latest) and upload it in the Coder dashboard under Templates, Create template.
- Or push this directory with the CLI: `coder templates push hauth-hapi -d deploy/coder-hapi`.

Review the template variables when prompted; `namespace` defaults to `coder-workspaces`.

## Prerequisites

You need an existing Kubernetes cluster and namespace for workspace resources. The namespace must exist before workspaces are created.

The Coder provisioner must be able to authenticate to the target cluster. If Coder runs inside the same cluster, use in-cluster ServiceAccount credentials (`use_kubeconfig = false`). If Coder runs outside the cluster, set `use_kubeconfig = true` and provide a valid `~/.kube/config` on the Coder host.

The default workspace image is `ghcr.io/aslafy-z/hauth-hapi:latest`, built from [deploy/docker/Dockerfile.hapi](../docker/Dockerfile.hapi). Any replacement image must ship Node with the HAPI package, the hauth binary at `/usr/local/bin/hauth`, and the startup script at `/opt/hauth/start-hapi.sh`.

The hauth kiosk enforces a same-origin policy on browser requests, so the proxied Coder app and SSH tunnels both work without any hostname configuration.

## Architecture

Workspace creation creates a persistent volume claim for `/home/coder` and a Kubernetes Deployment for the running workspace pod. The persistent volume keeps the user's home directory, tool caches, HAPI state, and locally restored credentials across workspace restarts. The Deployment is ephemeral and is recreated when the workspace starts.

At startup, `/opt/hauth/start-hapi.sh` (baked into the image) installs the selected harness CLIs natively with `hauth install`, starts `hauth agent` on `127.0.0.1:43117`, starts the HAPI hub on `127.0.0.1:3006`, and starts a HAPI runner rooted at `/home/coder/project`. The hauth agent restores stored credentials before the harnesses are first used, watches for changes, and pushes them back to the credential store. A harness CLI that is still missing (for example after switching the harness selection) can also be installed on demand from the kiosk's Install button or with `hauth install`.

## The Agent Auth app

The **Agent Auth** app is the hauth kiosk: a mobile-friendly loopback UI showing the authentication state of `gh`, `claude`, `codex`, `opencode`, and `agy`, with one button per browser-free login method. URLs and one-time codes are tap-to-copy; paste-code flows have an input field; a terminal panel is available but hidden by default.

hauth also persists credentials: after a login, the credential files are bundled and written to a per-user store, and any other workspace of the same user restores them at start. Tokens injected by the platform (for example `GH_TOKEN` from external auth) or logins done by hand are reported as "managed externally" and never touched.

The kiosk has no authentication of its own. The Coder app is `share = "owner"`; that owner-only sharing is the access control.

## Credential store

hauth keeps credential bundles under `/home/coder/.local/share/hauth/store` on the persistent home volume, so logins survive workspace restarts. To share credentials across all of a user's workspaces, mount per-user shared storage into each workspace and set `HAUTH_STORE_PATH` to it; the store's lease and compare-and-swap semantics are built for that concurrent access.

## Parameters

| Parameter | Default | Notes |
|---|---|---|
| Workspace image | `ghcr.io/aslafy-z/hauth-hapi:latest` | must ship hauth and the startup script |
| CPU / Memory / Home disk size | 2 cores / 2 GB / 10 GB | pod limits and PVC size |
| Agent harness | `all` | which harness CLIs `hauth install` sets up at startup; `none` skips HAPI and hauth |
| HAPI CLI API token | `token` | passed to HAPI as `CLI_API_TOKEN`; change it for shared or sensitive environments |

Template variables: `namespace` (default `coder-workspaces`), `use_kubeconfig`, `enable_github_external_auth`, `github_external_auth_id`.

## GitHub external auth

With `enable_github_external_auth = true`, the template reads the token from the configured Coder external auth provider and exports it as `GH_TOKEN`. hauth detects the env var, shows the `gh` row as "managed externally", and does not store, refresh, or revoke it. This is the intended division of labor, not a limitation.

## Opening the apps

After the workspace starts with any harness mode except `none`, open **HAPI** from the Coder dashboard to drive agent sessions, and **Agent Auth** to authenticate GitHub or an AI harness before launching sessions.

## Credentials

No provider credentials are committed by this template. Users authenticate through each tool's normal login flow, brokered by hauth, or provide per-user environment variables and mounted secrets, which hauth detects and leaves alone. Credential bundles stay on the user's home volume, `0600` and owned by the workspace user.

## Known limitations

Live HAPI sessions do not survive pod deletion; this template runs a per-workspace HAPI hub only. Harness CLI installation depends on each project's release or install-script availability. Credentials follow the user across workspaces only when the store path is on storage mounted in all of them.
