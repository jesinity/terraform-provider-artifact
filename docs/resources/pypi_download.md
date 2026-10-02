# artifact_pypi_download

Resolves and downloads a PyPI artifact to a local `output_path`, using HTTP only.

Defaults:
- `index_url` defaults to public PyPI (`https://pypi.org`)
- `artifact_type` defaults to `wheel`

## How it works

1. The resource fetches the PyPI JSON metadata:
   - `${index_url}/pypi/<package>/<version>/json`
2. It selects an artifact URL:
   - `artifact_type = "wheel"` → `packagetype = "bdist_wheel"`
   - `artifact_type = "sdist"` → `packagetype = "sdist"`
   - If `filename` is set, it must match exactly (and still match the packagetype).
3. It downloads the selected artifact to `output_path`.

## Ephemeral-by-default semantics

- **Create/Update**: resolves + downloads the artifact.
- **Read/Refresh**: by default does *not* check whether the file still exists.
- **Re-downloads only when inputs change**.

If you want Terraform to notice missing files (or detect local changes), set `refresh_strategy`.

## Examples

### Public PyPI wheel (default)

```hcl
resource "artifact_pypi_download" "requests" {
  package     = "requests"
  version     = "2.31.0"
  output_path = "${path.module}/.terraform/artifacts/requests-2.31.0.whl"
}
```

### Pin an exact filename

```hcl
resource "artifact_pypi_download" "requests" {
  package     = "requests"
  version     = "2.31.0"
  artifact_type = "wheel"
  filename    = "requests-2.31.0-py3-none-any.whl"
  output_path = "${path.module}/.terraform/artifacts/requests.whl"
}
```

### Private index (basic auth)

```hcl
resource "artifact_pypi_download" "internal_pkg" {
  index_url   = "https://pypi.example.com"
  package     = "my-internal-lib"
  version     = "1.2.3"
  username    = var.pypi_user
  password    = var.pypi_pass
  output_path = "${path.module}/.terraform/artifacts/my-internal-lib-1.2.3.whl"
}
```

## Arguments

- `package`, `version` (required)
- `output_path` (required)
- `artifact_type` (optional, default `"wheel"`) — `"wheel" | "sdist"`
- `filename` (optional) — exact match selector when multiple artifacts exist
- `index_url` (optional, default `https://pypi.org`)
- `username` / `password` (optional) — basic auth for requests to the index origin and explicitly trusted origins
- `trusted_auth_origins` (optional, set of strings) — additional origins allowed to receive these credentials
- `follow_redirects` (optional, default `true`)
- `timeout_seconds` (optional, default `120`) — integer from `1` through `9223372036`
- `refresh_strategy` (optional, default `"none"`) — `"none" | "missing" | "sha256"`.

## Attributes

- `resolved_url`
- `sha256`
- `size_bytes`
- `etag`, `last_modified`


## Credential scope

The provider sends the configured username/password only to the `index_url`
origin. An origin includes the scheme, hostname, and effective port. This rule
also applies to redirects: a different port, subdomain, or HTTPS-to-HTTP redirect
does not receive credentials automatically. A public artifact on another origin
can still be downloaded without authentication.

If an additional artifact server requires the same credentials, authorize its
exact origin explicitly:

```hcl
resource "artifact_pypi_download" "internal_pkg" {
  index_url   = "https://pypi.example.com"
  package     = "my-internal-lib"
  version     = "1.2.3"
  username    = var.pypi_user
  password    = var.pypi_pass
  output_path = "${path.module}/.terraform/artifacts/internal.whl"

  trusted_auth_origins = ["https://packages.example.com:8443"]
}
```

Entries must be HTTP(S) origins without credentials, paths, queries, fragments,
or wildcard hosts. The index origin is always trusted and need not be repeated.
Existing configurations that authenticate to a separate artifact origin must
add that origin to `trusted_auth_origins`.
