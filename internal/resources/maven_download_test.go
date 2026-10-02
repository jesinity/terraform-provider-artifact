package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBuildMavenURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		model MavenDownloadModel
		want  string
	}{
		{
			name: "default jar coordinates",
			model: MavenDownloadModel{
				GroupID: types.StringValue("org.example.tools"), ArtifactID: types.StringValue("runner"),
				Version: types.StringValue("1.2.3"), RepoURL: types.StringValue("https://repo.example/maven2"),
			},
			want: "https://repo.example/maven2/org/example/tools/runner/1.2.3/runner-1.2.3.jar",
		},
		{
			name: "sources classifier",
			model: MavenDownloadModel{
				GroupID: types.StringValue("org.example"), ArtifactID: types.StringValue("runner"),
				Version: types.StringValue("1.2.3"), RepoURL: types.StringValue("https://repo.example/"),
				Kind: types.StringValue("sources"),
			},
			want: "https://repo.example/org/example/runner/1.2.3/runner-1.2.3-sources.jar",
		},
		{
			name: "custom extension and classifier",
			model: MavenDownloadModel{
				GroupID: types.StringValue("org.example"), ArtifactID: types.StringValue("runner"),
				Version: types.StringValue("1.2.3"), RepoURL: types.StringValue("https://repo.example"),
				Kind: types.StringValue("custom"), Extension: types.StringValue("zip"),
				Classifier: types.StringValue("linux-x64"),
			},
			want: "https://repo.example/org/example/runner/1.2.3/runner-1.2.3-linux-x64.zip",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			applyMavenDefaults(&tc.model)
			got, err := buildMavenURL(tc.model)
			if err != nil {
				t.Fatalf("buildMavenURL returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("URL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildMavenURLRejectsInvalidCoordinatesAndKind(t *testing.T) {
	t.Parallel()

	base := MavenDownloadModel{
		GroupID: types.StringValue("org.example"), ArtifactID: types.StringValue("runner"),
		Version: types.StringValue("1.2.3"), RepoURL: types.StringValue("https://repo.example"),
	}
	tests := []struct {
		name  string
		model MavenDownloadModel
	}{
		{name: "missing coordinate", model: MavenDownloadModel{ArtifactID: base.ArtifactID, Version: base.Version, RepoURL: base.RepoURL}},
		{name: "unknown kind", model: MavenDownloadModel{GroupID: base.GroupID, ArtifactID: base.ArtifactID, Version: base.Version, RepoURL: base.RepoURL, Kind: types.StringValue("tar")}},
		{name: "custom extension required", model: MavenDownloadModel{GroupID: base.GroupID, ArtifactID: base.ArtifactID, Version: base.Version, RepoURL: base.RepoURL, Kind: types.StringValue("custom")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildMavenURL(tc.model); err == nil {
				t.Fatal("buildMavenURL succeeded for invalid input")
			}
		})
	}
}

func TestMavenDownloadWritesBytesAndMetadata(t *testing.T) {
	t.Parallel()

	const body = "maven artifact"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("ETag", "maven-etag")
		w.Header().Set("Last-Modified", "maven-modified")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	out := filepath.Join(t.TempDir(), "nested", "artifact.jar")
	model := MavenDownloadModel{
		BearerToken: types.StringValue("token"), FollowRedirects: types.BoolValue(true),
		TimeoutSeconds: types.Int64Value(5),
	}
	hash, size, etag, modified, err := doHTTPDownload(context.Background(), server.URL, out, model)
	if err != nil {
		t.Fatalf("doHTTPDownload returned error: %v", err)
	}
	sum := sha256.Sum256([]byte(body))
	if hash != hex.EncodeToString(sum[:]) || size != int64(len(body)) {
		t.Fatalf("download result = (%q, %d)", hash, size)
	}
	if etag != "maven-etag" || modified != "maven-modified" {
		t.Fatalf("headers = (%q, %q)", etag, modified)
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != body {
		t.Fatalf("output = %q, err=%v", got, err)
	}
}
