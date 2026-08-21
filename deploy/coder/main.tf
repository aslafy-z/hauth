# hauth Coder template snippet.
#
# Paste these resources into an existing Coder template and wire `agent_id`
# to your coder_agent resource. The hauth binary must exist in the workspace
# image (ghcr.io/aslafy-z/hauth-hapi ships it at /usr/local/bin/hauth).
# For a complete, importable Kubernetes template with HAPI and hauth wired
# together, see ../coder-hapi instead.

variable "agent_id" {
  description = "ID of the coder_agent resource that runs the workspace"
  type        = string
}

variable "hauth_listen" {
  description = "Loopback address the hauth kiosk binds to"
  type        = string
  default     = "127.0.0.1:43117"
}

variable "hauth_store" {
  description = "Store backend: file or k8s"
  type        = string
  default     = "file"
}

variable "hauth_namespace" {
  description = "Namespace of the per-user credentials Secret (k8s store)"
  type        = string
  default     = ""
}

data "coder_workspace" "me" {}
data "coder_workspace_owner" "me" {}

resource "coder_script" "hauth_agent" {
  agent_id     = var.agent_id
  display_name = "hauth agent"
  icon         = "/icon/key.svg"
  run_on_start = true

  script = <<-EOT
    #!/usr/bin/env bash
    set -euo pipefail

    export HAUTH_USER="${data.coder_workspace_owner.me.name}"
    export HAUTH_INSTANCE="${data.coder_workspace.me.name}"
    export HAUTH_ALLOWED_ORIGINS="${data.coder_workspace.me.access_url}"
    export HAUTH_LISTEN="${var.hauth_listen}"
    export HAUTH_STORE="${var.hauth_store}"
    export HAUTH_NAMESPACE="${var.hauth_namespace}"

    if curl -fsS "http://$${HAUTH_LISTEN}/healthz" >/dev/null 2>&1; then
      echo "hauth agent already running"
      exit 0
    fi

    nohup hauth agent >"$${HOME}/.hauth-agent.log" 2>&1 &

    for _ in $(seq 1 30); do
      if curl -fsS "http://$${HAUTH_LISTEN}/healthz" >/dev/null 2>&1; then
        echo "hauth agent is ready"
        exit 0
      fi
      sleep 1
    done

    echo "hauth agent did not become ready" >&2
    tail -n 50 "$${HOME}/.hauth-agent.log" >&2 || true
    exit 1
  EOT
}

resource "coder_app" "hauth" {
  agent_id     = var.agent_id
  slug         = "hauth"
  display_name = "Agent Auth"
  url          = "http://localhost:43117"
  icon         = "/icon/key.svg"
  share        = "owner"
  subdomain    = false

  healthcheck {
    url       = "http://localhost:43117/healthz"
    interval  = 10
    threshold = 6
  }
}

# Optional: platform-injected GitHub token. hauth detects GH_TOKEN and shows
# the gh harness as "managed externally"; it never stores or revokes it.
#
# data "coder_external_auth" "github" {
#   id = "github"
# }
#
# then add to the coder_agent env:
#   GH_TOKEN = data.coder_external_auth.github.access_token
