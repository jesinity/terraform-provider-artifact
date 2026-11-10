package resources

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestZipLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	input := t.TempDir()
	writeFile(t, filepath.Join(input, "a.txt"), "text")
	writeFile(t, filepath.Join(input, "b.json"), "{}")
	out := filepath.Join(t.TempDir(), "bundle.zip")
	r := NewZipResource()
	model := zipTestModel(input, out)
	plan := modelState(t, r, model)

	created := resource.CreateResponse{State: plan}
	r.Create(ctx, resource.CreateRequest{Plan: planFromState(plan)}, &created)
	requireNoErrors(t, created.Diagnostics)
	assertZipState(t, created.State, out, map[string]string{"a.txt": "text", "b.json": "{}"})

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	requireNoErrors(t, read.Diagnostics)
	if !read.State.Raw.Equal(created.State.Raw) {
		t.Fatal("refresh changed state for an unchanged ZIP")
	}

	model.Include = types.ListValueMust(types.StringType, []attr.Value{types.StringValue("**/*.txt")})
	updatePlan := modelState(t, r, model)
	updated := resource.UpdateResponse{State: updatePlan}
	r.Update(ctx, resource.UpdateRequest{State: read.State, Plan: planFromState(updatePlan)}, &updated)
	requireNoErrors(t, updated.Diagnostics)
	assertZipState(t, updated.State, out, map[string]string{"a.txt": "text"})

	deleted := resource.DeleteResponse{State: updated.State}
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &deleted)
	requireNoErrors(t, deleted.Diagnostics)
	if !deleted.State.Raw.IsNull() {
		t.Fatal("destroy retained resource state")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("destroy did not remove the ZIP: %v", err)
	}
}

func TestZipReadRemovesMissingArchive(t *testing.T) {
	t.Parallel()
	r := NewZipResource()
	state := modelState(t, r, zipTestModel(t.TempDir(), filepath.Join(t.TempDir(), "missing.zip")))
	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	requireNoErrors(t, resp.Diagnostics)
	if !resp.State.Raw.IsNull() {
		t.Fatal("refresh retained state for a missing archive")
	}
}

func TestZipReadRebuildOnRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	input := t.TempDir()
	source := filepath.Join(input, "source.txt")
	writeFile(t, source, "original")
	out := filepath.Join(t.TempDir(), ".terraform", "bundle.zip")
	r := NewZipResource()
	model := zipTestModel(input, out)
	model.RebuildOnRead = types.BoolValue(true)
	plan := modelState(t, r, model)
	created := resource.CreateResponse{State: plan}
	r.Create(ctx, resource.CreateRequest{Plan: planFromState(plan)}, &created)
	requireNoErrors(t, created.Diagnostics)
	assertZipState(t, created.State, out, map[string]string{"source.txt": "original"})
	original, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	read := func(state tfsdk.State) resource.ReadResponse {
		resp := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		return resp
	}

	// A present, unchanged archive has no state diff.
	unchanged := read(created.State)
	requireNoErrors(t, unchanged.Diagnostics)
	if !unchanged.State.Raw.Equal(created.State.Raw) {
		t.Fatal("unchanged archive changed state")
	}

	// A fresh CI checkout can restore the missing file during refresh.
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	restored := read(unchanged.State)
	requireNoErrors(t, restored.Diagnostics)
	if !restored.State.Raw.Equal(created.State.Raw) {
		t.Fatal("identical rebuilt archive changed state")
	}
	rebuilt, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(rebuilt) != string(original) {
		t.Fatal("rebuilt archive bytes differ")
	}
	assertZipState(t, restored.State, out, map[string]string{"source.txt": "original"})
	writeFile(t, out, "damaged archive")
	repaired := read(restored.State)
	requireNoErrors(t, repaired.Diagnostics)
	if !repaired.State.Raw.Equal(created.State.Raw) {
		t.Fatal("repairing a damaged local ZIP changed state")
	}
	assertZipState(t, repaired.State, out, map[string]string{"source.txt": "original"})

	// A modified source must leave a real plan, even if the old ZIP existed.
	writeFile(t, source, "modified")
	changed := read(repaired.State)
	requireNoErrors(t, changed.Diagnostics)
	if !changed.State.Raw.IsNull() {
		t.Fatal("source change did not remove the old resource from state")
	}
	changedBytes, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(changedBytes) == string(original) {
		t.Fatal("source change did not rebuild the archive")
	}
	changedSHA := sha256.Sum256(changedBytes)
	originalSHA := sha256.Sum256(original)
	if hex.EncodeToString(changedSHA[:]) == hex.EncodeToString(originalSHA[:]) {
		t.Fatal("source change produced the original hash")
	}
}

func TestZipReadRebuildErrorRetainsState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	input := t.TempDir()
	writeFile(t, filepath.Join(input, "source.txt"), "content")
	out := filepath.Join(t.TempDir(), "bundle.zip")
	r := NewZipResource()
	model := zipTestModel(input, out)
	model.RebuildOnRead = types.BoolValue(true)
	plan := modelState(t, r, model)
	created := resource.CreateResponse{State: plan}
	r.Create(ctx, resource.CreateRequest{Plan: planFromState(plan)}, &created)
	requireNoErrors(t, created.Diagnostics)
	if err := os.RemoveAll(input); err != nil {
		t.Fatal(err)
	}
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if !read.Diagnostics.HasError() {
		t.Fatal("missing input_dir did not report a rebuild error")
	}
	if !read.State.Raw.Equal(created.State.Raw) {
		t.Fatal("failed rebuild changed state")
	}
}

func TestZipExtraTriggersReplacement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := NewZipResource()
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	requireNoErrors(t, schemaResp.Diagnostics)
	triggers, ok := schemaResp.Schema.Attributes["extra_triggers"].(schema.MapAttribute)
	if !ok || len(triggers.PlanModifiers) == 0 {
		t.Fatal("extra_triggers has no map plan modifiers")
	}
	a := types.MapValueMust(types.StringType, map[string]attr.Value{"sha": types.StringValue("a")})
	b := types.MapValueMust(types.StringType, map[string]attr.Value{"sha": types.StringValue("b")})
	for _, tc := range []struct {
		name    string
		prior   types.Map
		next    types.Map
		create  bool
		destroy bool
		replace bool
	}{
		{name: "unchanged", prior: a, next: a},
		{name: "changed", prior: a, next: b, replace: true},
		{name: "added", prior: types.MapNull(types.StringType), next: a, replace: true},
		{name: "removed", prior: a, next: types.MapNull(types.StringType)},
		{name: "creation", prior: types.MapNull(types.StringType), next: a, create: true},
		{name: "destruction", prior: a, next: types.MapNull(types.StringType), destroy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := zipTestModel("input", "output.zip")
			model.ExtraTriggers = tc.prior
			prior := modelState(t, r, model)
			if tc.create {
				prior.RemoveResource(ctx)
			}
			model.ExtraTriggers = tc.next
			next := modelState(t, r, model)
			if tc.destroy {
				next.RemoveResource(ctx)
			}
			req := planmodifier.MapRequest{
				Path:  path.Root("extra_triggers"),
				State: prior, StateValue: tc.prior,
				Plan: planFromState(next), PlanValue: tc.next,
				Config: tfsdk.Config{Schema: next.Schema, Raw: next.Raw}, ConfigValue: tc.next,
			}
			resp := planmodifier.MapResponse{PlanValue: tc.next}
			for _, modifier := range triggers.PlanModifiers {
				modifier.PlanModifyMap(ctx, req, &resp)
			}
			requireNoErrors(t, resp.Diagnostics)
			if resp.RequiresReplace != tc.replace {
				t.Fatalf("RequiresReplace = %t, want %t", resp.RequiresReplace, tc.replace)
			}
		})
	}
}

func zipTestModel(input, out string) ZipModel {
	return ZipModel{
		InputDir: types.StringValue(input), OutputPath: types.StringValue(out),
		Include: types.ListNull(types.StringType), Exclude: types.ListNull(types.StringType),
		ExtraTriggers: types.MapNull(types.StringType), Deterministic: types.BoolValue(true),
		RebuildOnRead: types.BoolNull(),
	}
}

func assertZipState(t *testing.T, state tfsdk.State, out string, want map[string]string) {
	t.Helper()
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactState(t, state, "zip_sha256", "file_size", out, string(data))
	var id, hash types.String
	requireNoErrors(t, state.GetAttribute(context.Background(), path.Root("id"), &id))
	requireNoErrors(t, state.GetAttribute(context.Background(), path.Root("zip_sha256"), &hash))
	if id.ValueString() != zipID(out, hash.ValueString()) {
		t.Errorf("id %q is inconsistent with zip_sha256 %q", id.ValueString(), hash.ValueString())
	}
	var count types.Int64
	requireNoErrors(t, state.GetAttribute(context.Background(), path.Root("file_count"), &count))
	if !count.Equal(types.Int64Value(int64(len(want)))) {
		t.Errorf("file_count = %v, want %d", count, len(want))
	}
	archive, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != len(want) {
		t.Fatalf("ZIP has %d entries, want %d", len(archive.File), len(want))
	}
	seen := map[string]bool{}
	for _, file := range archive.File {
		expected, exists := want[file.Name]
		if !exists || seen[file.Name] {
			t.Fatalf("unexpected or duplicate ZIP entry %q", file.Name)
		}
		seen[file.Name] = true
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("reading %q: %v, closing: %v", file.Name, readErr, closeErr)
		}
		if string(data) != expected {
			t.Errorf("ZIP entry %q = %q, want %q", file.Name, data, expected)
		}
	}
}
