package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/jesinity/terraform-provider-artifact/internal/resources"
)

type artifactProvider struct{}

func New() provider.Provider {
	return &artifactProvider{}
}

func (p *artifactProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "artifact"
}

func (p *artifactProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{}
}

func (p *artifactProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	// No shared client in MVP.
}

func (p *artifactProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		resources.NewDownloadResource,
		resources.NewZipResource,
		resources.NewMavenDownloadResource,
		resources.NewPyPIDownloadResource,
	}
}

func (p *artifactProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}
