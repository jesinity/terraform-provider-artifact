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
  rebuild_on_read = true

  # Optional: change triggers that force replacement.
  extra_triggers = {
    upstream_sha = artifact_download.s3_sink_zip.download_sha256
  }
}

output "zip_sha" {
  value = artifact_zip.bundle.zip_sha256
}
```

The output archive and its `.tmp` file are automatically excluded from the input
files, even when `output_path` is inside `input_dir`. You do not need an explicit
exclude pattern for the archive itself.

## Ephemeral CI checkouts

Set `rebuild_on_read = true` when `output_path` is local to an ephemeral agent,
such as `${path.module}/.terraform/bundle.zip`. On every refresh, the resource
rebuilds the ZIP from its saved `input_dir`, `include`, `exclude`, and
`deterministic` inputs. If the new bytes have the saved `zip_sha256`, Terraform
keeps the resource unchanged and the ZIP is available for a downstream upload.
If the bytes differ, Terraform plans to create the resource again. A rebuild
error fails refresh instead of silently keeping an unusable path in state.

The option is disabled when omitted, preserving the existing behavior: a
missing ZIP removes the resource from state and causes a create plan. A data
source would require changing resource references and would not use the saved
archive identity to distinguish restoration from changed source content.
When enabling the option on an existing resource, first apply that configuration
while its ZIP is present: refresh reads the previous state, so it cannot use the
new setting until the setting has been saved. If the ZIP is already missing on
that first run, Terraform plans one recreation.

Use `artifact_zip.bundle.zip_sha256` as the downstream content hash so a changed
archive also updates the consumer. For example, an `aws_lambda_function` can use
`filename = artifact_zip.bundle.output_path` and
`source_code_hash = artifact_zip.bundle.zip_sha256`.

The input directory and every included source file must exist on the agent
performing refresh. Identical hashes require reproducible source bytes and ZIP
metadata; use `deterministic = true`. Refresh writes the local ZIP even for a
no-change plan, so the workspace must be writable. A saved plan applied on a
different agent needs the ZIP restored there as well.
