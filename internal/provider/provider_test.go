package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// The framework validates schemas lazily; these tests catch a broken attribute definition
// (duplicate names, invalid plan modifiers, missing required/optional flags) before a real run does.
func TestProviderSchema(t *testing.T) {
	ctx := context.Background()
	p := New("test")()
	var resp provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("provider schema: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("provider schema implementation: %v", diags)
	}
	for _, a := range []string{"uri", "api_key_id", "api_key"} {
		if _, ok := resp.Schema.Attributes[a]; !ok {
			t.Errorf("provider schema lacks attribute %q", a)
		}
	}
}

func TestResourceSchemas(t *testing.T) {
	ctx := context.Background()
	p := New("test")()
	factories := p.Resources(ctx)
	if len(factories) < 2 {
		t.Fatalf("expected the component and branch resources, got %d", len(factories))
	}
	for _, f := range factories {
		r := f()
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "rearm"}, &meta)
		var resp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("%s schema: %v", meta.TypeName, resp.Diagnostics)
		}
		if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("%s schema implementation: %v", meta.TypeName, diags)
		}
		if _, ok := resp.Schema.Attributes["name"]; !ok {
			t.Errorf("%s: no name attribute", meta.TypeName)
		}
	}
}
