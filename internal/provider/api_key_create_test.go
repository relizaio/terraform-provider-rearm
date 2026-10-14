package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	rearm "github.com/relizaio/rearm-client-go"
	"github.com/relizaio/rearm-client-go/catalog"
)

// RD3-11 run-1 T-1: a create goes through the framework with the plan the way Terraform sends it -- id, uuid and
// secret_slots unknown -- and ends with them known in state. The conversion helpers alone passed while every
// real create failed on the unknown secret_slots.
func TestApiKeyCreateThroughTheFramework(t *testing.T) {
	sent, resp := createThroughFramework(t, "release pipeline", "release pipeline")
	if resp.Diagnostics.HasError() {
		t.Fatalf("create: %v", resp.Diagnostics)
	}
	if sent["name"] != "ci-release" || sent["status"] != "ACTIVE" {
		t.Errorf("sent: %v", sent)
	}
	var got apiKeyModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state: %v", d)
	}
	if got.ID.ValueString() != "FREEFORM__org__ord__o1" || got.UUID.ValueString() != "u-1" {
		t.Errorf("id and uuid: %v %v", got.ID, got.UUID)
	}
	if got.SecretSlots.IsUnknown() || got.SecretSlots.IsNull() || len(got.SecretSlots.Elements()) != 0 {
		t.Errorf("secret_slots known and empty after create: %v", got.SecretSlots)
	}
	if !got.Notes.Equal(types.StringValue("release pipeline")) {
		t.Errorf("notes: %v", got.Notes)
	}
}

// A heredoc note goes to ReARM as written and, read back normalised, stays in state as configured: no
// inconsistent result after apply.
func TestApiKeyCreateKeepsAHeredocNote(t *testing.T) {
	sent, resp := createThroughFramework(t, "CI  pipeline\n", "CI pipeline")
	if resp.Diagnostics.HasError() {
		t.Fatalf("create: %v", resp.Diagnostics)
	}
	if sent["notes"] != "CI  pipeline\n" {
		t.Errorf("the note is sent as configured: %q", sent["notes"])
	}
	var got apiKeyModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state: %v", d)
	}
	if !got.Notes.Equal(types.StringValue("CI  pipeline\n")) {
		t.Errorf("notes: %v", got.Notes)
	}
}

// createThroughFramework creates key ci-release with the given notes, ReARM reading them back as readNotes,
// and returns the key entry sent to ReARM and the create response.
func createThroughFramework(t *testing.T, notes, readNotes string) (map[string]any, resource.CreateResponse) {
	t.Helper()
	ctx := context.Background()
	r := &apiKeyResource{}
	var sch resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sch)
	typ := sch.Schema.Type().TerraformType(ctx).(tftypes.Object)
	vals := map[string]tftypes.Value{}
	for name, at := range typ.AttributeTypes {
		vals[name] = tftypes.NewValue(at, nil)
	}
	vals["name"] = tftypes.NewValue(tftypes.String, "ci-release")
	vals["notes"] = tftypes.NewValue(tftypes.String, notes)
	for _, unknown := range []string{"id", "uuid", "secret_slots"} {
		vals[unknown] = tftypes.NewValue(typ.AttributeTypes[unknown], tftypes.UnknownValue)
	}
	plan := tfsdk.Plan{Schema: sch.Schema, Raw: tftypes.NewValue(typ, vals)}

	var sent map[string]any
	applySpec = func(_ context.Context, _ *rearm.Client, f any, _ bool, _ *catalog.Source) (*catalog.Result, error) {
		sent = f.(*catalog.ApiKeysFile).Spec["keys"].([]any)[0].(map[string]any)
		return &catalog.Result{}, nil
	}
	readApiKey = func(context.Context, *rearm.Client, string) (map[string]any, error) {
		return map[string]any{"uuid": "u-1", "keyId": "FREEFORM__org__ord__o1", "name": "ci-release", "type": "FREEFORM",
			"status": "ACTIVE", "notes": readNotes, "secretSlots": []any{}}, nil
	}
	defer func() { applySpec = catalog.Apply; readApiKey = defaultReadApiKey }()

	resp := resource.CreateResponse{State: tfsdk.State{Schema: sch.Schema, Raw: tftypes.NewValue(typ, nil)}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	return sent, resp
}