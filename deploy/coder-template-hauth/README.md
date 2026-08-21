---
display_name: Coder hauth Workspace Template
description: Provision Kubernetes pods with persistent home volumes, hauth-brokered harness authentication, and AI agent harness CLIs
icon: /icon/k8s.svg
verified: false
tags: [kubernetes, hauth, ai, agents]
---

# Coder hauth Workspace Template

This template provisions a Kubernetes pod as a Coder workspace with a persistent home volume and [hauth](https://github.com/aslafy-z/hauth) as the authentication companion for AI agent harness CLIs: browser-free login brokering plus credential persistence across workspace restarts. It is the HAPI-free sibling of [deploy/coder-template-hapi-hauth](../coder-template-hapi-hauth/).

## Importing the template

Two equivalent paths:

- Download `coder-template-hauth.zip` from the [latest release](https://github.com/aslafy-z/hauth/releases/latest) and upload it in the Coder dashboard under Templates, Create template.
- Or push this directory with the CLI: `coder templates push hauth -d deploy/coder-template-hauth`.

Review the template variables when prompted; `namespace` defaults to `coder-workspaces`.

## Prerequisites

You need an existing Kubernetes cluster and namespace for workspace resources. The namespace must exist before workspaces are created.

The Coder provisioner must be able to authenticate to the target cluster. If Coder runs inside the same cluster, use in-cluster ServiceAccount credentials (`use_kubeconfig = false`). If Coder runs outside the cluster, set `use_kubeconfig = true` and provide a valid `~/.kube/config` on the Coder host.

No purpose-built workspace image is required: the default is `codercom/enterprise-base:ubuntu`, and the startup script downloads the hauth binary from the latest GitHub release into `~/.local/bin` when it is missing. Any image with `curl` and a shell works.

## Architecture

Workspace creation creates a persistent volume claim for `/home/coder` and a Kubernetes Deployment for the running workspace pod. The persistent volume keeps the user's home directory, the hauth binary, installed harness CLIs, restored credentials, and the credential store across workspace restarts.

At startup the template's `coder_script` installs hauth if needed, installs the selected harness CLIs natively with `hauth install` (`gh` always, plus the **Agent harness** selection), and starts `hauth agent` on `127.0.0.1:43117`. The agent restores stored credentials, watches for changes, pushes them back to the store, and serves the kiosk.

## The Agent Auth app

The **Agent Auth** app is the hauth kiosk: a mobile-friendly loopback UI showing the authentication state of `gh`, `claude`, `codex`, `opencode`, and `agy`, with one button per browser-free login method. URLs and one-time codes are tap-to-copy; paste-code flows have an input field; a terminal panel is available but hidden by default. The kiosk enforces a same-origin policy on browser requests, so the proxied Coder app and SSH tunnels both work without hostname configuration.

The kiosk has no authentication of its own. The Coder app is `share = "owner"`; that owner-only sharing is the access control.

## Credential store

hauth keeps credential bundles under `/home/coder/.local/share/hauth/store` on the persistent home volume, so logins survive workspace restarts. To share credentials across all of a user's workspaces, mount per-user shared storage into each workspace and set `HAUTH_STORE_PATH` to it.

## Parameters

| Parameter | Default | Notes |
|---|---|---|
| Workspace image | `codercom/enterprise-base:ubuntu` | any image with curl and a shell |
| CPU / Memory / Home disk size | 2 cores / 2 GB / 10 GB | pod limits and PVC size |
| Agent harness | `all` | which harness CLIs `hauth install` sets up at startup; `gh` is always installed; `none` skips installs |

Template variables: `namespace` (default `coder-workspaces`), `use_kubeconfig`, `enable_github_external_auth`, `github_external_auth_id`.

## GitHub external auth

With `enable_github_external_auth = true`, the template reads the token from the configured Coder external auth provider and exports it as `GH_TOKEN`. hauth detects the env var, shows the `gh` row as "managed externally", and does not store, refresh, or revoke it.

## Credentials

No provider credentials are committed by this template. Users authenticate through each tool's normal login flow, brokered by hauth, or provide per-user environment variables and mounted secrets, which hauth detects and leaves alone. Credential bundles stay on the user's home volume, `0600` and owned by the workspace user.

## Known limitations

Harness CLI installation depends on each project's release or install-script availability. Credentials follow the user across workspaces only when the store path is on storage mounted in all of them. For a workspace that also runs a HAPI hub and runner, use [deploy/coder-template-hapi-hauth](../coder-template-hapi-hauth/) instead.
