terraform {
  required_providers {
    artifact = {
      source  = "jesinity/artifact"
      version = ">= 0.0.1"
    }
  }
}

resource "artifact_zip" "bundle" {
  input_dir       = "${path.module}/data/input"
  output_path     = "${path.module}/.terraform/bundle.zip"
  include         = ["**/*"]
  deterministic   = true
  rebuild_on_read = true
}

output "zip_sha" {
  value = artifact_zip.bundle.zip_sha256
}

output "zip_file_count" {
  value = artifact_zip.bundle.file_count
}

output "zip_file_size" {
  value = artifact_zip.bundle.file_size
}
