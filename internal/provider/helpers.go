package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// optString returns a pointer for a set, non-empty string attribute; nil means "not managed here".
func optString(v types.String) *string {
	if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
		return nil
	}
	s := v.ValueString()
	return &s
}

// optEnum converts an optional string attribute into a pointer of a string-based enum type.
func optEnum[T ~string](v types.String) *T {
	if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
		return nil
	}
	t := T(v.ValueString())
	return &t
}

// optBool returns a pointer for a set bool attribute.
func optBool(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	b := v.ValueBool()
	return &b
}

// strOrNull maps an optional server value back to state, keeping null when the server has none.
func strOrNull[T ~string](p *T) types.String {
	if p == nil || string(*p) == "" {
		return types.StringNull()
	}
	return types.StringValue(string(*p))
}

func boolOrNull(p *bool) types.Bool {
	if p == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*p)
}
