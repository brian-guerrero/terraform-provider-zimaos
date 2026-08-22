package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// oneOfStatusValidator constrains desired_state to the confirmed status enum
// (start|stop|restart) accepted by PUT /compose/{id}/status.
type oneOfStatusValidator struct{}

var _ validator.String = oneOfStatusValidator{}

func (v oneOfStatusValidator) Description(_ context.Context) string {
	return "value must be one of: start, stop, restart"
}

func (v oneOfStatusValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v oneOfStatusValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	allowed := map[string]bool{"start": true, "stop": true, "restart": true}
	if !allowed[req.ConfigValue.ValueString()] {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid desired_state",
			fmt.Sprintf("expected one of [start stop restart], got %q", req.ConfigValue.ValueString()),
		)
	}
}
