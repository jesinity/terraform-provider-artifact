package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	tfsdkpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type DownloadResource struct{}

func NewDownloadResource() resource.Resource { return &DownloadResource{} }

type DownloadModel struct {
	ID types.String `tfsdk:"id"`

	URL        types.String `tfsdk:"url"`
	OutputPath types.String `tfsdk:"output_path"`

	// Optional auth
	Username    types.String `tfsdk:"username"`
	Password    types.String `tfsdk:"password"`
	BearerToken types.String `tfsdk:"bearer_token"`

	FollowRedirects types.Bool  `tfsdk:"follow_redirects"`
	TimeoutSeconds  types.Int64 `tfsdk:"timeout_seconds"`

	// Refresh behaviour
	// - "none" (default): do not check disk on refresh
	// - "missing": if output_path is missing, mark for recreate
	// - "sha256": if missing OR local sha256 != state sha256, mark for recreate
	RefreshStrategy types.String `tfsdk:"refresh_strategy"`

	// Computed
	ResolvedURL     types.String `tfsdk:"resolved_url"`
	DownloadSHA256  types.String `tfsdk:"download_sha256"`
	DownloadSize    types.Int64  `tfsdk:"download_size_bytes"`
	ETag            types.String `tfsdk:"etag"`
	LastModified    types.String `tfsdk:"last_modified"`
	DownloadedAtUTC types.String `tfsdk:"downloaded_at_utc"`
}

func (r *DownloadResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_download"
}

func (r *DownloadResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true},
			"url":         schema.StringAttribute{Required: true},
			"output_path": schema.StringAttribute{Required: true},

			"username":     schema.StringAttribute{Optional: true},
			"password":     schema.StringAttribute{Optional: true, Sensitive: true},
			"bearer_token": schema.StringAttribute{Optional: true, Sensitive: true},

			"follow_redirects": schema.BoolAttribute{Optional: true, Computed: true},
			"timeout_seconds":  schema.Int64Attribute{Optional: true, Computed: true},

			"refresh_strategy": schema.StringAttribute{
				Optional: true,
				Computed: true,
			},

			"resolved_url":        schema.StringAttribute{Computed: true},
			"download_sha256":     schema.StringAttribute{Computed: true},
			"download_size_bytes": schema.Int64Attribute{Computed: true},
			"etag":                schema.StringAttribute{Computed: true},
			"last_modified":       schema.StringAttribute{Computed: true},
			"downloaded_at_utc":   schema.StringAttribute{Computed: true},
		},
	}
}

func (r *DownloadResource) Configure(context.Context, resource.ConfigureRequest, *resource.ConfigureResponse) {}

func (r *DownloadResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan DownloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	applyDownloadDefaults(&plan)

	resolved, err := normalizeURL(plan.URL.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid URL", err.Error())
		return
	}

	if err := os.MkdirAll(filepath.Dir(plan.OutputPath.ValueString()), 0o755); err != nil {
		resp.Diagnostics.AddError("Failed to create output directory", err.Error())
		return
	}

	sha, size, etag, lm, dlAt, err := downloadToPath(ctx, resolved, plan.OutputPath.ValueString(), plan)
	if err != nil {
		resp.Diagnostics.AddError("Download failed", err.Error())
		return
	}

	plan.ID = types.StringValue(stableDownloadID(plan))
	plan.ResolvedURL = types.StringValue(resolved)
	plan.DownloadSHA256 = types.StringValue(sha)
	plan.DownloadSize = types.Int64Value(size)
	plan.ETag = stringOrNull(etag)
	plan.LastModified = stringOrNull(lm)
	plan.DownloadedAtUTC = types.StringValue(dlAt.UTC().Format(time.RFC3339))

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *DownloadResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state DownloadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	applyDownloadDefaults(&state)

	// Default: do NOT touch disk. This keeps the resource "ephemeral" and avoids re-downloading
	// just because the working directory was cleaned.
	switch strings.ToLower(strings.TrimSpace(state.RefreshStrategy.ValueString())) {
	case "none":
		// no-op
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
		if err != nil || (!state.DownloadSHA256.IsNull() && state.DownloadSHA256.ValueString() != "" && sha != state.DownloadSHA256.ValueString()) {
			resp.State.RemoveResource(ctx)
			return
		}
	default:
		resp.Diagnostics.AddError("Invalid refresh_strategy", `refresh_strategy must be one of: "none", "missing", "sha256"`)
		return
	}

	// Keep resolved_url normalized/stable.
	if u, err := normalizeURL(state.URL.ValueString()); err == nil {
		state.ResolvedURL = types.StringValue(u)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DownloadResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan DownloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	applyDownloadDefaults(&plan)

	resolved, err := normalizeURL(plan.URL.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid URL", err.Error())
		return
	}

	if err := os.MkdirAll(filepath.Dir(plan.OutputPath.ValueString()), 0o755); err != nil {
		resp.Diagnostics.AddError("Failed to create output directory", err.Error())
		return
	}

	sha, size, etag, lm, dlAt, err := downloadToPath(ctx, resolved, plan.OutputPath.ValueString(), plan)
	if err != nil {
		resp.Diagnostics.AddError("Download failed", err.Error())
		return
	}

	plan.ID = types.StringValue(stableDownloadID(plan))
	plan.ResolvedURL = types.StringValue(resolved)
	plan.DownloadSHA256 = types.StringValue(sha)
	plan.DownloadSize = types.Int64Value(size)
	plan.ETag = stringOrNull(etag)
	plan.LastModified = stringOrNull(lm)
	plan.DownloadedAtUTC = types.StringValue(dlAt.UTC().Format(time.RFC3339))

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *DownloadResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state DownloadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_ = os.Remove(state.OutputPath.ValueString())
	resp.State.RemoveResource(ctx)
}

func (r *DownloadResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, tfsdkpath.Root("id"), req.ID)...)
}

func applyDownloadDefaults(m *DownloadModel) {
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

func httpClientFromDownload(m DownloadModel) *http.Client {
	timeout := time.Duration(m.TimeoutSeconds.ValueInt64()) * time.Second
	c := &http.Client{Timeout: timeout}
	if !m.FollowRedirects.ValueBool() {
		c.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return c
}

func applyAuthHeader(req *http.Request, username, password, bearer types.String) {
	if !bearer.IsNull() && bearer.ValueString() != "" {
		req.Header.Set("Authorization", "Bearer "+bearer.ValueString())
		return
	}
	if !username.IsNull() && username.ValueString() != "" &&
		!password.IsNull() && password.ValueString() != "" {
		req.SetBasicAuth(username.ValueString(), password.ValueString())
	}
}

func downloadToPath(ctx context.Context, resolvedURL, out string, m DownloadModel) (sha string, size int64, etag, lastModified string, downloadedAt time.Time, err error) {
	client := httpClientFromDownload(m)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, resolvedURL, nil)
	if err != nil {
		return "", 0, "", "", time.Time{}, err
	}
	applyAuthHeader(httpReq, m.Username, m.Password, m.BearerToken)

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return "", 0, "", "", time.Time{}, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return "", 0, "", "", time.Time{}, fmt.Errorf("HTTP %d from %s", httpResp.StatusCode, resolvedURL)
	}

	if et := httpResp.Header.Get("ETag"); et != "" {
		etag = et
	}
	if lm := httpResp.Header.Get("Last-Modified"); lm != "" {
		lastModified = lm
	}

	// Write atomically.
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", 0, "", "", time.Time{}, err
	}
	tmp := out + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return "", 0, "", "", time.Time{}, err
	}

	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, hasher), httpResp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return "", 0, "", "", time.Time{}, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", 0, "", "", time.Time{}, closeErr
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return "", 0, "", "", time.Time{}, err
	}

	downloadedAt = time.Now().UTC()
	return hex.EncodeToString(hasher.Sum(nil)), n, etag, lastModified, downloadedAt, nil
}

func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("url must be non-empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" {
		return "", fmt.Errorf("url must include scheme (http/https)")
	}
	// Normalize path a bit to reduce drift in state:
	u.Path = path.Clean(u.Path)
	return u.String(), nil
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
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

func stringOrNull(s string) types.String {
	if strings.TrimSpace(s) == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func stableDownloadID(m DownloadModel) string {
	parts := []string{
		strings.TrimSpace(m.URL.ValueString()),
		strings.TrimSpace(m.OutputPath.ValueString()),
		strings.TrimSpace(m.Username.ValueString()),
		// NOTE: do not include secrets
		strings.TrimSpace(m.RefreshStrategy.ValueString()),
	}
	return strings.Join(parts, "|")
}
