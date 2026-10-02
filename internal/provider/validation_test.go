package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestDownloadConfigurationValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := providerserver.NewProtocol6(New())()
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	requireNoProtocolErrors(t, schemas.Diagnostics)
	for _, resourceName := range []string{"artifact_download", "artifact_maven_download", "artifact_pypi_download"} {
		t.Run(resourceName, func(t *testing.T) {
			for _, tc := range []struct {
				name      string
				attribute string
				value     any
				wantError bool
			}{
				{"omitted timeout", "timeout_seconds", nil, false},
				{"unknown timeout", "timeout_seconds", tftypes.UnknownValue, false},
				{"positive timeout", "timeout_seconds", int64(5), false},
				{"zero timeout", "timeout_seconds", int64(0), true},
				{"negative timeout", "timeout_seconds", int64(-1), true},
				{"overflowing timeout", "timeout_seconds", int64(9223372037), true},
				{"omitted strategy", "refresh_strategy", nil, false},
				{"unknown strategy", "refresh_strategy", tftypes.UnknownValue, false},
				{"none", "refresh_strategy", "none", false},
				{"missing", "refresh_strategy", "missing", false},
				{"sha256", "refresh_strategy", "sha256", false},
				{"existing normalized spelling", "refresh_strategy", " SHA256 ", false},
				{"empty strategy", "refresh_strategy", "", true},
				{"invalid strategy", "refresh_strategy", "sh256", true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					config := validationConfig(t, schemas.ResourceSchemas[resourceName], resourceName, map[string]any{tc.attribute: tc.value})
					resp, err := server.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: resourceName, Config: &config})
					if err != nil {
						t.Fatal(err)
					}
					assertValidationResult(t, resp.Diagnostics, tc.wantError)
				})
			}
		})
	}
}

func TestTrustedAuthOriginsValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := providerserver.NewProtocol6(New())()
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	requireNoProtocolErrors(t, schemas.Diagnostics)
	for _, tc := range []struct {
		value     any
		wantError bool
	}{
		{"https://cdn.example", false},
		{"https://cdn.example:8443/", false},
		{"https://[::1]:8443", false},
		{tftypes.UnknownValue, false},
		{nil, true},
		{"cdn.example", true},
		{"ftp://cdn.example", true},
		{"https://*.example", true},
		{"https://user:password@cdn.example", true},
		{"https://cdn.example/packages", true},
		{"https://cdn.example?token=abc", true},
		{"https://cdn.example#fragment", true},
		{"https://cdn.example:70000", true},
	} {
		values := []tftypes.Value{tftypes.NewValue(tftypes.String, tc.value)}
		config := validationConfig(t, schemas.ResourceSchemas["artifact_pypi_download"], "artifact_pypi_download", map[string]any{"trusted_auth_origins": values})
		resp, err := server.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: "artifact_pypi_download", Config: &config})
		if err != nil {
			t.Fatal(err)
		}
		assertValidationResult(t, resp.Diagnostics, tc.wantError)
	}
}

func validationConfig(t *testing.T, schema *tfprotov6.Schema, name string, overrides map[string]any) tfprotov6.DynamicValue {
	t.Helper()
	configured := map[string]any{"output_path": "artifact"}
	switch name {
	case "artifact_download":
		configured["url"] = "https://example.com/artifact"
	case "artifact_maven_download":
		configured["group_id"], configured["artifact_id"], configured["version"] = "org.example", "demo", "1.0"
	case "artifact_pypi_download":
		configured["package"], configured["version"] = "demo", "1.0"
	}
	for name, value := range overrides {
		configured[name] = value
	}
	valueType := schema.ValueType().(tftypes.Object)
	values := map[string]tftypes.Value{}
	for name, attributeType := range valueType.AttributeTypes {
		values[name] = tftypes.NewValue(attributeType, configured[name])
	}
	config, err := tfprotov6.NewDynamicValue(valueType, tftypes.NewValue(valueType, values))
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func assertValidationResult(t *testing.T, diagnostics []*tfprotov6.Diagnostic, wantError bool) {
	t.Helper()
	hasError := false
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
			hasError = true
		}
	}
	if hasError != wantError {
		for _, diagnostic := range diagnostics {
			t.Logf("%s: %s", diagnostic.Summary, diagnostic.Detail)
		}
		t.Fatalf("validation hasError = %t, want %t", hasError, wantError)
	}
}
