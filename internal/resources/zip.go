package resources

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type ZipResource struct{}

func NewZipResource() resource.Resource {
	return &ZipResource{}
}

type ZipModel struct {
	ID            types.String `tfsdk:"id"`
	InputDir      types.String `tfsdk:"input_dir"`
	OutputPath    types.String `tfsdk:"output_path"`
	Include       types.List   `tfsdk:"include"` // list(string)
	Exclude       types.List   `tfsdk:"exclude"` // list(string)
	Deterministic types.Bool   `tfsdk:"deterministic"`

	ZipSHA256 types.String `tfsdk:"zip_sha256"`
	FileCount types.Int64  `tfsdk:"file_count"`
	FileSize  types.Int64  `tfsdk:"file_size"`
}

func (r *ZipResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_zip"
}

func (r *ZipResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},

			"input_dir": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"output_path": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},

			"include": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
			},

			"exclude": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
			},

			"deterministic": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},

			"zip_sha256": schema.StringAttribute{
				Computed: true,
			},
			"file_count": schema.Int64Attribute{
				Computed: true,
			},
			"file_size": schema.Int64Attribute{
				Computed: true,
			},
		},
	}
}

// NOTE: Plugin framework types.ListValueMust uses attr.Value. To avoid importing the whole attr package
// in your other files, we define a tiny alias here.
type attrValue = types.String

func (r *ZipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ZipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sha, fileCount, size, err := buildZip(
		plan.InputDir.ValueString(),
		plan.OutputPath.ValueString(),
		listToStrings(plan.Include),
		listToStrings(plan.Exclude),
		plan.Deterministic.ValueBool(),
	)
	if err != nil {
		resp.Diagnostics.AddError("Zip build failed", err.Error())
		return
	}

	plan.ID = types.StringValue(fmt.Sprintf("%s:%s", plan.OutputPath.ValueString(), sha))
	plan.ZipSHA256 = types.StringValue(sha)
	plan.FileCount = types.Int64Value(int64(fileCount))
	plan.FileSize = types.Int64Value(size)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ZipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ZipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	p := state.OutputPath.ValueString()
	fi, err := os.Stat(p)
	if err != nil {
		// File disappeared; force recreate
		resp.State.RemoveResource(ctx)
		return
	}

	sha, err := fileSHA256(p)
	if err != nil {
		resp.Diagnostics.AddError("Failed to hash zip", err.Error())
		return
	}

	state.FileSize = types.Int64Value(fi.Size())
	state.ZipSHA256 = types.StringValue(sha)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ZipResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ZipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sha, fileCount, size, err := buildZip(
		plan.InputDir.ValueString(),
		plan.OutputPath.ValueString(),
		listToStrings(plan.Include),
		listToStrings(plan.Exclude),
		plan.Deterministic.ValueBool(),
	)
	if err != nil {
		resp.Diagnostics.AddError("Zip build failed", err.Error())
		return
	}

	plan.ID = types.StringValue(fmt.Sprintf("%s:%s", plan.OutputPath.ValueString(), sha))
	plan.ZipSHA256 = types.StringValue(sha)
	plan.FileCount = types.Int64Value(int64(fileCount))
	plan.FileSize = types.Int64Value(size)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ZipResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ZipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_ = os.Remove(state.OutputPath.ValueString())
	resp.State.RemoveResource(ctx)
}

// --- helpers ---

func listToStrings(l types.List) []string {
	if l.IsNull() || l.IsUnknown() {
		return nil
	}
	var out []string
	_ = l.ElementsAs(context.Background(), &out, false)
	return out
}

type zipEntry struct {
	absPath string // actual file on disk
	relPath string // path inside zip (always '/')
	mode    fs.FileMode
}

func buildZip(inputDir, outputPath string, include, exclude []string, deterministic bool) (sha string, fileCount int, size int64, err error) {
	// Validate input
	inInfo, err := os.Stat(inputDir)
	if err != nil {
		return "", 0, 0, fmt.Errorf("input_dir stat: %w", err)
	}
	if !inInfo.IsDir() {
		return "", 0, 0, fmt.Errorf("input_dir is not a directory: %s", inputDir)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", 0, 0, fmt.Errorf("mkdir: %w", err)
	}

	entries, err := collectEntries(inputDir, include, exclude)
	if err != nil {
		return "", 0, 0, err
	}

	// Deterministic ordering
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].relPath < entries[j].relPath
	})

	tmp := outputPath + ".tmp"
	_ = os.Remove(tmp)

	f, err := os.Create(tmp)
	if err != nil {
		return "", 0, 0, fmt.Errorf("create tmp: %w", err)
	}

	// Hash while writing zip
	h := sha256.New()
	mw := io.MultiWriter(f, h)

	zw := zip.NewWriter(mw)

	constZipTime := time.Unix(0, 0).UTC()

	for _, e := range entries {
		fi, err := os.Lstat(e.absPath)
		if err != nil {
			_ = zw.Close()
			_ = f.Close()
			_ = os.Remove(tmp)
			return "", 0, 0, fmt.Errorf("stat: %w", err)
		}
		if fi.IsDir() {
			continue
		}

		// Create zip header
		hdr, err := zip.FileInfoHeader(fi)
		if err != nil {
			_ = zw.Close()
			_ = f.Close()
			_ = os.Remove(tmp)
			return "", 0, 0, fmt.Errorf("zip header: %w", err)
		}

		// Force deflate for files; directories are skipped above
		hdr.Method = zip.Deflate

		// Normalize path separators
		hdr.Name = e.relPath

		// Deterministic metadata
		if deterministic {
			hdr.Modified = constZipTime
			// Normalize permissions: keep executable bit if present, otherwise 0644.
			mode := fi.Mode()
			if mode&0o111 != 0 {
				hdr.SetMode(0o755)
			} else {
				hdr.SetMode(0o644)
			}
		}

		w, err := zw.CreateHeader(hdr)
		if err != nil {
			_ = zw.Close()
			_ = f.Close()
			_ = os.Remove(tmp)
			return "", 0, 0, fmt.Errorf("zip create: %w", err)
		}

		src, err := os.Open(e.absPath)
		if err != nil {
			_ = zw.Close()
			_ = f.Close()
			_ = os.Remove(tmp)
			return "", 0, 0, fmt.Errorf("open: %w", err)
		}

		if _, err := io.Copy(w, src); err != nil {
			_ = src.Close()
			_ = zw.Close()
			_ = f.Close()
			_ = os.Remove(tmp)
			return "", 0, 0, fmt.Errorf("copy: %w", err)
		}
		_ = src.Close()

		fileCount++
	}

	if err := zw.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", 0, 0, fmt.Errorf("close zip: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", 0, 0, fmt.Errorf("close tmp: %w", err)
	}

	sum := hex.EncodeToString(h.Sum(nil))

	// Atomic replace
	if err := os.Rename(tmp, outputPath); err != nil {
		_ = os.Remove(tmp)
		return "", 0, 0, fmt.Errorf("rename: %w", err)
	}

	fi, err := os.Stat(outputPath)
	if err != nil {
		return "", 0, 0, fmt.Errorf("stat output: %w", err)
	}

	return sum, fileCount, fi.Size(), nil
}

func collectEntries(inputDir string, include, exclude []string) ([]zipEntry, error) {
	// Normalize include/exclude
	if len(include) == 0 {
		include = []string{"**/*"}
	}

	var entries []zipEntry

	err := filepath.WalkDir(inputDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Skip the root itself
		if p == inputDir {
			return nil
		}

		rel, err := filepath.Rel(inputDir, p)
		if err != nil {
			return err
		}

		// Use forward slashes in zip entries
		relZip := filepath.ToSlash(rel)

		// Directories: keep walking
		if d.IsDir() {
			return nil
		}

		// Apply include/exclude on relZip
		if !matchesAny(relZip, include) {
			return nil
		}
		if matchesAny(relZip, exclude) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		entries = append(entries, zipEntry{
			absPath: p,
			relPath: relZip,
			mode:    info.Mode(),
		})
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walk input_dir: %w", err)
	}
	return entries, nil
}

// matchesAny returns true if path matches at least one glob pattern.
// Supports "**" to match across directory separators.
func matchesAny(path string, patterns []string) bool {
	for _, pat := range patterns {
		if globMatch(pat, path) {
			return true
		}
	}
	return false
}

// globMatch implements a small glob matcher supporting:
// - "*" matches any chars except "/"
// - "**" matches any chars including "/"
// - "?" matches any single char except "/"
func globMatch(pattern, s string) bool {
	// Fast path: exact match
	if pattern == s {
		return true
	}

	// Tokenize pattern by '/' to handle ** cleanly.
	pParts := strings.Split(pattern, "/")
	sParts := strings.Split(s, "/")

	return matchParts(pParts, sParts)
}

func matchParts(pParts, sParts []string) bool {
	if len(pParts) == 0 {
		return len(sParts) == 0
	}

	// Handle ** at this part
	if pParts[0] == "**" {
		// ** can match zero or more path segments
		// Try consuming 0..len(sParts)
		for i := 0; i <= len(sParts); i++ {
			if matchParts(pParts[1:], sParts[i:]) {
				return true
			}
		}
		return false
	}

	if len(sParts) == 0 {
		return false
	}

	// Match this segment with *, ?
	if !matchSegment(pParts[0], sParts[0]) {
		return false
	}
	return matchParts(pParts[1:], sParts[1:])
}

func matchSegment(pat, s string) bool {
	// Segment-level glob where '*' and '?' do not cross '/'
	pi, si := 0, 0
	star := -1
	match := 0

	for si < len(s) {
		if pi < len(pat) && (pat[pi] == '?' || pat[pi] == s[si]) {
			pi++
			si++
			continue
		}
		if pi < len(pat) && pat[pi] == '*' {
			star = pi
			match = si
			pi++
			continue
		}
		if star != -1 {
			pi = star + 1
			match++
			si = match
			continue
		}
		return false
	}

	// Consume trailing '*' in pattern
	for pi < len(pat) && pat[pi] == '*' {
		pi++
	}
	return pi == len(pat)
}
