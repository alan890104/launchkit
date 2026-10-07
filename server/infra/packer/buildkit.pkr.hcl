packer {
  required_plugins {
    googlecompute = {
      version = ">= 1.1.0"
      source  = "github.com/hashicorp/googlecompute"
    }
  }
}

variable "project_id" {
  type        = string
  description = "GCP project ID"
}

variable "zone" {
  type        = string
  default     = "us-east4-c"
  description = "GCE zone for building the image"
}

variable "machine_type" {
  type        = string
  default     = "e2-standard-4"
  description = "Machine type for the build VM"
}

variable "disk_size" {
  type        = number
  default     = 100
  description = "Boot disk size in GB"
}

variable "artifact_registry_region" {
  type        = string
  default     = "us-east4"
  description = "Artifact Registry region for docker-credential-gcr setup"
}

source "googlecompute" "buildkit" {
  project_id          = var.project_id
  zone                = var.zone
  machine_type        = var.machine_type
  disk_size           = var.disk_size
  disk_type           = "pd-balanced"
  source_image_family = "ubuntu-2404-lts-amd64"
  source_image_project_id = ["ubuntu-os-cloud"]

  image_name        = "launchkit-buildkit-{{timestamp}}"
  image_family      = "launchkit-buildkit"
  image_description = "LaunchKit BuildKit VM with gVisor isolation, CNI networking, and pre-cached base images"
  image_labels = {
    managed_by = "packer"
    component  = "buildkit"
  }

  ssh_username = "packer"
  use_iap      = true

  # No public IP — IAP tunnel handles SSH during build
  omit_external_ip    = true
  use_internal_ip     = true

  # Tags for firewall rules (IAP SSH ingress)
  tags = ["packer-build"]
}

build {
  sources = ["source.googlecompute.buildkit"]

  # Create destination directory, then copy config files
  provisioner "shell" {
    inline = ["mkdir -p /tmp/configs"]
  }

  provisioner "file" {
    source      = "configs/"
    destination = "/tmp/configs/"
  }

  # Run the main provisioning script
  provisioner "shell" {
    script = "scripts/provision.sh"
    environment_vars = [
      "ARTIFACT_REGISTRY_REGION=${var.artifact_registry_region}",
    ]
    execute_command = "chmod +x {{ .Path }}; sudo -E {{ .Path }}"
  }
}
