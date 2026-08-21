terraform {
  required_providers {
    coder = {
      source  = "coder/coder"
      version = ">= 2.4.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = ">= 2.23.0"
    }
  }
}

provider "coder" {}

variable "use_kubeconfig" {
  type        = bool
  description = <<-EOF
  Use host kubeconfig? (true/false)

  Set this to false if the Coder host is itself running as a Pod on the same
  Kubernetes cluster as you are deploying workspaces to.

  Set this to true if the Coder host is running outside the Kubernetes cluster
  for workspaces. A valid "~/.kube/config" must be present on the Coder host.
  EOF
  default     = false
}

variable "namespace" {
  type        = string
  description = "The Kubernetes namespace to create workspaces in (must exist prior to creating workspaces). If the Coder host is itself running as a Pod on the same Kubernetes cluster as you are deploying workspaces to, set this to the same namespace."
  default     = "coder-workspaces"
}

variable "enable_github_external_auth" {
  type        = bool
  description = "Export GH_TOKEN from a Coder external auth provider into workspaces. hauth then reports gh as managed externally and leaves it alone."
  default     = false
}

variable "github_external_auth_id" {
  type        = string
  description = "ID of the Coder external auth provider to read the GitHub token from."
  default     = "github"
}

provider "kubernetes" {
  config_path = var.use_kubeconfig ? "~/.kube/config" : null
}

data "coder_provisioner" "me" {}
data "coder_workspace" "me" {}
data "coder_workspace_owner" "me" {}

data "coder_external_auth" "github" {
  count = var.enable_github_external_auth ? 1 : 0
  id    = var.github_external_auth_id
}

data "coder_parameter" "workspace_image" {
  name         = "workspace_image"
  display_name = "Workspace image"
  description  = "Workspace image to run. Any image with curl and a shell works: hauth is downloaded from its latest release at startup when missing."
  type         = "string"
  mutable      = true
  default      = "codercom/enterprise-base:ubuntu"
  order        = 1
}

locals {
  cpu_options = {
    "2 Cores" = "2"
    "4 Cores" = "4"
    "6 Cores" = "6"
    "8 Cores" = "8"
  }

  memory_options = {
    "2 GB" = "2"
    "4 GB" = "4"
    "6 GB" = "6"
    "8 GB" = "8"
  }
}

data "coder_parameter" "cpu" {
  name         = "cpu"
  display_name = "CPU"
  description  = "The number of CPU cores"
  default      = "2"
  icon         = "/icon/memory.svg"
  mutable      = true
  order        = 2
  form_type    = "radio"

  dynamic "option" {
    for_each = local.cpu_options
    content {
      name  = option.key
      value = option.value
    }
  }
}

data "coder_parameter" "memory" {
  name         = "memory"
  display_name = "Memory"
  description  = "The amount of memory in GB"
  default      = "2"
  icon         = "/icon/memory.svg"
  mutable      = true
  order        = 3
  form_type    = "radio"

  dynamic "option" {
    for_each = local.memory_options
    content {
      name  = option.key
      value = option.value
    }
  }
}

data "coder_parameter" "home_disk_size" {
  name         = "home_disk_size"
  display_name = "Home disk size"
  description  = "The size of the home disk in GB"
  default      = "10"
  type         = "number"
  icon         = "/emojis/1f4be.png"
  mutable      = false
  order        = 4
  form_type    = "slider"

  validation {
    min = 1
    max = 99999
  }
}

data "coder_parameter" "agent_harness" {
  name         = "agent_harness"
  display_name = "Agent harness"
  description  = "Choose which AI agent harness CLIs hauth installs when the workspace starts. gh is always installed."
  default      = "all"
  order        = 5
  form_type    = "dropdown"

  option {
    name  = "None"
    value = "none"
  }

  option {
    name  = "Claude Code"
    value = "claude"
  }

  option {
    name  = "Antigravity CLI"
    value = "agy"
  }

  option {
    name  = "Codex"
    value = "codex"
  }

  option {
    name  = "OpenCode"
    value = "opencode"
  }

  option {
    name  = "All"
    value = "all"
  }
}

resource "coder_agent" "main" {
  os   = "linux"
  arch = data.coder_provisioner.me.arch

  metadata {
    display_name = "CPU Usage"
    key          = "0_cpu_usage"
    script       = "coder stat cpu"
    interval     = 10
    timeout      = 1
  }

  metadata {
    display_name = "RAM Usage"
    key          = "1_ram_usage"
    script       = "coder stat mem"
    interval     = 10
    timeout      = 1
  }

  metadata {
    display_name = "Home Disk"
    key          = "3_home_disk"
    script       = "coder stat disk --path $${HOME}"
    interval     = 60
    timeout      = 1
  }
}

resource "coder_script" "hauth" {
  agent_id           = coder_agent.main.id
  display_name       = "Start hauth"
  icon               = "/icon/terminal.svg"
  run_on_start       = true
  start_blocks_login = false

  script = <<-EOT
    #!/usr/bin/env bash
    set -euo pipefail

    export PATH="$HOME/.local/bin:$PATH"
    mkdir -p "$HOME/.local/bin"

    if ! command -v hauth >/dev/null 2>&1; then
      arch="$(uname -m)"
      case "$arch" in
        x86_64) arch=amd64 ;;
        aarch64 | arm64) arch=arm64 ;;
      esac
      echo "Installing hauth (linux/$arch) from the latest release"
      url="$(curl -fsSL https://api.github.com/repos/aslafy-z/hauth/releases/latest \
        | grep -o "https://[^\"]*hauth_[^\"]*_linux_$arch\.tar\.gz" | head -1)"
      curl -fsSL "$url" | tar -xz -C "$HOME/.local/bin" hauth
    fi
    hauth version

    if [ "$AGENT_HARNESS" != "none" ]; then
      case "$AGENT_HARNESS" in
        all) harnesses="gh claude agy codex opencode" ;;
        *) harnesses="gh $AGENT_HARNESS" ;;
      esac
      echo "Installing harness CLIs: $harnesses"
      # shellcheck disable=SC2086
      hauth install $harnesses || echo "some harness installs failed; the kiosk can retry them" >&2
    fi

    if ! curl -fsS http://127.0.0.1:43117/healthz >/dev/null 2>&1; then
      echo "Starting hauth agent"
      nohup hauth agent >"$HOME/.hauth-agent.log" 2>&1 &
    fi

    for _ in $(seq 1 30); do
      if curl -fsS http://127.0.0.1:43117/healthz >/dev/null 2>&1; then
        echo "hauth agent is ready"
        exit 0
      fi
      sleep 1
    done

    echo "hauth agent did not become ready" >&2
    tail -n 50 "$HOME/.hauth-agent.log" >&2 || true
    exit 1
  EOT
}

resource "coder_app" "agent_auth" {
  agent_id     = coder_agent.main.id
  slug         = "auth"
  display_name = "Agent Auth"
  icon         = "/emojis/1f511.png"

  url       = "http://127.0.0.1:43117"
  subdomain = true
  share     = "owner"

  healthcheck {
    url       = "http://127.0.0.1:43117/healthz"
    interval  = 5
    threshold = 12
  }
}

locals {
  # The store lives on the persistent home volume; point HAUTH_STORE_PATH
  # at a shared volume to sync credentials across workspaces.
  store_env = {
    HAUTH_STORE      = "file"
    HAUTH_STORE_PATH = "/home/coder/.local/share/hauth/store"
  }

  github_env = var.enable_github_external_auth ? {
    GH_TOKEN = data.coder_external_auth.github[0].access_token
  } : {}

  workspace_env = merge({
    CODER_AGENT_TOKEN = coder_agent.main.token
    AGENT_HARNESS     = data.coder_parameter.agent_harness.value
    PATH              = "/home/coder/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

    HAUTH_USER     = data.coder_workspace_owner.me.name
    HAUTH_INSTANCE = data.coder_workspace.me.name
  }, local.store_env, local.github_env)

  workspace_labels = {
    "app.kubernetes.io/name"     = "coder-workspace"
    "app.kubernetes.io/instance" = "coder-workspace-${data.coder_workspace.me.id}"
    "app.kubernetes.io/part-of"  = "coder"
    "com.coder.resource"         = "true"
    "com.coder.workspace.id"     = data.coder_workspace.me.id
    "com.coder.workspace.name"   = data.coder_workspace.me.name
    "com.coder.user.id"          = data.coder_workspace_owner.me.id
    "com.coder.user.username"    = data.coder_workspace_owner.me.name
  }
}

resource "kubernetes_persistent_volume_claim_v1" "home" {
  metadata {
    name      = "coder-${data.coder_workspace.me.id}-home"
    namespace = var.namespace
    labels = merge(local.workspace_labels, {
      "app.kubernetes.io/name"     = "coder-pvc"
      "app.kubernetes.io/instance" = "coder-pvc-${data.coder_workspace.me.id}"
    })
    annotations = {
      "com.coder.user.email" = data.coder_workspace_owner.me.email
    }
  }
  wait_until_bound = false

  spec {
    access_modes = ["ReadWriteOnce"]
    resources {
      requests = {
        storage = "${data.coder_parameter.home_disk_size.value}Gi"
      }
    }
  }
}

resource "kubernetes_deployment_v1" "workspace" {
  count = data.coder_workspace.me.start_count
  depends_on = [
    kubernetes_persistent_volume_claim_v1.home,
  ]
  wait_for_rollout = false

  metadata {
    name      = "coder-${data.coder_workspace.me.id}"
    namespace = var.namespace
    labels    = local.workspace_labels
    annotations = {
      "com.coder.user.email" = data.coder_workspace_owner.me.email
    }
  }

  spec {
    replicas = 1
    selector {
      match_labels = local.workspace_labels
    }
    strategy {
      type = "Recreate"
    }

    template {
      metadata {
        labels = local.workspace_labels
      }

      spec {
        security_context {
          run_as_user     = 1000
          fs_group        = 1000
          run_as_non_root = true
        }

        container {
          name              = "dev"
          image             = data.coder_parameter.workspace_image.value
          image_pull_policy = "Always"
          command           = ["sh", "-c", coder_agent.main.init_script]

          security_context {
            run_as_user = "1000"
          }

          dynamic "env" {
            for_each = local.workspace_env
            content {
              name  = env.key
              value = env.value
            }
          }

          resources {
            requests = {
              "cpu"    = "250m"
              "memory" = "512Mi"
            }
            limits = {
              "cpu"    = data.coder_parameter.cpu.value
              "memory" = "${data.coder_parameter.memory.value}Gi"
            }
          }

          volume_mount {
            mount_path = "/home/coder"
            name       = "home"
            read_only  = false
          }
        }

        volume {
          name = "home"
          persistent_volume_claim {
            claim_name = kubernetes_persistent_volume_claim_v1.home.metadata[0].name
            read_only  = false
          }
        }

        affinity {
          # This affinity attempts to spread out all workspace pods evenly across nodes.
          pod_anti_affinity {
            preferred_during_scheduling_ignored_during_execution {
              weight = 1
              pod_affinity_term {
                topology_key = "kubernetes.io/hostname"
                label_selector {
                  match_expressions {
                    key      = "app.kubernetes.io/name"
                    operator = "In"
                    values   = ["coder-workspace"]
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}
