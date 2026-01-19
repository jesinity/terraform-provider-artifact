# artifact_download

Downloads a generic artifact over HTTP or HTTPS.

This resource is intentionally simple and deterministic. It **always downloads**
the remote artifact and records its checksum and metadata in state.

## Example

```hcl
resource "artifact_download" "example" {
  url          = "https://example.com/tool.tar.gz"
  output_path = "${path.module}/data/tool.tar.gz"
}
```

## Arguments

| Name | Description |
|----|----|
| `url` | URL of the artifact |
| `output_path` | Where the file will be written |
| `follow_redirects` | Whether to follow HTTP redirects (default: true) |
| `timeout_seconds` | Request timeout (default: 120) |

## Attributes

| Name | Description |
|----|----|
| `sha256` | SHA-256 checksum |
| `size_bytes` | Size of the downloaded file |
| `resolved_url` | Final URL after redirects |
