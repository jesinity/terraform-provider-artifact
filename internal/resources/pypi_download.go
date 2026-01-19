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

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PyPIDownloadResource struct{}

func NewPyPIDownloadResource() resource.Resource {
	return &PyPIDownloadResource{}
}

type PyPIDownloadModel struct {
	ID types.String `tfsdk:"id"`

	PackageName types.String `tfsdk:"package_name"`
	Version     types.String `tfsdk:"version"` // optional; if empty -> latest

	// kind: wheel|sdist|any|custom
	// - wheel: prefer bdist_wheel
	// - sdist: prefer sdist
	// - any:   first available
	// - custom: requires filename
	Kind     types.String `tfsdk:"kind"`
	Filename types.String `tfsdk:"filename"` // required when kind=custom

	RepoURL types.String `tfsdk:"repo_url"` // default https://pypi.org

	OutputPath types.String `tfsdk:"output_path"`

	Username    types.String `tfsdk:"username"`
	Password    types.String `tfsdk:"password"`
	BearerToken types.String `tfsdk:"bearer_token"`

	FollowRedirects types.Bool  `tfsdk:"follow_redirects"`
	TimeoutSeconds  types.Int64 `tfsdk:"timeout_seconds"`

	ResolvedVersion  types.String `tfsdk:"resolved_version"`
	ResolvedFilename types.String `tfsdk:"resolved_filename"`
	ResolvedURL      types.String `tfsdk:"resolved_url"`

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

			"package_name": schema.StringAttribute{Required: true},

			// optional => latest if empty
			"version": schema.StringAttribute{Optional: true},

			"kind": schema.StringAttribute{
				Optional: true,
				Computed: true, // default wheel
			},
			"filename": schema.StringAttribute{
				Optional: true,
			},

			"repo_url": schema.StringAttribute{
				Optional: true,
				Computed: true, // default to public PyPI
			},

			"output_path": schema.StringAttribute{Required: true},

			"username":     schema.StringAttribute{Optional: true},
			"password":     schema.StringAttribute{Optional: true, Sensitive: true},
			"bearer_token": schema.StringAttribute{Optional: true, Sensitive: true},

			"follow_redirects": schema.BoolAttribute{Optional: true, Computed: true},
			"timeout_seconds":  schema.Int64Attribute{Optional: true, Computed: true},

			"resolved_version":  schema.StringAttribute{Computed: true},
			"resolved_filename": schema.StringAttribute{Computed: true},
			"resolved_url":      schema.StringAttribute{Computed: true},

			"sha256":        schema.StringAttribute{Computed: true},
			"size_bytes":    schema.Int64Attribute{Computed: true},
			"etag":          schema.StringAttribute{Computed: true},
			"last_modified": schema.StringAttribute{Computed: true},
		},
	}
}

func (r *PyPIDownloadResource) Configure(context.Context, resource.ConfigureRequest, *resource.ConfigureResponse) {
}

func (r *PyPIDownloadResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PyPIDownloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.downloadAndFillState(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PyPIDownloadResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PyPIDownloadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	applyPyPIDefaults(&state)

	// If local file is gone, let Terraform recreate.
	if _, err := os.Stat(state.OutputPath.ValueString()); err != nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PyPIDownloadResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PyPIDownloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Best-effort cleanup if output_path changed
	var prior PyPIDownloadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if !resp.Diagnostics.HasError() {
		oldPath := strings.TrimSpace(prior.OutputPath.ValueString())
		newPath := strings.TrimSpace(plan.OutputPath.ValueString())
		if oldPath != "" && newPath != "" && oldPath != newPath {
			_ = os.Remove(oldPath)
		}
	}

	r.downloadAndFillState(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

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

// ---- internals ----

type pypiJSONInfo struct {
	Version string `json:"version"`
}

type pypiJSONURL struct {
	Filename    string            `json:"filename"`
	URL         string            `json:"url"`
	Packagetype string            `json:"packagetype"` // "bdist_wheel", "sdist"
	Digests     map[string]string `json:"digests"`     // {"sha256": "..."}
	Size        int64             `json:"size"`
}

type pypiJSONResponse struct {
	Info pypiJSONInfo  `json:"info"`
	URLs []pypiJSONURL `json:"urls"`
}

func (r *PyPIDownloadResource) downloadAndFillState(ctx context.Context, plan *PyPIDownloadModel, diags *diag.Diagnostics) {
	applyPyPIDefaults(plan)

	repo := strings.TrimSpace(plan.RepoURL.ValueString())
	if repo == "" {
		repo = "https://pypi.org"
	}
	repo = strings.TrimRight(repo, "/")

	name := strings.TrimSpace(plan.PackageName.ValueString())
	if name == "" {
		diags.AddError("Invalid package_name", "package_name must be non-empty")
		return
	}

	kind := strings.ToLower(strings.TrimSpace(plan.Kind.ValueString()))
	if kind == "" {
		kind = "wheel"
	}

	out := strings.TrimSpace(plan.OutputPath.ValueString())
	if out == "" {
		diags.AddError("Invalid output_path", "output_path must be non-empty")
		return
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		diags.AddError("Failed to create output directory", err.Error())
		return
	}

	client := httpClientGeneric(plan.TimeoutSeconds, plan.FollowRedirects)

	// Resolve version (if omitted) using /pypi/{name}/json
	version := strings.TrimSpace(plan.Version.ValueString())
	if version == "" {
		metaURL := fmt.Sprintf("%s/pypi/%s/json", repo, url.PathEscape(name))
		var meta pypiJSONResponse
		if err := fetchPyPIJSON(ctx, client, metaURL, plan, &meta, diags); err != nil {
			return
		}
		version = strings.TrimSpace(meta.Info.Version)
		if version == "" {
			diags.AddError("Failed to resolve version", "PyPI JSON did not include info.version")
			return
		}
	}

	// Fetch version metadata: /pypi/{name}/{version}/json
	versionURL := fmt.Sprintf("%s/pypi/%s/%s/json", repo, url.PathEscape(name), url.PathEscape(version))
	var meta pypiJSONResponse
	if err := fetchPyPIJSON(ctx, client, versionURL, plan, &meta, diags); err != nil {
		return
	}

	chosen, err := choosePyPIArtifact(meta.URLs, kind, plan.Filename)
	if err != nil {
		diags.AddError("No matching artifact found", err.Error())
		return
	}

	resolvedURL := strings.TrimSpace(chosen.URL)
	if resolvedURL == "" {
		diags.AddError("Invalid artifact URL", "selected artifact has empty url")
		return
	}

	// Download the artifact
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, resolvedURL, nil)
	if err != nil {
		diags.AddError("Failed to create HTTP request", err.Error())
		return
	}
	applyPyPIAuth(httpReq, plan)

	httpResp, err := client.Do(httpReq)
	if err != nil {
		diags.AddError("Download failed", err.Error())
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		diags.AddError("Download failed", fmt.Sprintf("HTTP %d from %s", httpResp.StatusCode, resolvedURL))
		return
	}

	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		diags.AddError("Failed to create output file", err.Error())
		return
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hasher), httpResp.Body)
	if err != nil {
		_ = os.Remove(tmp)
		diags.AddError("Failed writing file", err.Error())
		return
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		diags.AddError("Failed closing file", err.Error())
		return
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		diags.AddError("Failed finalizing file", err.Error())
		return
	}

	plan.ID = types.StringValue(stablePyPIID(*plan))
	plan.ResolvedVersion = types.StringValue(version)
	plan.ResolvedFilename = types.StringValue(chosen.Filename)
	plan.ResolvedURL = types.StringValue(resolvedURL)
	plan.SHA256 = types.StringValue(hex.EncodeToString(hasher.Sum(nil)))
	plan.SizeBytes = types.Int64Value(n)

	if et := httpResp.Header.Get("ETag"); et != "" {
		plan.ETag = types.StringValue(et)
	} else {
		plan.ETag = types.StringNull()
	}
	if lm := httpResp.Header.Get("Last-Modified"); lm != "" {
		plan.LastModified = types.StringValue(lm)
	} else {
		plan.LastModified = types.StringNull()
	}
}

func applyPyPIDefaults(m *PyPIDownloadModel) {
	if m.Kind.IsNull() || m.Kind.IsUnknown() || strings.TrimSpace(m.Kind.ValueString()) == "" {
		m.Kind = types.StringValue("wheel")
	}
	if m.RepoURL.IsNull() || m.RepoURL.IsUnknown() || strings.TrimSpace(m.RepoURL.ValueString()) == "" {
		m.RepoURL = types.StringValue("https://pypi.org")
	}
	if m.FollowRedirects.IsNull() || m.FollowRedirects.IsUnknown() {
		m.FollowRedirects = types.BoolValue(true)
	}
	if m.TimeoutSeconds.IsNull() || m.TimeoutSeconds.IsUnknown() || m.TimeoutSeconds.ValueInt64() <= 0 {
		m.TimeoutSeconds = types.Int64Value(120)
	}
}

func fetchPyPIJSON(ctx context.Context, client *http.Client, u string, plan *PyPIDownloadModel, out any, diags *diag.Diagnostics) error {
	if _, err := url.Parse(u); err != nil {
		diags.AddError("Invalid metadata URL", err.Error())
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		diags.AddError("Failed to create HTTP request", err.Error())
		return err
	}
	applyPyPIAuth(req, plan)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		diags.AddError("Metadata request failed", err.Error())
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		diags.AddError("Metadata request failed", fmt.Sprintf("HTTP %d from %s", resp.StatusCode, u))
		return fmt.Errorf("metadata http %d", resp.StatusCode)
	}

	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(out); err != nil {
		diags.AddError("Failed parsing JSON metadata", err.Error())
		return err
	}
	return nil
}

func choosePyPIArtifact(urls []pypiJSONURL, kind string, filename types.String) (pypiJSONURL, error) {
	if kind == "custom" {
		want := strings.TrimSpace(filename.ValueString())
		if want == "" {
			return pypiJSONURL{}, fmt.Errorf(`filename is required when kind="custom"`)
		}
		for _, u := range urls {
			if u.Filename == want {
				return u, nil
			}
		}
		return pypiJSONURL{}, fmt.Errorf("no artifact with filename %q", want)
	}

	switch kind {
	case "wheel":
		for _, u := range urls {
			if strings.EqualFold(u.Packagetype, "bdist_wheel") {
				return u, nil
			}
		}
		// fallback
		for _, u := range urls {
			if strings.EqualFold(u.Packagetype, "sdist") {
				return u, nil
			}
		}
		if len(urls) > 0 {
			return urls[0], nil
		}
		return pypiJSONURL{}, fmt.Errorf("no artifacts found for this release")
	case "sdist":
		for _, u := range urls {
			if strings.EqualFold(u.Packagetype, "sdist") {
				return u, nil
			}
		}
		if len(urls) > 0 {
			return urls[0], nil
		}
		return pypiJSONURL{}, fmt.Errorf("no artifacts found for this release")
	case "any":
		if len(urls) == 0 {
			return pypiJSONURL{}, fmt.Errorf("no artifacts found for this release")
		}
		return urls[0], nil
	default:
		return pypiJSONURL{}, fmt.Errorf(`kind must be one of: wheel, sdist, any, custom`)
	}
}

func applyPyPIAuth(req *http.Request, plan *PyPIDownloadModel) {
	if plan == nil {
		return
	}
	if !plan.BearerToken.IsNull() && strings.TrimSpace(plan.BearerToken.ValueString()) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(plan.BearerToken.ValueString()))
		return
	}
	if !plan.Username.IsNull() && strings.TrimSpace(plan.Username.ValueString()) != "" &&
		!plan.Password.IsNull() && strings.TrimSpace(plan.Password.ValueString()) != "" {
		req.SetBasicAuth(strings.TrimSpace(plan.Username.ValueString()), strings.TrimSpace(plan.Password.ValueString()))
	}
}

func httpClientGeneric(timeoutSeconds types.Int64, followRedirects types.Bool) *http.Client {
	timeout := time.Duration(timeoutSeconds.ValueInt64()) * time.Second
	c := &http.Client{Timeout: timeout}
	if !followRedirects.ValueBool() {
		c.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return c
}

func stablePyPIID(m PyPIDownloadModel) string {
	parts := []string{
		m.RepoURL.ValueString(),
		m.PackageName.ValueString(),
		m.Version.ValueString(),
		m.Kind.ValueString(),
		m.Filename.ValueString(),
		m.OutputPath.ValueString(),
	}
	return strings.Join(parts, "|")
}
