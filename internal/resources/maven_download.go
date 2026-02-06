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
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type MavenDownloadResource struct{}

func NewMavenDownloadResource() resource.Resource {
	return &MavenDownloadResource{}
}

type MavenDownloadModel struct {
	ID         types.String `tfsdk:"id"`
	GroupID    types.String `tfsdk:"group_id"`
	ArtifactID types.String `tfsdk:"artifact_id"`
	Version    types.String `tfsdk:"version"`

	Classifier types.String `tfsdk:"classifier"`
	RepoURL    types.String `tfsdk:"repo_url"`

	// kind: jar|pom|sources|javadoc|custom
	Kind      types.String `tfsdk:"kind"`
	Extension types.String `tfsdk:"extension"` // required for custom

	OutputPath types.String `tfsdk:"output_path"`

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

	ResolvedURL  types.String `tfsdk:"resolved_url"`
	SHA256       types.String `tfsdk:"sha256"`
	SizeBytes    types.Int64  `tfsdk:"size_bytes"`
	ETag         types.String `tfsdk:"etag"`
	LastModified types.String `tfsdk:"last_modified"`
}

func (r *MavenDownloadResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_maven_download"
}

func (r *MavenDownloadResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true},

			"group_id":    schema.StringAttribute{Required: true},
			"artifact_id": schema.StringAttribute{Required: true},
			"version":     schema.StringAttribute{Required: true},

			"classifier": schema.StringAttribute{Optional: true},

			"repo_url": schema.StringAttribute{
				Optional: true,
				Computed: true, // default to Maven Central
			},

			"kind": schema.StringAttribute{
				Optional: true,
				Computed: true, // default "jar"
			},
			"extension": schema.StringAttribute{
				Optional: true,
			},

			"output_path": schema.StringAttribute{Required: true},

			"username":     schema.StringAttribute{Optional: true},
			"password":     schema.StringAttribute{Optional: true, Sensitive: true},
			"bearer_token": schema.StringAttribute{Optional: true, Sensitive: true},

			"follow_redirects": schema.BoolAttribute{Optional: true, Computed: true},
			"timeout_seconds":  schema.Int64Attribute{Optional: true, Computed: true},

			"refresh_strategy": schema.StringAttribute{Optional: true, Computed: true},

			"resolved_url":  schema.StringAttribute{Computed: true},
			"sha256":        schema.StringAttribute{Computed: true},
			"size_bytes":    schema.Int64Attribute{Computed: true},
			"etag":          schema.StringAttribute{Computed: true},
			"last_modified": schema.StringAttribute{Computed: true},
		},
	}
}

func (r *MavenDownloadResource) Configure(context.Context, resource.ConfigureRequest, *resource.ConfigureResponse) {}

func (r *MavenDownloadResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MavenDownloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	applyMavenDefaults(&plan)

	resolved, err := buildMavenURL(plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Maven coordinates/config", err.Error())
		return
	}

	sha, n, et, lm, err := doHTTPDownload(ctx, resolved, plan.OutputPath.ValueString(), plan)
	if err != nil {
		resp.Diagnostics.AddError("Download failed", err.Error())
		return
	}

	plan.ID = types.StringValue(stableMavenID(plan))
	plan.ResolvedURL = types.StringValue(resolved)
	plan.SHA256 = types.StringValue(sha)
	plan.SizeBytes = types.Int64Value(n)
	plan.ETag = stringOrNull(et)
	plan.LastModified = stringOrNull(lm)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MavenDownloadResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MavenDownloadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	applyMavenDefaults(&state)

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
		if err != nil || (!state.SHA256.IsNull() && state.SHA256.ValueString() != "" && sha != state.SHA256.ValueString()) {
			resp.State.RemoveResource(ctx)
			return
		}
	default:
		resp.Diagnostics.AddError("Invalid refresh_strategy", `refresh_strategy must be one of: "none", "missing", "sha256"`)
		return
	}

	// Keep resolved URL stable in state.
	resolved, err := buildMavenURL(state)
	if err == nil {
		state.ResolvedURL = types.StringValue(resolved)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MavenDownloadResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MavenDownloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	applyMavenDefaults(&plan)

	resolved, err := buildMavenURL(plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Maven coordinates/config", err.Error())
		return
	}

	sha, n, et, lm, err := doHTTPDownload(ctx, resolved, plan.OutputPath.ValueString(), plan)
	if err != nil {
		resp.Diagnostics.AddError("Download failed", err.Error())
		return
	}

	plan.ID = types.StringValue(stableMavenID(plan))
	plan.ResolvedURL = types.StringValue(resolved)
	plan.SHA256 = types.StringValue(sha)
	plan.SizeBytes = types.Int64Value(n)
	plan.ETag = stringOrNull(et)
	plan.LastModified = stringOrNull(lm)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MavenDownloadResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MavenDownloadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_ = os.Remove(state.OutputPath.ValueString())
	resp.State.RemoveResource(ctx)
}

func (r *MavenDownloadResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func applyMavenDefaults(m *MavenDownloadModel) {
	if m.Kind.IsNull() || m.Kind.IsUnknown() || strings.TrimSpace(m.Kind.ValueString()) == "" {
		m.Kind = types.StringValue("jar")
	}
	if m.RepoURL.IsNull() || m.RepoURL.IsUnknown() || strings.TrimSpace(m.RepoURL.ValueString()) == "" {
		m.RepoURL = types.StringValue("https://repo1.maven.org/maven2/")
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

func buildMavenURL(m MavenDownloadModel) (string, error) {
	repo := strings.TrimSpace(m.RepoURL.ValueString())
	if !strings.HasSuffix(repo, "/") {
		repo += "/"
	}
	if _, err := url.Parse(repo); err != nil {
		return "", fmt.Errorf("invalid repo_url: %w", err)
	}

	group := strings.ReplaceAll(strings.TrimSpace(m.GroupID.ValueString()), ".", "/")
	artifact := strings.TrimSpace(m.ArtifactID.ValueString())
	version := strings.TrimSpace(m.Version.ValueString())
	if group == "" || artifact == "" || version == "" {
		return "", fmt.Errorf("group_id, artifact_id, version must be non-empty")
	}

	kind := strings.ToLower(strings.TrimSpace(m.Kind.ValueString()))
	classifier := strings.TrimSpace(m.Classifier.ValueString())

	ext := ""
	switch kind {
	case "jar":
		ext = "jar"
	case "pom":
		ext = "pom"
	case "sources":
		ext = "jar"
		if classifier == "" {
			classifier = "sources"
		}
	case "javadoc":
		ext = "jar"
		if classifier == "" {
			classifier = "javadoc"
		}
	case "custom":
		ext = strings.TrimSpace(m.Extension.ValueString())
		if ext == "" {
			return "", fmt.Errorf(`extension is required when kind="custom"`)
		}
	default:
		return "", fmt.Errorf(`kind must be one of: jar, pom, sources, javadoc, custom`)
	}

	filename := fmt.Sprintf("%s-%s", artifact, version)
	if classifier != "" {
		filename += "-" + classifier
	}
	filename += "." + ext

	return repo + group + "/" + artifact + "/" + version + "/" + filename, nil
}

func applyAuth(req *http.Request, m MavenDownloadModel) {
	if !m.BearerToken.IsNull() && m.BearerToken.ValueString() != "" {
		req.Header.Set("Authorization", "Bearer "+m.BearerToken.ValueString())
		return
	}
	if !m.Username.IsNull() && m.Username.ValueString() != "" &&
		!m.Password.IsNull() && m.Password.ValueString() != "" {
		req.SetBasicAuth(m.Username.ValueString(), m.Password.ValueString())
	}
}

func httpClient(m MavenDownloadModel) *http.Client {
	timeout := time.Duration(m.TimeoutSeconds.ValueInt64()) * time.Second
	c := &http.Client{Timeout: timeout}
	if !m.FollowRedirects.ValueBool() {
		c.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return c
}

func doHTTPDownload(ctx context.Context, resolvedURL, out string, m MavenDownloadModel) (sha string, size int64, etag, lastModified string, err error) {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", 0, "", "", err
	}
	client := httpClient(m)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, resolvedURL, nil)
	if err != nil {
		return "", 0, "", "", err
	}
	applyAuth(httpReq, m)

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return "", 0, "", "", err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return "", 0, "", "", fmt.Errorf("HTTP %d from %s", httpResp.StatusCode, resolvedURL)
	}

	if et := httpResp.Header.Get("ETag"); et != "" {
		etag = et
	}
	if lm := httpResp.Header.Get("Last-Modified"); lm != "" {
		lastModified = lm
	}

	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", 0, "", "", err
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hasher), httpResp.Body)
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

func stableMavenID(m MavenDownloadModel) string {
	parts := []string{
		m.RepoURL.ValueString(),
		m.GroupID.ValueString(),
		m.ArtifactID.ValueString(),
		m.Version.ValueString(),
		m.Classifier.ValueString(),
		m.Kind.ValueString(),
		m.Extension.ValueString(),
		m.OutputPath.ValueString(),
		m.RefreshStrategy.ValueString(),
	}
	return strings.Join(parts, "|")
}
