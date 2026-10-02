package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestNormalizeURL(t *testing.T) {
	t.Parallel()

	got, err := normalizeURL("  https://example.com/a/../artifact.zip?download=1  ")
	if err != nil {
		t.Fatalf("normalizeURL returned error: %v", err)
	}
	if got != "https://example.com/artifact.zip?download=1" {
		t.Fatalf("normalized URL = %q", got)
	}
	if _, err := normalizeURL(" "); err == nil {
		t.Fatal("normalizeURL accepted an empty URL")
	}
	if _, err := normalizeURL("example.com/artifact.zip"); err == nil {
		t.Fatal("normalizeURL accepted a URL without a scheme")
	}
}

func TestDownloadToPathWritesBytesAndResponseMetadata(t *testing.T) {
	t.Parallel()

	const body = "artifact payload"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q, want bearer token", r.Header.Get("Authorization"))
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	out := filepath.Join(t.TempDir(), "nested", "artifact.bin")
	model := DownloadModel{
		BearerToken:     types.StringValue("secret"),
		Username:        types.StringValue("user"),
		Password:        types.StringValue("pass"),
		FollowRedirects: types.BoolValue(true),
		TimeoutSeconds:  types.Int64Value(5),
	}
	gotHash, gotSize, etag, modified, _, err := downloadToPath(context.Background(), server.URL, out, model)
	if err != nil {
		t.Fatalf("downloadToPath returned error: %v", err)
	}
	wantSum := sha256.Sum256([]byte(body))
	if gotHash != hex.EncodeToString(wantSum[:]) || gotSize != int64(len(body)) {
		t.Fatalf("download metadata = (%q, %d), want (%q, %d)", gotHash, gotSize, hex.EncodeToString(wantSum[:]), len(body))
	}
	if etag != `"v1"` || modified != "Wed, 21 Oct 2015 07:28:00 GMT" {
		t.Fatalf("response metadata = (%q, %q)", etag, modified)
	}
	gotBody, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBody) != body {
		t.Fatalf("output body = %q, want %q", gotBody, body)
	}
}

func TestDownloadToPathRejectsHTTPErrorWithoutOverwritingOutput(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	out := filepath.Join(t.TempDir(), "artifact.bin")
	writeFile(t, out, "old content")
	_, _, _, _, _, err := downloadToPath(context.Background(), server.URL, out, DownloadModel{
		FollowRedirects: types.BoolValue(true),
		TimeoutSeconds:  types.Int64Value(5),
	})
	if err == nil {
		t.Fatal("downloadToPath succeeded for HTTP 502")
	}
	got, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old content" {
		t.Fatalf("existing output was changed to %q", got)
	}
}

func TestApplyDownloadDefaults(t *testing.T) {
	t.Parallel()

	model := DownloadModel{}
	applyDownloadDefaults(&model)
	if !model.FollowRedirects.ValueBool() || model.TimeoutSeconds.ValueInt64() != 120 || model.RefreshStrategy.ValueString() != "none" {
		t.Fatalf("unexpected defaults: redirects=%t timeout=%d refresh=%q", model.FollowRedirects.ValueBool(), model.TimeoutSeconds.ValueInt64(), model.RefreshStrategy.ValueString())
	}
}

func TestStableDownloadIDDoesNotIncludeSecrets(t *testing.T) {
	t.Parallel()

	model := DownloadModel{
		URL:             types.StringValue("https://example.com/a"),
		OutputPath:      types.StringValue("artifact.bin"),
		Username:        types.StringValue("user"),
		Password:        types.StringValue("secret"),
		BearerToken:     types.StringValue("token"),
		RefreshStrategy: types.StringValue("none"),
	}
	id := stableDownloadID(model)
	if id == "" || containsAny(id, "secret", "token") {
		t.Fatalf("stable ID is empty or contains a secret: %q", id)
	}
}

func containsAny(value string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(value, part) {
			return true
		}
	}
	return false
}
