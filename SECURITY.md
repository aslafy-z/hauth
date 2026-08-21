# Security policy

## Supported versions

| Version | Supported |
|---|---|
| latest v0.x release | yes |
| anything older | no |

v0 moves fast; fixes land in the next release rather than being backported.

## Reporting a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/aslafy-z/hauth/security/advisories/new). Do not open a public issue for security reports.

Reports that are in scope, with the threat model in [docs/threat-model.md](docs/threat-model.md) as the reference:

- Kiosk Origin/Host validation bypass, or any way to drive the kiosk cross-origin.
- Escaping the store RBAC boundary: reading or writing another user's Secret through hauth.
- Redaction bypass: credential material reaching the ring buffer, SSE stream, or logs unmasked.
- Bundle path traversal: a crafted bundle writing outside `$HOME` or outside the adapter's declared paths.

Out of scope: attacks requiring code execution inside the workspace as the workspace user (that position already owns the credential files hauth manages), and the kiosk having no authentication of its own (documented design; the front door authenticates).
