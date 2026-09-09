package provider

import (
	"strings"

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

// sameVcsURI reports whether two repository URIs name the same repository once ReARM's
// normalisation (scheme dropped, trailing slash and .git ignored, case-insensitive host) is applied.
func sameVcsURI(a, b string) bool {
	return normVcsURI(a) == normVcsURI(b)
}

func normVcsURI(u string) string {
	u = strings.TrimSpace(u)
	for _, p := range []string{"https://", "http://", "ssh://", "git@"} {
		u = strings.TrimPrefix(u, p)
	}
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	return strings.ToLower(u)
}

// keepEquivalent returns the configured value when the server's value is only a normalised form of
// it, so a stable configuration never shows drift; otherwise the server's value.
func keepEquivalent(configured types.String, server *string, same func(a, b string) bool) types.String {
	if server == nil || *server == "" {
		// The server records some of these only on creation (a repository's type, for
		// example); when it has nothing, the configured value stands rather than drifting to null.
		if !configured.IsNull() && !configured.IsUnknown() {
			return configured
		}
		return types.StringNull()
	}
	if !configured.IsNull() && !configured.IsUnknown() && same(configured.ValueString(), *server) {
		return configured
	}
	return types.StringValue(*server)
}
