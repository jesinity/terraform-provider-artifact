# artifact_zip

Packages a directory into a ZIP archive.

This is useful for producing deployment artifacts such as:
- AWS Lambda bundles
- Kafka Connect plugins
- MSK custom plugins
- Generic uploadable artifacts

## Example

```hcl
resource "artifact_zip" "plugin" {
  input_dir  = "${path.module}/plugin"
  output_path = "${path.module}/plugin.zip"
}
```

## Arguments

| Name | Description |
|----|----|
| `input_dir` | Directory to package |
| `output_path` | Path to resulting ZIP file |
| `include` | Glob patterns to include |
| `exclude` | Glob patterns to exclude |

## Attributes

| Name | Description |
|----|----|
| `file_count` | Number of files included |
| `zip_size_bytes` | Size of ZIP |
| `sha256` | ZIP checksum |
