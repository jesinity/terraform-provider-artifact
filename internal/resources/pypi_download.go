package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PyPIDownloadResource struct{}

func NewPyPIDownloadResource() resource.Resource { return &PyPIDownloadResource{} }

type PyPIDownloadModel struct {
	ID types.String `tfsdk:"id"`

	Package types.String `tfsdk:"package"`
	Version types.String `tfsdk:"version"`

	// "wheel" (default) or "sdist"
	ArtifactType types.String `tfsdk:"artifact_type"`

	// Optional: choose an exact file if multiple match (e.g. requests-2.31.0-py3-none-any.whl)
	Filename types.String `tfsdk:"filename"`

	// Index base URL.
	// Defaults to "https://pypi.org".
	IndexURL types.String `tfsdk:"index_url"`

	// Optional auth (useful for private indexes that protect the JSON endpoint and/or the artifact itself)
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`

	FollowRedirects types.Bool  `tfsdk:"follow_redirects"`
	TimeoutSeconds  types.Int64 `tfsdk:"timeout_seconds"`

	// Refresh behaviour
	// - "none" (default): do not check disk on refresh
	// - "missing": if output_path is missing, mark for recreate
	// - "sha256": if missing OR local sha256 != state sha256, mark for recreate
	RefreshStrategy types.String `tfsdk:"refresh_strategy"`

	OutputPath types.String `tfsdk:"output_path"`

	// Computed
	ResolvedURL  types.String `tfsdk:"resolved_url"`
	SHA256       types.String `tfsdk:"sha256"`
	SizeBytes    types.Int64  `tfsdk:"size_bytes"`
	ETag         types.String `tfsdk:"etag"`
	LastModified types.String `tfsdk:"last_modified"`
}

func (r *PyPIDownloadResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pypi_download"
}

func (r *PyPIDownloadResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true},

			"package": schema.StringAttribute{Required: true},
			"version": schema.StringAttribute{Required: true},

			"artifact_type": schema.StringAttribute{Optional: true, Computed: true},
			"filename":      schema.StringAttribute{Optional: true},

			"index_url": schema.StringAttribute{Optional: true, Computed: true},

			"username": schema.StringAttribute{Optional: true},
			"password": schema.StringAttribute{Optional: true, Sensitive: true},

			"follow_redirects": schema.BoolAttribute{Optional: true, Computed: true},
			"timeout_seconds":  schema.Int64Attribute{Optional: true, Computed: true},

			"refresh_strategy": schema.StringAttribute{Optional: true, Computed: true},

			"output_path": schema.StringAttribute{Required: true},

			"resolved_url":  schema.StringAttribute{Computed: true},
			"sha256":        schema.StringAttribute{Computed: true},
			"size_bytes":    schema.Int64Attribute{Computed: true},
			"etag":          schema.StringAttribute{Computed: true},
			"last_modified": schema.StringAttribute{Computed: true},
		},
	}
}

func (r *PyPIDownloadResource) Configure(context.Context, resource.ConfigureRequest, *resource.ConfigureResponse) {}

func (r *PyPIDownloadResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PyPIDownloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	applyPyPIDefaults(&plan)

	resolved, err := resolvePyPIArtifactURL(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to resolve PyPI artifact", err.Error())
		return
	}

	sha, n, et, lm, err := doPyPIDownload(ctx, resolved, plan.OutputPath.ValueString(), plan)
	if err != nil {
		resp.Diagnostics.AddError("Download failed", err.Error())
		return
	}

	plan.ID = types.StringValue(stablePyPIID(plan))
	plan.ResolvedURL = types.StringValue(resolved)
	plan.SHA256 = types.StringValue(sha)
	plan.SizeBytes = types.Int64Value(n)
	plan.ETag = stringOrNull(et)
	plan.LastModified = stringOrNull(lm)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PyPIDownloadResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PyPIDownloadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	applyPyPIDefaults(&state)

	switch strings.ToLower(strings.TrimSpace(state.RefreshStrategy.ValueString())) {
	case "none":
	case "missing":
		if _, err := os.Stat(state.OutputPath.ValueString()); err != nil {
			resp.State.RemoveResource(ctx)
			return
		}
	case "sha256":
		p := state.OutputPath.ValueString()
		finfo, err := os.Stat(p)
		if err != nil || finfo.IsDir() {
			resp.State.RemoveResource(ctx)
			return
		}
		sha, err := sha256File(p)
		if err != nil || (!state.SHA256.IsNull() && state.SHA256.ValueString() != "" && sha != state.SHA256.ValueString()) {
			resp.State.RemoveResource(ctx)
			return
		}
	default:
		resp.Diagnostics.AddError("Invalid refresh_strategy", `refresh_strategy must be one of: "none", "missing", "sha256"`)
		return
	}

	// Keep resolved_url stable where possible (best effort).
	resolved, err := resolvePyPIArtifactURL(ctx, state)
	if err == nil && resolved != "" {
		state.ResolvedURL = types.StringValue(resolved)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PyPIDownloadResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PyPIDownloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	applyPyPIDefaults(&plan)

	resolved, err := resolvePyPIArtifactURL(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to resolve PyPI artifact", err.Error())
		return
	}

	sha, n, et, lm, err := doPyPIDownload(ctx, resolved, plan.OutputPath.ValueString(), plan)
	if err != nil {
		resp.Diagnostics.AddError("Download failed", err.Error())
		return
	}

	plan.ID = types.StringValue(stablePyPIID(plan))
	plan.ResolvedURL = types.StringValue(resolved)
	plan.SHA256 = types.StringValue(sha)
	plan.SizeBytes = types.Int64Value(n)
	plan.ETag = stringOrNull(et)
	plan.LastModified = stringOrNull(lm)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PyPIDownloadResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PyPIDownloadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_ = os.Remove(state.OutputPath.ValueString())
	resp.State.RemoveResource(ctx)
}

func (r *PyPIDownloadResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func applyPyPIDefaults(m *PyPIDownloadModel) {
	if m.IndexURL.IsNull() || m.IndexURL.IsUnknown() || strings.TrimSpace(m.IndexURL.ValueString()) == "" {
		m.IndexURL = types.StringValue("https://pypi.org")
	}
	if m.ArtifactType.IsNull() || m.ArtifactType.IsUnknown() || strings.TrimSpace(m.ArtifactType.ValueString()) == "" {
		m.ArtifactType = types.StringValue("wheel")
	}
	if m.FollowRedirects.IsNull() || m.FollowRedirects.IsUnknown() {
		m.FollowRedirects = types.BoolValue(true)
	}
	if m.TimeoutSeconds.IsNull() || m.TimeoutSeconds.IsUnknown() || m.TimeoutSeconds.ValueInt64() <= 0 {
		m.TimeoutSeconds = types.Int64Value(120)
	}
	if m.RefreshStrategy.IsNull() || m.RefreshStrategy.IsUnknown() || strings.TrimSpace(m.RefreshStrategy.ValueString()) == "" {
		m.RefreshStrategy = types.StringValue("none")
	}
}

func pypiHTTPClient(m PyPIDownloadModel) *http.Client {
	timeout := time.Duration(m.TimeoutSeconds.ValueInt64()) * time.Second
	c := &http.Client{Timeout: timeout}
	if !m.FollowRedirects.ValueBool() {
		c.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	}
	return c
}

func applyBasicAuth(req *http.Request, m PyPIDownloadModel) {
	if !m.Username.IsNull() && m.Username.ValueString() != "" &&
		!m.Password.IsNull() && m.Password.ValueString() != "" {
		req.SetBasicAuth(m.Username.ValueString(), m.Password.ValueString())
	}
}

type pypiJSON struct {
	URLs []struct {
		URL         string `json:"url"`
		Filename    string `json:"filename"`
		Packagetype string `json:"packagetype"` // bdist_wheel, sdist
	} `json:"urls"`
}

func resolvePyPIArtifactURL(ctx context.Context, m PyPIDownloadModel) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(m.IndexURL.ValueString()), "/")
	pkg := strings.TrimSpace(m.Package.ValueString())
	ver := strings.TrimSpace(m.Version.ValueString())
	if pkg == "" || ver == "" {
		return "", fmt.Errorf("package and version are required")
	}

	// Validate base URL
	if _, err := url.Parse(base); err != nil {
		return "", fmt.Errorf("invalid index_url: %w", err)
	}

	jsonURL := fmt.Sprintf("%s/pypi/%s/%s/json", base, url.PathEscape(pkg), url.PathEscape(ver))

	client := pypiHTTPClient(m)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jsonURL, nil)
	if err != nil {
		return "", err
	}
	applyBasicAuth(req, m)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d resolving %s", resp.StatusCode, jsonURL)
	}

	var payload pypiJSON
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("failed to decode PyPI JSON: %w", err)
	}

	wantType := strings.ToLower(strings.TrimSpace(m.ArtifactType.ValueString()))
	wantFilename := strings.TrimSpace(m.Filename.ValueString())

	// Choose packagetype
	targetPackagetype := ""
	switch wantType {
	case "wheel":
		targetPackagetype = "bdist_wheel"
	case "sdist":
		targetPackagetype = "sdist"
	default:
		return "", fmt.Errorf(`artifact_type must be "wheel" or "sdist"`)
	}

	// 1) Exact filename wins (still must be right packagetype)
	if wantFilename != "" {
		for _, u := range payload.URLs {
			if u.Packagetype == targetPackagetype && u.Filename == wantFilename && u.URL != "" {
				return u.URL, nil
			}
		}
		return "", fmt.Errorf("no %s found matching filename %q for %s==%s", wantType, wantFilename, pkg, ver)
	}

	// 2) Otherwise pick the first matching packagetype
	for _, u := range payload.URLs {
		if u.Packagetype == targetPackagetype && u.URL != "" {
			return u.URL, nil
		}
	}

	return "", fmt.Errorf("no %s found for %s==%s (packagetype=%s)", wantType, pkg, ver, targetPackagetype)
}

func doPyPIDownload(ctx context.Context, resolvedURL, out string, m PyPIDownloadModel) (sha string, size int64, etag, lastModified string, err error) {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", 0, "", "", err
	}

	client := pypiHTTPClient(m)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resolvedURL, nil)
	if err != nil {
		return "", 0, "", "", err
	}
	applyBasicAuth(req, m)

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, "", "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, resolvedURL)
	}

	if et := resp.Header.Get("ETag"); et != "" {
		etag = et
	}
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		lastModified = lm
	}

	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", 0, "", "", err
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hasher), resp.Body)
	if err != nil {
		_ = os.Remove(tmp)
		return "", 0, "", "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", 0, "", "", err
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return "", 0, "", "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), n, etag, lastModified, nil
}

func stablePyPIID(m PyPIDownloadModel) string {
	parts := []string{
		m.IndexURL.ValueString(),
		m.Package.ValueString(),
		m.Version.ValueString(),
		m.ArtifactType.ValueString(),
		m.Filename.ValueString(),
		m.OutputPath.ValueString(),
		m.RefreshStrategy.ValueString(),
	}
	return strings.Join(parts, "|")
}
