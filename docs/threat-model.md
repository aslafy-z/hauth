# Threat model

What hauth protects, what it deliberately does not, and where the trust boundaries sit.

## Assets

- Provider credentials on disk (OAuth token caches, `hosts.yml`, `auth.json` files).
- Credential bundles in the store backend.
- One-time login URLs and device codes in transit through the kiosk.

## Trust boundaries

### Who can reach the kiosk

The kiosk binds `127.0.0.1` only and refuses configuration to a non-loopback address. It has no authentication of its own. The front door (Coder `coder_app` with `share = "owner"`, an SSH tunnel, Tailscale serve) is the authentication layer, and the deployment docs say so explicitly.

Consequences:

- Any process inside the workspace running as any user that can reach loopback can drive the kiosk. In practice workspace pods are single-user; a co-resident malicious process already has the user's files, including the credential caches themselves, so the kiosk adds no privilege.
- Browser requests are held to a same-origin policy: an `Origin` header must match the request's `Host` (or be loopback, or listed in `HAUTH_ALLOWED_ORIGINS` for fronts that serve the page from a different host). This stops cross-origin requests from a hostile tab reaching the forwarded port without requiring the fronting hostname to be configured. Requests without an `Origin` pass; the fronting proxy is the authentication boundary. Residual risk: DNS rebinding against a locally forwarded port pairs a matching Origin and Host; modern browsers' local-network access protections mitigate this, and the fronting proxy is unaffected by it.

### Store contents

Bundles are plaintext files, `0600` inside `0700` directories, owned by the workspace user. Protection is filesystem permissions plus whatever encryption-at-rest the volume provides. When the store path is on storage shared between a user's workspaces, mounting that storage is the access boundary: mount it per user, never per team. hauth does not add its own encryption layer in v1; an encrypting backend is the planned path for stronger guarantees.

## Logs and redaction

- Session output passes through a redaction filter before it reaches the ring buffer, the SSE event stream, or any log line. Known token shapes (`ghp_`, `gho_`, `sk-ant-`, bearer headers, JSON fields named token/key/secret) are masked.
- The redaction guarantee covers what hauth writes. It cannot cover what a CLI writes to its own log files.
- One-time URLs and device codes are deliberately not redacted: displaying them is the product. They are short-lived and bound to the provider-side flow.

## Malicious or compromised harness CLI

hauth runs the CLIs as the workspace user, exactly as the user would in a shell. A malicious CLI update can already read every file the user can; hauth grants it no additional privilege. The pty sessions add no capability the user's own terminal lacks.

## Externally managed credentials

Credentials that arrive via env vars (`GH_TOKEN` from platform external auth, mounted secrets) are detected and reported, never stored, synced, or revoked by hauth. This is a deliberate do-no-harm rule: the tool refuses to manage material it did not create.

## Non-goals

- Protecting against a hostile workspace platform (the platform terminates TLS and owns the pod).
- Multi-user isolation inside one workspace.
- Secrecy of bundle metadata (file names, sizes, timestamps) from cluster operators.
