---
page_title: "artifact_zip Resource"
description: "Packages a directory into a ZIP file."
---

# artifact_zip (Resource)

Creates a ZIP archive from a directory.

## Example usage

```hcl
resource "artifact_zip" "msk_plugin" {
  input_dir   = "${path.module}/data/confluentinc-kafka-connect-s3-11.0.8"
  output_path = "${path.module}/data/msk-s3-plugin.zip"
  include     = ["**/*"]
}

output "zip_sha" {
  value = artifact_zip.msk_plugin.zip_sha256
}
```

## Arguments

- `input_dir` (String, Required)  
  Directory to zip.

- `output_path` (String, Required)  
  Output zip file path.

- `include` (List(String), Optional)  
  Glob patterns to include (implementation-defined).  
  If omitted, defaults to including everything.

## Attributes

- `zip_sha256` (String)  
  SHA256 of the created ZIP.

- `zip_size_bytes` (Number)  
  Output zip size.

- `zip_file_count` (Number)  
  Files included in the zip.
