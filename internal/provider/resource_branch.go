package provider

import (
	"context"
	"fmt"
	"strings"

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
	_ resource.Resource                = &branchResource{}
	_ resource.ResourceWithConfigure   = &branchResource{}
	_ resource.ResourceWithImportState = &branchResource{}
)

// branchResource manages one branch of a component, or one feature set of a product (the Branches slice).
type branchResource struct {
	client *rearm.Client
}

type dependencyModel struct {
	Component       types.String `tfsdk:"component"`
	Branch          types.String `tfsdk:"branch"`
	Release         types.String `tfsdk:"release"`
	Status          types.String `tfsdk:"status"`
	IsFollowVersion types.Bool   `tfsdk:"is_follow_version"`
}

type dependencyPatternModel struct {
	Pattern          types.String `tfsdk:"pattern"`
	TargetBranchName types.String `tfsdk:"target_branch_name"`
	DefaultStatus    types.String `tfsdk:"default_status"`
	FallbackToBase   types.String `tfsdk:"fallback_to_base"`
}

type branchModel struct {
	ID                            types.String             `tfsdk:"id"`
	Component                     types.String             `tfsdk:"component"`
	Name                          types.String             `tfsdk:"name"`
	Type                          types.String             `tfsdk:"type"`
	VersionSchema                 types.String             `tfsdk:"version_schema"`
	MarketingVersionSchema        types.String             `tfsdk:"marketing_version_schema"`
	VcsBranch                     types.String             `tfsdk:"vcs_branch"`
	AutoIntegrate                 types.String             `tfsdk:"auto_integrate"`
	FindingAnalyticsParticipation types.String             `tfsdk:"finding_analytics_participation"`
	Dependencies                  []dependencyModel        `tfsdk:"dependency"`
	DependencyPatterns            []dependencyPatternModel `tfsdk:"dependency_pattern"`
}

func NewBranchResource() resource.Resource { return &branchResource{} }

func (r *branchResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_branch"
}

func (r *branchResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A branch of a component, or a feature set of a product, identified by component name and branch name. " +
			"The dependency and dependency_pattern blocks are owned as a whole (omitting them means none); scalar attributes left unset keep whatever ReARM has.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "component/name",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"component": schema.StringAttribute{Required: true, Description: "Owning component or product name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"name": schema.StringAttribute{Required: true, Description: "Branch / feature set name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"type":                            schema.StringAttribute{Optional: true, Computed: true, Description: "BASE, FEATURE, REGULAR, RELEASE, PULL_REQUEST or TAG. The base branch type cannot change."},
			"version_schema":                  schema.StringAttribute{Optional: true, Computed: true},
			"marketing_version_schema":        schema.StringAttribute{Optional: true, Computed: true},
			"vcs_branch":                      schema.StringAttribute{Optional: true, Computed: true},
			"auto_integrate":                  schema.StringAttribute{Optional: true, Computed: true, Description: "ENABLED or DISABLED (feature sets)."},
			"finding_analytics_participation": schema.StringAttribute{Optional: true, Computed: true, Description: "INCLUDED or EXCLUDED."},
		},
		Blocks: map[string]schema.Block{
			"dependency": schema.ListNestedBlock{
				Description: "Manual dependency of a feature set, by component name; optionally pinned to a branch and a release version.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"component":         schema.StringAttribute{Required: true},
						"branch":            schema.StringAttribute{Optional: true},
						"release":           schema.StringAttribute{Optional: true, Description: "Release version to pin."},
						"status":            schema.StringAttribute{Optional: true, Computed: true, Description: "ACTIVE, IGNORED, TRANSIENT or REQUIRED."},
						"is_follow_version": schema.BoolAttribute{Optional: true, Computed: true},
					},
				},
			},
			"dependency_pattern": schema.ListNestedBlock{
				Description: "Regex-selected dependencies of a feature set.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"pattern":            schema.StringAttribute{Required: true},
						"target_branch_name": schema.StringAttribute{Optional: true, Computed: true},
						"default_status":     schema.StringAttribute{Optional: true, Computed: true},
						"fallback_to_base":   schema.StringAttribute{Optional: true, Computed: true, Description: "ENABLED or DISABLED."},
					},
				},
			},
		},
	}
}

func (r *branchResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*rearm.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *rearm.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

func (m *branchModel) toSpec() *catalog.BranchesFile {
	b := &rearm.BranchSpecInput{
		Name:                          m.Name.ValueString(),
		Type:                          optEnum[rearm.BranchType](m.Type),
		VersionSchema:                 optString(m.VersionSchema),
		MarketingVersionSchema:        optString(m.MarketingVersionSchema),
		VcsBranch:                     optString(m.VcsBranch),
		AutoIntegrate:                 optEnum[rearm.AutoIntegrateState](m.AutoIntegrate),
		FindingAnalyticsParticipation: optEnum[rearm.FindingAnalyticsParticipation](m.FindingAnalyticsParticipation),
	}
	// Blocks are owned as a whole: omitted means none, so an empty list is sent and the server clears.
	b.Dependencies = []*rearm.DependencySpecInput{}
	{
		for _, d := range m.Dependencies {
			b.Dependencies = append(b.Dependencies, &rearm.DependencySpecInput{
				Component:       d.Component.ValueString(),
				Branch:          optString(d.Branch),
				Release:         optString(d.Release),
				Status:          optEnum[rearm.Status](d.Status),
				IsFollowVersion: optBool(d.IsFollowVersion),
			})
		}
	}
	b.DependencyPatterns = []*rearm.DependencyPatternSpecInput{}
	{
		for _, p := range m.DependencyPatterns {
			b.DependencyPatterns = append(b.DependencyPatterns, &rearm.DependencyPatternSpecInput{
				Pattern:          p.Pattern.ValueString(),
				TargetBranchName: optString(p.TargetBranchName),
				DefaultStatus:    optEnum[rearm.Status](p.DefaultStatus),
				FallbackToBase:   optEnum[rearm.FallbackToBase](p.FallbackToBase),
			})
		}
	}
	f := false
	return &catalog.BranchesFile{Kind: catalog.KindBranches, BranchesSpecInput: rearm.BranchesSpecInput{
		Version: 1, Component: m.Component.ValueString(), Authoritative: &f, Branches: []*rearm.BranchSpecInput{b}}}
}

func (r *branchResource) apply(ctx context.Context, m *branchModel, diags *diagAdder) bool {
	res, err := catalog.Apply(ctx, r.client, m.toSpec(), false, nil)
	if err != nil {
		diags.err("ReARM apply failed", err.Error())
		return false
	}
	for _, ch := range res.Changes {
		if ch.Action == rearm.DeclarativeActionError {
			diags.err("ReARM rejected the branch", fmt.Sprintf("%s %s: %s", ch.Kind, ch.Name, ch.Message))
			return false
		}
	}
	return true
}

func (r *branchResource) read(ctx context.Context, m *branchModel, diags *diagAdder) bool {
	f, err := catalog.ExportBranches(ctx, r.client, m.Component.ValueString())
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false
		}
		diags.err("ReARM export failed", err.Error())
		return false
	}
	for _, b := range f.Branches {
		if b == nil || b.Name != m.Name.ValueString() {
			continue
		}
		m.ID = types.StringValue(m.Component.ValueString() + "/" + b.Name)
		m.Type = strOrNull(b.Type)
		m.VersionSchema = strOrNull(b.VersionSchema)
		m.MarketingVersionSchema = strOrNull(b.MarketingVersionSchema)
		m.VcsBranch = strOrNull(b.VcsBranch)
		m.AutoIntegrate = strOrNull(b.AutoIntegrate)
		m.FindingAnalyticsParticipation = strOrNull(b.FindingAnalyticsParticipation)
		// Nested blocks are owned as a whole by the resource, so they always reflect the server.
		{
			deps := []dependencyModel{}
			for _, d := range b.Dependencies {
				if d == nil {
					continue
				}
				deps = append(deps, dependencyModel{
					Component: types.StringValue(d.Component), Branch: strOrNull(d.Branch), Release: strOrNull(d.Release),
					Status: strOrNull(d.Status), IsFollowVersion: boolOrNull(d.IsFollowVersion),
				})
			}
			m.Dependencies = deps
		}
		{
			pats := []dependencyPatternModel{}
			for _, p := range b.DependencyPatterns {
				if p == nil {
					continue
				}
				pats = append(pats, dependencyPatternModel{
					Pattern: types.StringValue(p.Pattern), TargetBranchName: strOrNull(p.TargetBranchName),
					DefaultStatus: strOrNull(p.DefaultStatus), FallbackToBase: strOrNull(p.FallbackToBase),
				})
			}
			m.DependencyPatterns = pats
		}
		return true
	}
	return false
}

func (r *branchResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan branchModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := &diagAdder{&resp.Diagnostics}
	if !r.apply(ctx, &plan, d) || !r.read(ctx, &plan, d) {
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.AddError("Branch not found after create", plan.Component.ValueString()+"/"+plan.Name.ValueString())
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *branchResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state branchModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := &diagAdder{&resp.Diagnostics}
	if !r.read(ctx, &state, d) {
		if !resp.Diagnostics.HasError() {
			resp.State.RemoveResource(ctx)
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *branchResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan branchModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := &diagAdder{&resp.Diagnostics}
	if !r.apply(ctx, &plan, d) || !r.read(ctx, &plan, d) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the branch from Terraform state only; archiving follows the organization's prune
// setting through an authoritative Branches apply, not a single resource.
func (r *branchResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state branchModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.AddWarning("Branch kept in ReARM",
		fmt.Sprintf("%s/%s was removed from Terraform state but not archived in ReARM.", state.Component.ValueString(), state.Name.ValueString()))
}

// ImportState accepts "component/name".
func (r *branchResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid import id", "expected component/name, got "+req.ID)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("component"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
