package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestResolvePyPIArtifactURLSelectsArtifact(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pypi/demo/1.0/json" {
			t.Errorf("metadata path = %q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"urls":[{"url":"https://files.example/demo.whl","filename":"demo-1.0-py3-none-any.whl","packagetype":"bdist_wheel"},{"url":"https://files.example/demo.tar.gz","filename":"demo-1.0.tar.gz","packagetype":"sdist"}]}`)
	}))
	defer server.Close()

	model := PyPIDownloadModel{
		TrustedAuthOrigins: types.SetNull(types.StringType),
		IndexURL:           types.StringValue(server.URL), Package: types.StringValue("demo"),
		Version: types.StringValue("1.0"), ArtifactType: types.StringValue("wheel"),
		FollowRedirects: types.BoolValue(true), TimeoutSeconds: types.Int64Value(5),
	}
	got, err := resolvePyPIArtifactURL(context.Background(), model)
	if err != nil {
		t.Fatalf("resolvePyPIArtifactURL returned error: %v", err)
	}
	if got != "https://files.example/demo.whl" {
		t.Fatalf("resolved URL = %q", got)
	}

	model.ArtifactType = types.StringValue("sdist")
	got, err = resolvePyPIArtifactURL(context.Background(), model)
	if err != nil || got != "https://files.example/demo.tar.gz" {
		t.Fatalf("sdist resolution = %q, err=%v", got, err)
	}
}

func TestResolvePyPIArtifactURLRequiresExactFilenameMatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"urls":[{"url":"https://files.example/demo.whl","filename":"demo.whl","packagetype":"bdist_wheel"}]}`)
	}))
	defer server.Close()

	model := PyPIDownloadModel{
		TrustedAuthOrigins: types.SetNull(types.StringType),
		IndexURL:           types.StringValue(server.URL), Package: types.StringValue("demo"), Version: types.StringValue("1.0"),
		ArtifactType: types.StringValue("wheel"), Filename: types.StringValue("not-demo.whl"),
		FollowRedirects: types.BoolValue(true), TimeoutSeconds: types.Int64Value(5),
	}
	if _, err := resolvePyPIArtifactURL(context.Background(), model); err == nil {
		t.Fatal("resolver accepted a filename not present in the metadata")
	}
}

func TestPyPIDownloadWritesBytesAndMetadata(t *testing.T) {
	t.Parallel()

	const body = "python package"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("expected basic authorization header")
		}
		w.Header().Set("ETag", "pypi-etag")
		w.Header().Set("Last-Modified", "pypi-modified")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	out := filepath.Join(t.TempDir(), "nested", "package.whl")
	model := PyPIDownloadModel{
		TrustedAuthOrigins: types.SetNull(types.StringType),
		IndexURL:           types.StringValue(server.URL),
		Username:           types.StringValue("user"), Password: types.StringValue("pass"),
		FollowRedirects: types.BoolValue(true), TimeoutSeconds: types.Int64Value(5),
	}
	hash, size, etag, modified, err := doPyPIDownload(context.Background(), server.URL, out, model)
	if err != nil {
		t.Fatalf("doPyPIDownload returned error: %v", err)
	}
	sum := sha256.Sum256([]byte(body))
	if hash != hex.EncodeToString(sum[:]) || size != int64(len(body)) {
		t.Fatalf("download result = (%q, %d)", hash, size)
	}
	if etag != "pypi-etag" || modified != "pypi-modified" {
		t.Fatalf("headers = (%q, %q)", etag, modified)
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != body {
		t.Fatalf("output = %q, err=%v", got, err)
	}
}
