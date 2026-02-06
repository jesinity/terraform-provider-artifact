# Artifact Provider

This provider offers **small, boring building blocks** for Terraform plans that need an artifact file locally.

The key design choice is **ephemeral local artifacts by default**:

- The provider downloads an artifact during `create` / `update`.
- On subsequent `terraform plan` / `terraform apply`, the provider **does not check whether the file still exists** (by default).
- The artifact is **re-downloaded only when resource inputs change** (e.g., URL, Maven coordinates, PyPI package/version, etc.).

This is intentionally closer to “**content-addressed input**” than “declarative file presence”.
It works well in CI pipelines where the downloaded artifact is *immediately consumed by other resources* (e.g., uploaded to S3, used as a plugin ZIP, etc.) and the workspace is ephemeral.

## Refresh behaviour

All download resources support `refresh_strategy`:

- `"none"` (default): do not touch disk on refresh. Terraform will *not* force a re-download just because a file vanished.
- `"missing"`: if `output_path` is missing on refresh, Terraform will mark the resource for recreation (and re-download on apply).
- `"sha256"`: if missing OR the local file’s sha256 differs from the stored sha256, Terraform will mark for recreation.

This lets you decide how “declarative” you want local artifacts to be.

## Resources

- `artifact_download` — HTTP/HTTPS download to `output_path`.
- `artifact_maven_download` — Maven artifact download (Maven Central by default).
- `artifact_pypi_download` — PyPI artifact download (public PyPI by default).
- `artifact_zip` — Create deterministic ZIP files.

See each resource page for attributes and examples.
