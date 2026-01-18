terraform {
  required_providers {
    artifact = {
      source  = "jesinity/artifact"
      version = ">= 0.0.1"
    }
  }
}

resource "artifact_download" "s3_sink" {
  url              = "https://api.hub.confluent.io/api/plugins/confluentinc/kafka-connect-s3/versions/11.0.8/archive"
  output_path      = "${path.module}/data/s3.zip"
  follow_redirects = true
  timeout_seconds  = 120
}

output "download_sha" {
  value = artifact_download.s3_sink.download_sha256
}
