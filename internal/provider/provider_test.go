package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestProviderSchemaAndConfiguration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := providerserver.NewProtocol6(New())()
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	requireNoProtocolErrors(t, schemas.Diagnostics)
	names := []string{"artifact_download", "artifact_maven_download", "artifact_pypi_download", "artifact_zip"}
	if len(schemas.ResourceSchemas) != len(names) {
		t.Fatalf("provider exposes %d resources, want %d", len(schemas.ResourceSchemas), len(names))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			resourceSchema, exists := schemas.ResourceSchemas[name]
			if !exists || resourceSchema == nil || resourceSchema.Block == nil {
				t.Fatalf("missing resource schema for %q", name)
			}
			attributes := map[string]*tfprotov6.SchemaAttribute{}
			for _, attribute := range resourceSchema.Block.Attributes {
				attributes[attribute.Name] = attribute
			}
			if id := attributes["id"]; id == nil || !id.Computed {
				t.Error("resource must expose a computed ID")
			}
			if name != "artifact_zip" {
				if password := attributes["password"]; password == nil || !password.Sensitive {
					t.Error("password must be marked sensitive in the public protocol schema")
				}
				if token := attributes["bearer_token"]; token != nil && !token.Sensitive {
					t.Error("bearer_token must be marked sensitive in the public protocol schema")
				}
			}
		})
	}

	if schemas.Provider == nil {
		t.Fatal("missing provider schema")
	}
	configType := schemas.Provider.ValueType()
	config, err := tfprotov6.NewDynamicValue(configType, tftypes.NewValue(configType, map[string]tftypes.Value{}))
	if err != nil {
		t.Fatal(err)
	}
	configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{
		TerraformVersion: "1.5.0", Config: &config,
	})
	if err != nil {
		t.Fatal(err)
	}
	requireNoProtocolErrors(t, configured.Diagnostics)
}

func requireNoProtocolErrors(t *testing.T, diagnostics []*tfprotov6.Diagnostic) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
			t.Errorf("%s: %s", diagnostic.Summary, diagnostic.Detail)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}
