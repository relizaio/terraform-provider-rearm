package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rearm "github.com/relizaio/rearm-client-go"
	"github.com/relizaio/rearm-client-go/catalog"
)

var (
	_ resource.Resource                = &agentRolePresetResource{}
	_ resource.ResourceWithConfigure   = &agentRolePresetResource{}
	_ resource.ResourceWithImportState = &agentRolePresetResource{}
)

// agentRolePresetResource manages one of the organization's role presets by name, sent as a
// presets file holding that one preset and not authoritative, so the organization's other presets
// are left alone. Presets seed new boards; changing one does not change existing boards.
type agentRolePresetResource struct {
	client *rearm.Client
	source *catalog.Source
}

// presetModel is a role at the top level, with the resource's id beside it.
type presetModel struct {
	ID                   types.String          `tfsdk:"id"`
	Name                 types.String          `tfsdk:"name"`
	Prompt               types.String          `tfsdk:"prompt"`
	OrderIndex           types.Int64           `tfsdk:"order_index"`
	WipLimit             types.Int64           `tfsdk:"wip_limit"`
	RequireDistinctAgent types.Bool            `tfsdk:"require_distinct_agent"`
	Active               types.Bool            `tfsdk:"active"`
	Kind                 types.String          `tfsdk:"kind"`
	Necessity            types.String          `tfsdk:"necessity"`
	HumanGate            types.String          `tfsdk:"human_gate"`
	RequiredCapabilities []types.String        `tfsdk:"required_capabilities"`
	HopBudgetMicros      types.Int64           `tfsdk:"hop_budget_micros"`
	BlindReview          types.Bool            `tfsdk:"blind_review"`
	RequiredInputs       []requiredInputModel  `tfsdk:"required_inputs"`
	ProducesOutputs      []producedOutputModel `tfsdk:"produces_outputs"`
	Strength             *strengthModel        `tfsdk:"strength"`
	Source               *sourceModel          `tfsdk:"provenance"`
}

func (p *presetModel) role() roleModel {
	return roleModel{Name: p.Name, Prompt: p.Prompt, OrderIndex: p.OrderIndex, WipLimit: p.WipLimit,
		RequireDistinctAgent: p.RequireDistinctAgent, Active: p.Active, Kind: p.Kind, Necessity: p.Necessity,
		HumanGate: p.HumanGate, RequiredCapabilities: p.RequiredCapabilities, HopBudgetMicros: p.HopBudgetMicros,
		BlindReview: p.BlindReview, RequiredInputs: p.RequiredInputs, ProducesOutputs: p.ProducesOutputs, Strength: p.Strength}
}

func (p *presetModel) setRole(r roleModel) {
	p.Name, p.Prompt, p.OrderIndex, p.WipLimit = r.Name, r.Prompt, r.OrderIndex, r.WipLimit
	p.RequireDistinctAgent, p.Active, p.Kind, p.Necessity = r.RequireDistinctAgent, r.Active, r.Kind, r.Necessity
	p.HumanGate, p.RequiredCapabilities, p.HopBudgetMicros = r.HumanGate, r.RequiredCapabilities, r.HopBudgetMicros
	p.BlindReview = r.BlindReview
	p.RequiredInputs, p.ProducesOutputs, p.Strength = r.RequiredInputs, r.ProducesOutputs, r.Strength
}

func NewAgentRolePresetResource() resource.Resource { return &agentRolePresetResource{} }

var _ resource.ResourceWithModifyPlan = &agentRolePresetResource{}

// ModifyPlan warns when the only change is to the provenance block, which ReARM does not record.
func (r *agentRolePresetResource) ModifyPlan(_ context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	warnIfProvenanceOnly(req, resp)
}

func (r *agentRolePresetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent_role_preset"
}

func (r *agentRolePresetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := roleAttributes()
	attrs["id"] = schema.StringAttribute{Computed: true, Description: "Same as name.",
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
	attrs["provenance"] = resourceSourceAttribute()
	attrs["name"] = schema.StringAttribute{Required: true,
		Description:   "Preset name. coordinator-tracker and coordinator-board-truth seed a new board's coordinator prompt.",
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
	resp.Schema = schema.Schema{
		Description: "One of the organization's agent role presets, which seed new boards. Attributes left unset " +
			"are not managed by Terraform. Destroying the resource deactivates the preset.",
		Attributes: attrs,
	}
}

func (r *agentRolePresetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if pd := configured(req, resp); pd != nil {
		r.client, r.source = pd.client, pd.source
	}
}

func presetFile(entry map[string]any) *catalog.RolePresetsFile {
	return &catalog.RolePresetsFile{Spec: map[string]any{
		"kind": string(rearm.DeclarativeKindRolePresets), "version": 1, "authoritative": false,
		"presets": []any{entry},
	}}
}

func (r *agentRolePresetResource) apply(ctx context.Context, m *presetModel, d *diagAdder) bool {
	role := m.role()
	res, err := applySpec(ctx, r.client, presetFile(roleToSpec(&role)), false, sourceFor(r.source, m.Source))
	if err != nil {
		d.err("ReARM apply failed", err.Error())
		return false
	}
	return reportChanges(res, d, "preset")
}

func (r *agentRolePresetResource) read(ctx context.Context, m *presetModel, full bool, d *diagAdder) bool {
	f, err := catalog.ExportRolePresets(ctx, r.client)
	if err != nil {
		d.err("ReARM export failed", err.Error())
		return false
	}
	for _, x := range list(f.Spec["presets"]) {
		e, ok := x.(map[string]any)
		if !ok || !sameName(str(e["name"]), m.Name.ValueString()) {
			continue
		}
		if a, ok := e["active"].(bool); ok && !a && m.Active.IsNull() {
			return false // deactivated outside Terraform: the plan creates it again
		}
		role := m.role()
		roleFromExport(&role, e, full)
		m.setRole(role)
		m.ID = m.Name
		return true
	}
	return false
}

func (r *agentRolePresetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan presetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := &diagAdder{&resp.Diagnostics}
	if !r.apply(ctx, &plan, d) || !r.read(ctx, &plan, false, d) {
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.AddError("Preset not found after create", plan.Name.ValueString())
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *agentRolePresetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state presetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := &diagAdder{&resp.Diagnostics}
	full := state.ID.IsNull() || state.ID.IsUnknown()
	if !r.read(ctx, &state, full, d) {
		if !resp.Diagnostics.HasError() {
			resp.State.RemoveResource(ctx)
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *agentRolePresetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan presetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := &diagAdder{&resp.Diagnostics}
	if !r.apply(ctx, &plan, d) || !r.read(ctx, &plan, false, d) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete deactivates the preset (declarative-boards D7): boards it seeded keep their copies.
func (r *agentRolePresetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state presetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.deactivate(ctx, &state, &diagAdder{&resp.Diagnostics})
}

// deactivate is the delete: an apply of active: false, and where it came from is worth recording
// too.
func (r *agentRolePresetResource) deactivate(ctx context.Context, m *presetModel, d *diagAdder) bool {
	res, err := applySpec(ctx, r.client, presetFile(map[string]any{"name": m.Name.ValueString(), "active": false}), false,
		sourceFor(r.source, m.Source))
	if err != nil {
		d.err("ReARM apply failed", err.Error())
		return false
	}
	return reportChanges(res, d, "preset")
}

func (r *agentRolePresetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
