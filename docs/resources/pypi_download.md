# artifact_pypi_download

Downloads Python distribution artifacts from PyPI-compatible indexes
using **pure HTTP resolution**.

This resource does **not invoke pip**.

## Example

```hcl
resource "artifact_pypi_download" "requests" {
  package     = "requests"
  version     = "2.31.0"
  output_path = "${path.module}/requests.whl"
}
```

## Supported Artifacts

- Wheels (`.whl`) (default)
- Source distributions (`.tar.gz`)
- Custom filenames

## Arguments

| Name | Description |
|----|----|
| `package` | PyPI package name |
| `version` | Package version |
| `index_url` | PyPI index (default: https://pypi.org/simple) |
| `kind` | `wheel`, `sdist`, `custom` |
| `filename` | Required if `kind = custom` |
| `username` | Optional auth |
| `password` | Optional auth |
| `output_path` | Local path |

## Attributes

| Name | Description |
|----|----|
| `resolved_url` | Final artifact URL |
| `sha256` | Checksum |
| `size_bytes` | Artifact size |
