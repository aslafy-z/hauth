# hauth Coder snippet

Resources to paste into an existing Coder template: a `coder_script` that
starts `hauth agent` with identity mapped from Coder workspace variables, an
owner-only `coder_app` for the kiosk, and an optional GitHub external auth
block exporting `GH_TOKEN`.

This is not an importable template on its own: it declares an `agent_id`
variable you must wire to your template's `coder_agent` resource, and it
provisions no workspace. For a complete, ready-to-import template, use
[deploy/coder-hapi](../coder-hapi/) or the `hauth-coder-hapi-template.zip`
asset on each release.

The workspace image must ship the hauth binary
(`ghcr.io/aslafy-z/hauth-hapi` has it at `/usr/local/bin/hauth`).

Walkthrough: [docs/coder.md](../../docs/coder.md).
