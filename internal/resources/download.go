package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type DownloadResource struct{}

func NewDownloadResource() resource.Resource {
	return &DownloadResource{}
}

type DownloadModel struct {
	ID              types.String `tfsdk:"id"`
	URL             types.String `tfsdk:"url"`
	OutputPath      types.String `tfsdk:"output_path"`
	SHA256Expected  types.String `tfsdk:"sha256"`
	FollowRedirects types.Bool   `tfsdk:"follow_redirects"`
	TimeoutSeconds  types.Int64  `tfsdk:"timeout_seconds"`

	ResolvedURL    types.String `tfsdk:"resolved_url"`
	DownloadSHA256 types.String `tfsdk:"download_sha256"`
	FileSize       types.Int64  `tfsdk:"file_size"`
}

func (r *DownloadResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_download"
}

func (r *DownloadResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},

			"url": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"output_path": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"sha256": schema.StringAttribute{
				Optional: true,
			},
			"follow_redirects": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},
			"timeout_seconds": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(60),
			},

			"resolved_url": schema.StringAttribute{
				Computed: true,
			},
			"download_sha256": schema.StringAttribute{
				Computed: true,
			},
			"file_size": schema.Int64Attribute{
				Computed: true,
			},
		},
	}
}

func (r *DownloadResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan DownloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resolved, sha, size, err := downloadToFile(
		plan.URL.ValueString(),
		plan.OutputPath.ValueString(),
		plan.SHA256Expected.ValueString(),
		plan.FollowRedirects.ValueBool(),
		time.Duration(plan.TimeoutSeconds.ValueInt64())*time.Second,
	)
	if err != nil {
		resp.Diagnostics.AddError("Download failed", err.Error())
		return
	}

	plan.ID = types.StringValue(fmt.Sprintf("%s:%s", plan.OutputPath.ValueString(), sha))
	plan.ResolvedURL = types.StringValue(resolved)
	plan.DownloadSHA256 = types.StringValue(sha)
	plan.FileSize = types.Int64Value(size)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *DownloadResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state DownloadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	p := state.OutputPath.ValueString()
	fi, err := os.Stat(p)
	if err != nil {
		// File disappeared; force recreate
		resp.State.RemoveResource(ctx)
		return
	}
	sha, err := fileSHA256(p)
	if err != nil {
		resp.Diagnostics.AddError("Failed to hash file", err.Error())
		return
	}

	state.FileSize = types.Int64Value(fi.Size())
	state.DownloadSHA256 = types.StringValue(sha)
	// ResolvedURL stays whatever we stored last time.

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DownloadResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan DownloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resolved, sha, size, err := downloadToFile(
		plan.URL.ValueString(),
		plan.OutputPath.ValueString(),
		plan.SHA256Expected.ValueString(),
		plan.FollowRedirects.ValueBool(),
		time.Duration(plan.TimeoutSeconds.ValueInt64())*time.Second,
	)
	if err != nil {
		resp.Diagnostics.AddError("Download failed", err.Error())
		return
	}

	plan.ID = types.StringValue(fmt.Sprintf("%s:%s", plan.OutputPath.ValueString(), sha))
	plan.ResolvedURL = types.StringValue(resolved)
	plan.DownloadSHA256 = types.StringValue(sha)
	plan.FileSize = types.Int64Value(size)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *DownloadResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state DownloadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_ = os.Remove(state.OutputPath.ValueString())
	// Remove from state.
	resp.State.RemoveResource(ctx)
}

// --- helpers ---

func downloadToFile(url, outPath, expectedSHA string, followRedirects bool, timeout time.Duration) (resolved string, sha string, size int64, err error) {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", "", 0, fmt.Errorf("mkdir: %w", err)
	}

	tmp := outPath + ".tmp"

	client := &http.Client{Timeout: timeout}
	if !followRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", "", 0, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", 0, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	f, err := os.Create(tmp)
	if err != nil {
		return "", "", 0, fmt.Errorf("create tmp: %w", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}()

	h := sha256.New()
	w := io.MultiWriter(f, h)

	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return "", "", 0, fmt.Errorf("download write: %w", err)
	}

	sum := hex.EncodeToString(h.Sum(nil))

	if expectedSHA != "" && sum != expectedSHA {
		return "", "", 0, fmt.Errorf("sha256 mismatch: expected %s got %s", expectedSHA, sum)
	}

	if err := f.Close(); err != nil {
		return "", "", 0, fmt.Errorf("close tmp: %w", err)
	}

	// Atomic replace
	if err := os.Rename(tmp, outPath); err != nil {
		return "", "", 0, fmt.Errorf("rename: %w", err)
	}

	return resp.Request.URL.String(), sum, n, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
