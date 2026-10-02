package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// All three resources have separate HTTP implementations, so exercise the
// same error and redirect contract against each of them.
func TestHTTPDownloadFailureAndRedirects(t *testing.T) {
	t.Parallel()
	engines := []struct {
		name string
		run  func(context.Context, string, string, bool) error
	}{
		{"http", func(ctx context.Context, url, out string, follow bool) error {
			_, _, _, _, _, err := downloadToPath(ctx, url, out, DownloadModel{
				FollowRedirects: types.BoolValue(follow), TimeoutSeconds: types.Int64Value(5),
			})
			return err
		}},
		{"maven", func(ctx context.Context, url, out string, follow bool) error {
			_, _, _, _, err := doHTTPDownload(ctx, url, out, MavenDownloadModel{
				FollowRedirects: types.BoolValue(follow), TimeoutSeconds: types.Int64Value(5),
			})
			return err
		}},
		{"pypi", func(ctx context.Context, url, out string, follow bool) error {
			_, _, _, _, err := doPyPIDownload(ctx, url, out, PyPIDownloadModel{
				TrustedAuthOrigins: types.SetNull(types.StringType),
				FollowRedirects:    types.BoolValue(follow), TimeoutSeconds: types.Int64Value(5),
			})
			return err
		}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/artifact", http.StatusFound)
		case "/truncated":
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("short"))
		case "/error":
			http.Error(w, "missing", http.StatusNotFound)
		default:
			_, _ = w.Write([]byte("downloaded"))
		}
	}))
	t.Cleanup(server.Close)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name      string
				endpoint  string
				follow    bool
				cancel    bool
				wantError bool
			}{
				{"follow redirect", "/redirect", true, false, false},
				{"reject redirect", "/redirect", false, false, true},
				{"truncated response", "/truncated", true, false, true},
				{"HTTP error", "/error", true, false, true},
				{"canceled context", "/artifact", true, true, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					out := filepath.Join(t.TempDir(), "artifact")
					writeFile(t, out, "previous")
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					if tc.cancel {
						cancel()
					}
					err := engine.run(ctx, server.URL+tc.endpoint, out, tc.follow)
					if (err != nil) != tc.wantError {
						t.Fatalf("error = %v, wantError = %t", err, tc.wantError)
					}
					content, err := os.ReadFile(out)
					if err != nil {
						t.Fatal(err)
					}
					expected := "downloaded"
					if tc.wantError {
						expected = "previous"
					}
					if string(content) != expected {
						t.Errorf("output = %q, want %q", content, expected)
					}
					if _, err := os.Stat(out + ".tmp"); !os.IsNotExist(err) {
						t.Errorf("temporary download file was not cleaned up: %v", err)
					}
				})
			}
		})
	}
}
