package provider

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const acceptanceProviderConfig = `
terraform {
  required_providers {
    artifact = {
      source = "jesinity/artifact"
    }
  }
}
`

func TestAccDownloadLifecycle(t *testing.T) {
	requireAcceptance(t)
	for _, kind := range []string{"download", "maven_download", "pypi_download"} {
		t.Run(kind, func(t *testing.T) {
			var downloads atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				version := "1.0"
				if strings.Contains(r.URL.Path, "2.0") {
					version = "2.0"
				}
				if strings.HasSuffix(r.URL.Path, "/json") {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"urls": []map[string]string{{
							"url":      "http://" + r.Host + "/artifact/" + version,
							"filename": "demo.whl", "packagetype": "bdist_wheel",
						}},
					})
					return
				}
				downloads.Add(1)
				_, _ = w.Write([]byte("artifact version " + version))
			}))
			defer server.Close()
			out := filepath.Join(t.TempDir(), "artifact")
			address := "artifact_" + kind + ".test"
			hashField := "sha256"
			if kind == "download" {
				hashField = "download_sha256"
			}
			config1 := acceptanceDownloadConfig(kind, server.URL, out, "1.0", "")
			config2 := acceptanceDownloadConfig(kind, server.URL, out, "2.0", `refresh_strategy = "missing"`)
			configSHA := acceptanceDownloadConfig(kind, server.URL, out, "2.0", `refresh_strategy = "sha256"`)
			check := func(version string, requests int64) resource.TestCheckFunc {
				body := "artifact version " + version
				sum := sha256.Sum256([]byte(body))
				return resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, hashField, hex.EncodeToString(sum[:])),
					resource.TestCheckResourceAttr(address, "timeout_seconds", "120"),
					resource.TestCheckResourceAttr(address, "follow_redirects", "true"),
					func(_ *terraform.State) error {
						content, err := os.ReadFile(out)
						if err != nil {
							return err
						}
						if string(content) != body {
							return fmt.Errorf("artifact = %q, want %q", content, body)
						}
						if downloads.Load() != requests {
							return fmt.Errorf("downloaded %d times, want %d", downloads.Load(), requests)
						}
						return nil
					},
				)
			}
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: acceptanceFactories(),
				CheckDestroy:             checkFileRemoved(out),
				Steps: []resource.TestStep{
					{
						Config:           config1,
						ConfigPlanChecks: expectAction(address, plancheck.ResourceActionCreate),
						Check: resource.ComposeAggregateTestCheckFunc(
							check("1.0", 1),
							resource.TestCheckResourceAttr(address, "refresh_strategy", "none"),
						),
					},
					{Config: config1, PlanOnly: true},
					{
						Config: config2, Check: check("2.0", 2),
						ConfigPlanChecks: expectAction(address, plancheck.ResourceActionUpdate),
					},
					{
						PreConfig: func() {
							if err := os.Remove(out); err != nil {
								t.Fatal(err)
							}
						},
						Config: config2, Check: check("2.0", 3),
						ConfigPlanChecks: expectAction(address, plancheck.ResourceActionCreate),
					},
					{Config: config2, PlanOnly: true},
					{Config: configSHA, Check: check("2.0", 4)},
					{
						PreConfig: func() {
							if err := os.WriteFile(out, []byte("tampered"), 0o644); err != nil {
								t.Fatal(err)
							}
						},
						Config: configSHA, Check: check("2.0", 5),
						ConfigPlanChecks: expectAction(address, plancheck.ResourceActionCreate),
					},
				},
			})
		})
	}
}

func TestAccZipLifecycle(t *testing.T) {
	requireAcceptance(t)
	input := t.TempDir()
	for name, content := range map[string]string{"source.txt": "source", "skip.tmp": "temporary"} {
		if err := os.WriteFile(filepath.Join(input, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Intentionally place the archive inside its input tree.
	out := filepath.Join(input, "bundle.zip")
	config := func(extra string) string {
		return acceptanceProviderConfig + fmt.Sprintf(`
resource "artifact_zip" "test" {
  input_dir = %q
  output_path = %q
  %s
}
`, input, out, extra)
	}
	config1 := config("")
	config2 := config(`exclude = ["**/*.tmp"]`)
	config3 := config("exclude = [\"**/*.tmp\"]\nextra_triggers = { revision = \"2\" }")
	check := func(count int) resource.TestCheckFunc {
		return resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("artifact_zip.test", "file_count", fmt.Sprint(count)),
			resource.TestCheckResourceAttr("artifact_zip.test", "deterministic", "true"),
			func(_ *terraform.State) error {
				archive, err := zip.OpenReader(out)
				if err != nil {
					return err
				}
				defer archive.Close()
				if len(archive.File) != count {
					return fmt.Errorf("ZIP entries = %d, want %d", len(archive.File), count)
				}
				for _, entry := range archive.File {
					if entry.Name == "bundle.zip" || entry.Name == "bundle.zip.tmp" {
						return fmt.Errorf("ZIP contains its own output: %s", entry.Name)
					}
				}
				return nil
			},
		)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acceptanceFactories(),
		CheckDestroy:             checkFileRemoved(out),
		Steps: []resource.TestStep{
			{Config: config1, Check: check(2)},
			{Config: config1, PlanOnly: true},
			{Config: config2, Check: check(1), ConfigPlanChecks: expectAction("artifact_zip.test", plancheck.ResourceActionUpdate)},
			{
				PreConfig: func() {
					if err := os.Remove(out); err != nil {
						t.Fatal(err)
					}
				},
				Config: config2, Check: check(1),
				ConfigPlanChecks: expectAction("artifact_zip.test", plancheck.ResourceActionCreate),
			},
			{Config: config3, Check: check(1), ConfigPlanChecks: expectAction("artifact_zip.test", plancheck.ResourceActionReplace)},
			{Config: config3, PlanOnly: true},
		},
	})
}

func TestAccDownloadInvalidConfiguration(t *testing.T) {
	requireAcceptance(t)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "validation should have prevented this request", http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, kind := range []string{"download", "maven_download", "pypi_download"} {
		t.Run(kind, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "artifact")
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: acceptanceFactories(),
				Steps: []resource.TestStep{
					{
						Config:   acceptanceDownloadConfig(kind, server.URL, out, "1.0", "timeout_seconds = 0"),
						PlanOnly: true, ExpectError: regexp.MustCompile("Invalid timeout_seconds"),
					},
					{
						Config:   acceptanceDownloadConfig(kind, server.URL, out, "1.0", `refresh_strategy = "sh256"`),
						PlanOnly: true, ExpectError: regexp.MustCompile("must be one of:"),
					},
				},
			})
		})
	}
	if requests.Load() != 0 {
		t.Errorf("invalid configuration caused %d HTTP requests", requests.Load())
	}
}

func requireAcceptance(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") != "1" {
		t.Skip("set TF_ACC=1 to run Terraform CLI acceptance tests")
	}
	t.Setenv("TF_ACC_PROVIDER_NAMESPACE", "jesinity")
}

func acceptanceFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"artifact": providerserver.NewProtocol6WithError(New()),
	}
}

func expectAction(address string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, action)}}
}

func checkFileRemoved(out string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			return fmt.Errorf("destroy left %s on disk (stat: %v)", out, err)
		}
		return nil
	}
}

func acceptanceDownloadConfig(kind, base, out, version, extra string) string {
	arguments := ""
	switch kind {
	case "download":
		arguments = fmt.Sprintf("url = %q", base+"/artifact/"+version)
	case "maven_download":
		arguments = fmt.Sprintf("repo_url = %q\ngroup_id = \"org.example\"\nartifact_id = \"demo\"\nversion = %q", base+"/maven", version)
	case "pypi_download":
		arguments = fmt.Sprintf("index_url = %q\npackage = \"demo\"\nversion = %q", base, version)
	}
	return acceptanceProviderConfig + fmt.Sprintf(`
resource "artifact_%s" "test" {
  output_path = %q
  %s
  %s
}
`, kind, out, arguments, extra)
}
