package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// These tests exercise the resource callbacks with real framework state
// encoding. Full Terraform CLI acceptance tests are a separate layer.
type downloadFixture struct {
	name      string
	resource  func() resource.Resource
	model     func(base, out, revision string) any
	hashField string
	sizeField string
}

func downloadFixtures() []downloadFixture {
	return []downloadFixture{
		{
			name: "http", resource: NewDownloadResource,
			hashField: "download_sha256", sizeField: "download_size_bytes",
			model: func(base, out, revision string) any {
				return DownloadModel{
					URL:        types.StringValue(base + "/artifact?version=" + revision),
					OutputPath: types.StringValue(out),
				}
			},
		},
		{
			name: "maven", resource: NewMavenDownloadResource,
			hashField: "sha256", sizeField: "size_bytes",
			model: func(base, out, revision string) any {
				return MavenDownloadModel{
					RepoURL: types.StringValue(base), GroupID: types.StringValue("org.example"),
					ArtifactID: types.StringValue("demo"), Version: types.StringValue(revision),
					OutputPath: types.StringValue(out),
				}
			},
		},
		{
			name: "pypi", resource: NewPyPIDownloadResource,
			hashField: "sha256", sizeField: "size_bytes",
			model: func(base, out, revision string) any {
				return PyPIDownloadModel{
					TrustedAuthOrigins: types.SetNull(types.StringType),
					IndexURL:           types.StringValue(base), Package: types.StringValue("demo"),
					Version: types.StringValue(revision), OutputPath: types.StringValue(out),
				}
			},
		},
	}
}

func TestDownloadLifecycle(t *testing.T) {
	t.Parallel()
	for _, fixture := range downloadFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			var body atomic.Value
			body.Store("first artifact")
			server := artifactServer(t, &body)
			out := filepath.Join(t.TempDir(), "nested", "artifact")
			r := fixture.resource()

			plan := modelState(t, r, fixture.model(server.URL, out, "1.0"))
			created := resource.CreateResponse{State: plan}
			r.Create(ctx, resource.CreateRequest{Plan: planFromState(plan)}, &created)
			requireNoErrors(t, created.Diagnostics)
			assertArtifactState(t, created.State, fixture.hashField, fixture.sizeField, out, "first artifact")
			assertStringAttribute(t, created.State, "refresh_strategy", "none")
			var redirects types.Bool
			requireNoErrors(t, created.State.GetAttribute(ctx, path.Root("follow_redirects"), &redirects))
			if !redirects.Equal(types.BoolValue(true)) {
				t.Fatalf("default follow_redirects = %v, want true", redirects)
			}
			var timeout types.Int64
			requireNoErrors(t, created.State.GetAttribute(ctx, path.Root("timeout_seconds"), &timeout))
			if !timeout.Equal(types.Int64Value(120)) {
				t.Fatalf("default timeout_seconds = %v, want 120", timeout)
			}
			for _, name := range []string{"etag", "last_modified"} {
				var value types.String
				requireNoErrors(t, created.State.GetAttribute(ctx, path.Root(name), &value))
				if !value.IsNull() {
					t.Errorf("absent %s should be null, got %v", name, value)
				}
			}

			read := resource.ReadResponse{State: created.State}
			r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
			requireNoErrors(t, read.Diagnostics)
			if !read.State.Raw.Equal(created.State.Raw) {
				t.Fatal("refresh changed state for an unchanged artifact")
			}

			body.Store("second, updated artifact")
			updatedPlan := modelState(t, r, fixture.model(server.URL, out, "2.0"))
			updated := resource.UpdateResponse{State: updatedPlan}
			r.Update(ctx, resource.UpdateRequest{State: read.State, Plan: planFromState(updatedPlan)}, &updated)
			requireNoErrors(t, updated.Diagnostics)
			assertArtifactState(t, updated.State, fixture.hashField, fixture.sizeField, out, "second, updated artifact")

			deleted := resource.DeleteResponse{State: updated.State}
			r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &deleted)
			requireNoErrors(t, deleted.Diagnostics)
			if !deleted.State.Raw.IsNull() {
				t.Fatal("destroy retained resource state")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("destroy did not remove the artifact: %v", err)
			}
		})
	}
}

func TestDownloadRefreshStrategies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		strategy string
		disk     string
		removed  bool
	}{
		{"none ignores missing file", "none", "missing", false},
		{"none ignores changed file", "none", "changed", false},
		{"missing keeps present file", "missing", "present", false},
		{"missing ignores content changes", "missing", "changed", false},
		{"missing recreates missing file", "missing", "missing", true},
		{"sha256 keeps matching file", "sha256", "present", false},
		{"sha256 recreates missing file", "sha256", "missing", true},
		{"sha256 recreates changed file", "sha256", "changed", true},
		{"sha256 rejects directory", "sha256", "directory", true},
	}
	for _, fixture := range downloadFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			var body atomic.Value
			body.Store("original")
			server := artifactServer(t, &body)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					ctx := context.Background()
					out := filepath.Join(t.TempDir(), "artifact")
					writeFile(t, out, "original")
					r := fixture.resource()
					state := modelState(t, r, fixture.model(server.URL, out, "1.0"))
					sum := sha256.Sum256([]byte("original"))
					requireNoErrors(t, state.SetAttribute(ctx, path.Root(fixture.hashField), hex.EncodeToString(sum[:])))
					requireNoErrors(t, state.SetAttribute(ctx, path.Root("refresh_strategy"), tc.strategy))

					switch tc.disk {
					case "missing", "directory":
						if err := os.Remove(out); err != nil {
							t.Fatal(err)
						}
						if tc.disk == "directory" {
							if err := os.Mkdir(out, 0o755); err != nil {
								t.Fatal(err)
							}
						}
					case "changed":
						writeFile(t, out, "tampered")
					}
					resp := resource.ReadResponse{State: state}
					r.Read(ctx, resource.ReadRequest{State: state}, &resp)
					requireNoErrors(t, resp.Diagnostics)
					if resp.State.Raw.IsNull() != tc.removed {
						t.Fatalf("resource removed = %t, want %t", resp.State.Raw.IsNull(), tc.removed)
					}
				})
			}
		})
	}
}

func TestDownloadCreateReportsHTTPFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	for _, fixture := range downloadFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "artifact")
			r := fixture.resource()
			plan := modelState(t, r, fixture.model(server.URL, out, "1.0"))
			resp := resource.CreateResponse{State: plan}
			r.Create(context.Background(), resource.CreateRequest{Plan: planFromState(plan)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("create succeeded despite HTTP 503")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("failed create left an artifact: %v", err)
			}
		})
	}
}

func artifactServer(t *testing.T, body *atomic.Value) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/json") {
			// PyPI metadata points to an artifact on the same local server.
			err := json.NewEncoder(w).Encode(map[string]any{
				"urls": []map[string]string{{
					"url": "http://" + r.Host + "/artifact", "filename": "demo.whl",
					"packagetype": "bdist_wheel",
				}},
			})
			if err != nil {
				t.Errorf("write metadata: %v", err)
			}
			return
		}
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	t.Cleanup(server.Close)
	return server
}

func modelState(t *testing.T, r resource.Resource, model any) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	requireNoErrors(t, schemaResp.Diagnostics)
	state := tfsdk.State{Schema: schemaResp.Schema}
	requireNoErrors(t, state.Set(ctx, model))
	return state
}

func planFromState(state tfsdk.State) tfsdk.Plan {
	return tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}
}

func requireNoErrors(t *testing.T, diagnostics diag.Diagnostics) {
	t.Helper()
	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
}

func assertStringAttribute(t *testing.T, state tfsdk.State, name, want string) {
	t.Helper()
	var got types.String
	requireNoErrors(t, state.GetAttribute(context.Background(), path.Root(name), &got))
	if !got.Equal(types.StringValue(want)) {
		t.Errorf("%s = %v, want %q", name, got, want)
	}
}

func assertArtifactState(t *testing.T, state tfsdk.State, hashField, sizeField, out, body string) {
	t.Helper()
	ctx := context.Background()
	var id types.String
	requireNoErrors(t, state.GetAttribute(ctx, path.Root("id"), &id))
	if id.IsNull() || id.IsUnknown() || id.ValueString() == "" {
		t.Errorf("resource has no known, nonempty ID: %v", id)
	}
	sum := sha256.Sum256([]byte(body))
	assertStringAttribute(t, state, hashField, hex.EncodeToString(sum[:]))
	var size types.Int64
	requireNoErrors(t, state.GetAttribute(ctx, path.Root(sizeField), &size))
	if !size.Equal(types.Int64Value(int64(len(body)))) {
		t.Errorf("%s = %v, want %d", sizeField, size, len(body))
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != body {
		t.Errorf("artifact bytes = %q, want %q", content, body)
	}
}
