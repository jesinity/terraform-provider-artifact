# artifact_download

Downloads a file via HTTP/HTTPS to a local `output_path`.

## Ephemeral-by-default semantics

This resource is designed for Terraform pipelines where the artifact is consumed immediately by other resources.

- **Create/Update**: downloads the file.
- **Read/Refresh**: by default does *not* check the filesystem.
- **Re-downloads only when inputs change** (e.g. `url`, `output_path`, `refresh_strategy`).

If you want Terraform to notice missing files (or detect local tampering), set `refresh_strategy`.

## Example

```hcl
resource "artifact_download" "s3_sink_zip" {
  url         = "https://example.com/plugin.zip"
  output_path = "${path.module}/.terraform/artifacts/plugin.zip"

  # defaults shown explicitly
  follow_redirects  = true
  timeout_seconds   = 120
  refresh_strategy  = "none" # or "missing" or "sha256"
}

output "sha256" {
  value = artifact_download.s3_sink_zip.download_sha256
}
```

## Arguments

- `url` (required) — HTTP/HTTPS URL.
- `output_path` (required) — local file path to write.
- `username` / `password` (optional) — basic auth.
- `bearer_token` (optional) — Authorization: Bearer token.
- `follow_redirects` (optional, default `true`)
- `timeout_seconds` (optional, default `120`)
- `refresh_strategy` (optional, default `"none"`) — `"none" | "missing" | "sha256"`.

## Attributes

- `resolved_url` — normalized URL stored in state.
- `download_sha256` — SHA-256 of downloaded content.
- `download_size_bytes` — size in bytes.
- `etag`, `last_modified` — if present on the HTTP response.
- `downloaded_at_utc` — timestamp of the last download.
