# artifact_zip

Creates a ZIP archive from a directory.

This resource is deterministic when `deterministic = true`:
- stable file ordering
- stable timestamps/metadata (implementation-specific)
- produces repeatable `zip_sha256` outputs in CI pipelines

## Example

```hcl
resource "artifact_zip" "bundle" {
  input_dir   = "${path.module}/data"
  output_path = "${path.module}/dist/bundle.zip"

  include       = ["**/*"]
  exclude       = ["**/*.tmp", "bundle.zip"]
  deterministic = true
}

output "zip_sha" {
  value = artifact_zip.bundle.zip_sha256
}
```
