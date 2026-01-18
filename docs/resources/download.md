---
page_title: "artifact_download Resource"
description: "Downloads a remote artifact to a local file."
---

# artifact_download (Resource)

Downloads a URL to a local file.

## Example usage

```hcl
resource "artifact_download" "s3_sink" {
  url              = "https://api.hub.confluent.io/api/plugins/confluentinc/kafka-connect-s3/versions/11.0.8/archive"
  output_path      = "${path.module}/data/s3.zip"
  follow_redirects = true
  timeout_seconds  = 120
  # sha256         = "optional_expected_sha256"
}

output "download_sha" {
  value = artifact_download.s3_sink.download_sha256
}
```

## Arguments

- `url` (String, Required)  
  URL to download.

- `output_path` (String, Required)  
  Destination path on disk.

- `follow_redirects` (Boolean, Optional)  
  Whether redirects are followed. Default: `true`.

- `timeout_seconds` (Number, Optional)  
  Download timeout. Default is provider-defined.

- `sha256` (String, Optional)  
  Expected SHA256 of the downloaded file. If set and mismatched, apply fails.

## Attributes

- `download_sha256` (String)  
  SHA256 of the downloaded file.

- `download_size_bytes` (Number)  
  File size.

- `downloaded_at` (String)  
  Timestamp (RFC3339) of successful download.
