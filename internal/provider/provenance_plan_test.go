package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// A resource as the framework plans it: a required name, an Optional+Computed kind the
// configuration leaves unset, and the provenance block.
var (
	provenanceType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"repo": tftypes.String, "path": tftypes.String, "commit": tftypes.String}}
	planResourceType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id": tftypes.String, "name": tftypes.String, "kind": tftypes.String, "provenance": provenanceType}}
)

func provenanceValue(path string) tftypes.Value {
	if path == "" {
		return tftypes.NewValue(provenanceType, nil)
	}
	return tftypes.NewValue(provenanceType, map[string]tftypes.Value{
		"repo":   tftypes.NewValue(tftypes.String, "https://github.com/acme/infra"),
		"path":   tftypes.NewValue(tftypes.String, path),
		"commit": tftypes.NewValue(tftypes.String, nil),
	})
}

// kind is nil for known-null, "?" for unknown, anything else for that value.
func resourceValue(name string, kind any, path string) tftypes.Value {
	k := tftypes.NewValue(tftypes.String, kind)
	if kind == "?" {
		k = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	}
	return tftypes.NewValue(planResourceType, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, name),
		"name":       tftypes.NewValue(tftypes.String, name),
		"kind":       k,
		"provenance": provenanceValue(path),
	})
}

func TestAProvenanceOnlyPlanIsTold(t *testing.T) {
	state := resourceValue("platform", "GENERIC", "a.tf")
	cases := []struct {
		name string
		plan tftypes.Value
		want bool
	}{
		{"only the path", resourceValue("platform", "GENERIC", "b.tf"), true},
		// The framework marks an unset Optional+Computed attribute unknown once anything changes.
		{"only the path, a computed attribute unknown", resourceValue("platform", "?", "b.tf"), true},
		{"the block added", resourceValue("platform", "GENERIC", ""), true},
		{"the path and the name", resourceValue("platform-2", "GENERIC", "b.tf"), false},
		{"only the name", resourceValue("platform-2", "GENERIC", "a.tf"), false},
		{"a computed attribute given a value", resourceValue("platform", "HELM", "b.tf"), false},
		{"nothing", resourceValue("platform", "GENERIC", "a.tf"), false},
	}
	for _, c := range cases {
		from := state
		if c.name == "the block added" {
			from, c.plan = resourceValue("platform", "GENERIC", ""), resourceValue("platform", "GENERIC", "a.tf")
		}
		if got := provenanceOnlyChange(from, c.plan); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if provenanceOnlyChange(tftypes.NewValue(planResourceType, nil), state) {
		t.Error("a create is not a provenance-only change")
	}
	if provenanceOnlyChange(state, tftypes.NewValue(planResourceType, nil)) {
		t.Error("a destroy is not a provenance-only change")
	}
}

func TestTheWarningIsWhatThePlanShows(t *testing.T) {
	plan := func(from, to tftypes.Value) *resource.ModifyPlanResponse {
		resp := &resource.ModifyPlanResponse{}
		warnIfProvenanceOnly(resource.ModifyPlanRequest{State: tfsdk.State{Raw: from}, Plan: tfsdk.Plan{Raw: to}}, resp)
		return resp
	}
	resp := plan(resourceValue("platform", "GENERIC", "a.tf"), resourceValue("platform", "?", "b.tf"))
	if resp.Diagnostics.WarningsCount() != 1 || resp.Diagnostics.HasError() {
		t.Fatalf("want one warning and no error, got %v", resp.Diagnostics)
	}
	if got := resp.Diagnostics.Warnings()[0].Summary(); got != "ReARM will not record this provenance change" {
		t.Errorf("summary: %q", got)
	}
	if plan(resourceValue("platform", "GENERIC", "a.tf"), resourceValue("platform-2", "?", "b.tf")).Diagnostics.WarningsCount() != 0 {
		t.Error("a spec change records the provenance, so it warns about nothing")
	}
}

// Every resource that sends provenance answers ModifyPlan, so the warning cannot be forgotten on one.
func TestEveryApplyingResourceWarns(t *testing.T) {
	for _, r := range []resource.Resource{NewAgentBoardResource(), NewAgentRolePresetResource(),
		NewBranchResource(), NewComponentResource()} {
		if _, ok := r.(resource.ResourceWithModifyPlan); !ok {
			t.Errorf("%T has no ModifyPlan", r)
		}
	}
}
