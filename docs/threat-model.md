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
- `Origin` and `Host` headers are validated against `HAUTH_ALLOWED_ORIGINS` to stop cross-origin requests from a browser tab reaching the forwarded port. Loopback origins are always allowed so tunnels work.

### What the ServiceAccount can read

The k8s store backend uses a per-workspace ServiceAccount bound to a Role scoped to the user's own Secret (`harness-creds-<user>`). Because Kubernetes cannot restrict `create` by resource name, the recommended setup pre-creates each user's Secret and grants only `get`/`update`/`delete` with `resourceNames`. A compromised workspace can then read and overwrite its own user's credentials, which it could do anyway via the files on disk, and nothing of any other user.

### Store contents

Bundles are plaintext inside the Secret (base64 like any Secret data). Protection is RBAC plus whatever encryption-at-rest the cluster configures for Secrets. hauth does not add its own encryption layer in v1; a Vault backend is the planned path for stronger guarantees.

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
