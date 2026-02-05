# artifact_maven_download

Downloads a Maven artifact to a local `output_path`.

Defaults:
- `repo_url` defaults to Maven Central (`https://repo1.maven.org/maven2/`)
- `kind` defaults to `jar`

## Ephemeral-by-default semantics

- **Create/Update**: downloads the artifact.
- **Read/Refresh**: by default does *not* check whether the file still exists.
- **Re-downloads only when inputs change** (coordinates, classifier, kind, etc.).

If you need Terraform to notice missing files (or detect changes), set `refresh_strategy`.

## Example

```hcl
resource "artifact_maven_download" "connector" {
  group_id    = "io.confluent"
  artifact_id = "kafka-connect-s3"
  version     = "11.0.8"

  # optional
  kind       = "jar"     # jar|pom|sources|javadoc|custom
  classifier = null      # e.g. "sources"
  repo_url   = null      # defaults to Maven Central
  refresh_strategy = "none"

  output_path = "${path.module}/.terraform/artifacts/kafka-connect-s3-11.0.8.jar"
}

output "jar_sha" {
  value = artifact_maven_download.connector.sha256
}
```

## Arguments

- `group_id`, `artifact_id`, `version` (required)
- `output_path` (required)
- `classifier` (optional)
- `repo_url` (optional, default Maven Central)
- `kind` (optional, default `jar`) — `jar|pom|sources|javadoc|custom`
- `extension` (optional) — required when `kind = "custom"`
- `username` / `password` (optional) — basic auth
- `bearer_token` (optional) — bearer auth
- `follow_redirects` (optional, default `true`)
- `timeout_seconds` (optional, default `120`)
- `refresh_strategy` (optional, default `"none"`) — `"none" | "missing" | "sha256"`.

## Attributes

- `resolved_url`
- `sha256`
- `size_bytes`
- `etag`, `last_modified`
