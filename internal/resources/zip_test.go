package resources

import (
	"archive/zip"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestBuildZipFiltersAndPreservesRelativePaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	input := filepath.Join(root, "input")
	if err := os.MkdirAll(filepath.Join(input, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(input, "keep.txt"), "keep")
	writeFile(t, filepath.Join(input, "nested", "keep.json"), "nested")
	writeFile(t, filepath.Join(input, "nested", "skip.tmp"), "skip")

	output := filepath.Join(root, "out", "bundle.zip")
	_, count, _, err := buildZip(input, output, []string{"**/*"}, []string{"**/*.tmp"}, true)
	if err != nil {
		t.Fatalf("buildZip returned error: %v", err)
	}
	if count != 2 {
		t.Fatalf("file count = %d, want 2", count)
	}

	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatalf("open output archive: %v", err)
	}
	defer archive.Close()

	var names []string
	for _, file := range archive.File {
		names = append(names, file.Name)
	}
	want := []string{"keep.txt", "nested/keep.json"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("archive entries = %v, want %v", names, want)
	}

	for _, file := range archive.File {
		if file.Modified.After(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("entry %q has non-normalized timestamp %s", file.Name, file.Modified)
		}
	}
}

func TestBuildZipDeterministicOutput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	input := filepath.Join(root, "input")
	if err := os.Mkdir(input, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(input, "artifact.bin")
	writeFile(t, file, "same bytes")
	before := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(file, before, before); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(root, "first.zip")
	second := filepath.Join(root, "second.zip")

	firstHash, _, _, err := buildZip(input, first, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	// Changing filesystem metadata must not change a deterministic archive.
	after := before.Add(24 * time.Hour)
	if err := os.Chtimes(file, after, after); err != nil {
		t.Fatal(err)
	}
	secondHash, _, _, err := buildZip(input, second, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("deterministic hashes differ: %s != %s", firstHash, secondHash)
	}
}

func TestBuildZipRejectsNonDirectoryInput(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "not-a-directory")
	writeFile(t, file, "content")
	if _, _, _, err := buildZip(file, filepath.Join(t.TempDir(), "out.zip"), nil, nil, true); err == nil {
		t.Fatal("buildZip succeeded with a non-directory input")
	}
}

func TestGlobMatchSupportsRecursiveAndSegmentWildcards(t *testing.T) {
	t.Parallel()

	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"**/*", "a.txt", true},
		{"**/*", "nested/a.txt", true},
		{"*.txt", "nested/a.txt", false},
		{"nested/?.json", "nested/a.json", true},
		{"nested/?.json", "nested/ab.json", false},
		{"nested/**/a.txt", "nested/deep/a.txt", true},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"/"+tc.path, func(t *testing.T) {
			if got := globMatch(tc.pattern, tc.path); got != tc.want {
				t.Errorf("globMatch(%q, %q) = %t, want %t", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildZipExcludesOutputAndStaleTemporaryFile(t *testing.T) {
	for _, alias := range []bool{false, true} {
		name := "direct"
		if alias {
			name = "symlinked input"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			input := filepath.Join(root, "input")
			if err := os.Mkdir(input, 0o755); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(input, "bundle.zip")
			writeFile(t, filepath.Join(input, "source.txt"), "source")
			if alias {
				link := filepath.Join(root, "alias")
				if err := os.Symlink(input, link); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("creating symlinks requires Windows developer mode or additional privileges: %v", err)
					}
					t.Fatal(err)
				}
				input = link
			}
			first, count, size, err := buildZip(input, out, nil, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, out+".tmp", "incomplete previous build")
			second, secondCount, secondSize, err := buildZip(input, out, nil, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			if count != 1 || secondCount != 1 || size != secondSize || first != second {
				t.Fatalf("unchanged sources produced different archives: counts %d/%d, sizes %d/%d, hashes %s/%s", count, secondCount, size, secondSize, first, second)
			}
			archive, err := zip.OpenReader(out)
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			if len(archive.File) != 1 || archive.File[0].Name != "source.txt" {
				t.Fatal("archive contains its output or temporary file")
			}
		})
	}
}
