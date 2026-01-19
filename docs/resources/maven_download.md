# artifact_maven_download

Downloads an artifact from a Maven-compatible repository **over HTTP**.

This resource does **not invoke Maven**. It resolves coordinates deterministically
using standard Maven repository layout rules.

## Example

```hcl
resource "artifact_maven_download" "connector" {
  group_id    = "org.apache.kafka"
  artifact_id = "kafka-clients"
  version     = "3.7.0"

  output_path = "${path.module}/kafka-clients.jar"
}
```

## Supported Artifacts

- JAR (default)
- POM
- Sources JAR
- Javadoc JAR
- Custom extensions

## Arguments

| Name | Description |
|----|----|
| `group_id` | Maven groupId |
| `artifact_id` | Maven artifactId |
| `version` | Artifact version |
| `classifier` | Optional classifier |
| `kind` | `jar`, `pom`, `sources`, `javadoc`, `custom` |
| `extension` | Required if `kind = custom` |
| `repo_url` | Repository base URL (default: Maven Central) |
| `username` | Optional basic auth |
| `password` | Optional basic auth |
| `bearer_token` | Optional bearer token |
| `output_path` | Local file path |

## Attributes

| Name | Description |
|----|----|
| `resolved_url` | Fully resolved Maven URL |
| `sha256` | Checksum |
| `etag` | HTTP ETag |
