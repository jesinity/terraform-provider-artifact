package resources

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

func followRedirectsAttribute() schema.BoolAttribute {
	return schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)}
}

func timeoutSecondsAttribute() schema.Int64Attribute {
	return schema.Int64Attribute{
		Optional: true, Computed: true, Default: int64default.StaticInt64(120),
		Validators: []validator.Int64{timeoutSecondsValidator{}},
	}
}

func refreshStrategyAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		Optional: true, Computed: true, Default: stringdefault.StaticString("none"),
		Validators: []validator.String{stringChoiceValidator{"none", "missing", "sha256"}},
	}
}

// Bound the timeout to values that can be represented as a time.Duration.
const maxTimeoutSeconds = math.MaxInt64 / int64(time.Second)

type timeoutSecondsValidator struct{}

func (timeoutSecondsValidator) Description(context.Context) string {
	return fmt.Sprintf("must be between 1 and %d seconds", maxTimeoutSeconds)
}

func (v timeoutSecondsValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v timeoutSecondsValidator) ValidateInt64(ctx context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueInt64()
	if value < 1 || value > maxTimeoutSeconds {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid timeout_seconds", v.Description(ctx))
	}
}

type stringChoiceValidator []string

func (v stringChoiceValidator) Description(context.Context) string {
	return "must be one of: " + strings.Join(v, ", ")
}

func (v stringChoiceValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v stringChoiceValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	// Preserve the existing case-insensitive input handling without rewriting
	// the configured value in state.
	value := strings.ToLower(strings.TrimSpace(req.ConfigValue.ValueString()))
	if !slices.Contains(v, value) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid attribute value", v.Description(ctx))
	}
}
