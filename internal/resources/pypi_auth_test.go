package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestPyPIArtifactCredentialsAreScoped(t *testing.T) {
	for _, tc := range []struct {
		name    string
		same    bool
		trusted bool
	}{
		{"index origin", true, false},
		{"separate origin", false, false},
		{"explicitly trusted origin", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			artifact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertBasicCredentials(t, r, tc.trusted)
				_, _ = w.Write([]byte("package"))
			}))
			defer artifact.Close()
			index := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertBasicCredentials(t, r, true)
				if r.URL.Path == "/artifact" {
					_, _ = w.Write([]byte("package"))
					return
				}
				target := artifact.URL + "/artifact"
				if tc.same {
					target = "http://" + r.Host + "/artifact"
				}
				writePyPIMetadata(t, w, target)
			}))
			defer index.Close()
			model := pypiAuthTestModel(index.URL)
			if tc.trusted {
				model.TrustedAuthOrigins = types.SetValueMust(types.StringType, []attr.Value{types.StringValue(artifact.URL)})
			}
			resolved, err := resolvePyPIArtifactURL(context.Background(), model)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, _, err := doPyPIDownload(context.Background(), resolved, filepath.Join(t.TempDir(), "artifact"), model); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPyPIRedirectCredentialsAreScoped(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		for _, trusted := range []bool{false, true} {
			name := "artifact"
			if metadata {
				name = "metadata"
			}
			if trusted {
				name += "/trusted"
			} else {
				name += "/untrusted"
			}
			t.Run(name, func(t *testing.T) {
				destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assertBasicCredentials(t, r, trusted)
					if metadata {
						writePyPIMetadata(t, w, "http://"+r.Host+"/artifact")
					} else {
						_, _ = w.Write([]byte("package"))
					}
				}))
				defer destination.Close()
				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assertBasicCredentials(t, r, true)
					http.Redirect(w, r, destination.URL+"/target", http.StatusFound)
				}))
				defer source.Close()
				model := pypiAuthTestModel(source.URL)
				if trusted {
					model.TrustedAuthOrigins = types.SetValueMust(types.StringType, []attr.Value{types.StringValue(destination.URL)})
				}
				if metadata {
					if _, err := resolvePyPIArtifactURL(context.Background(), model); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, _, _, _, err := doPyPIDownload(context.Background(), source.URL+"/artifact", filepath.Join(t.TempDir(), "artifact"), model); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestPyPIOriginBoundaries(t *testing.T) {
	model := pypiAuthTestModel("https://INDEX.example:443/private")
	for _, tc := range []struct {
		url  string
		auth bool
	}{
		{"https://index.example/artifact", true},
		{"https://index.example:443/artifact", true},
		{"http://index.example/artifact", false},
		{"https://index.example:444/artifact", false},
		{"https://cdn.index.example/artifact", false},
		{"https://index.example.evil.example/artifact", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.SetBasicAuth("user", "secret")
			client := pypiHTTPClient(model)
			if err := client.CheckRedirect(req, []*http.Request{{}}); err != nil {
				t.Fatal(err)
			}
			assertBasicCredentials(t, req, tc.auth)
		})
	}
}

func pypiAuthTestModel(index string) PyPIDownloadModel {
	return PyPIDownloadModel{
		IndexURL: types.StringValue(index), Package: types.StringValue("demo"),
		Version: types.StringValue("1.0"), ArtifactType: types.StringValue("wheel"),
		Username: types.StringValue("user"), Password: types.StringValue("secret"),
		FollowRedirects: types.BoolValue(true), TimeoutSeconds: types.Int64Value(5),
		TrustedAuthOrigins: types.SetNull(types.StringType),
	}
}

func assertBasicCredentials(t *testing.T, req *http.Request, want bool) {
	t.Helper()
	username, password, ok := req.BasicAuth()
	if want {
		if !ok || username != "user" || password != "secret" {
			t.Error("expected configured Basic Auth credentials")
		}
	} else if req.Header.Get("Authorization") != "" {
		t.Error("credentials were sent to an untrusted origin")
	}
}

func writePyPIMetadata(t *testing.T, w http.ResponseWriter, target string) {
	t.Helper()
	err := json.NewEncoder(w).Encode(map[string]any{
		"urls": []map[string]string{{
			"url": target, "filename": "demo.whl", "packagetype": "bdist_wheel",
		}},
	})
	if err != nil {
		t.Errorf("write metadata: %v", err)
	}
}
