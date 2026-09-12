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
	_ resource.Resource                = &componentResource{}
	_ resource.ResourceWithConfigure   = &componentResource{}
	_ resource.ResourceWithImportState = &componentResource{}
)

// componentResource manages one component or product (the Catalog slice) by name.
type componentResource struct {
	client *rearm.Client
}

type componentModel struct {
	ID                      types.String `tfsdk:"id"`
	Name                    types.String `tfsdk:"name"`
	Type                    types.String `tfsdk:"type"`
	Kind                    types.String `tfsdk:"kind"`
	VersionSchema           types.String `tfsdk:"version_schema"`
	MarketingVersionSchema  types.String `tfsdk:"marketing_version_schema"`
	VersionType             types.String `tfsdk:"version_type"`
	FeatureBranchVersioning types.String `tfsdk:"feature_branch_versioning"`
	DefaultBranch           types.String `tfsdk:"default_branch"`
	VcsURI                  types.String `tfsdk:"vcs_uri"`
	VcsType                 types.String `tfsdk:"vcs_type"`
	RepoPath                types.String `tfsdk:"repo_path"`
	Nature                  types.String `tfsdk:"nature"`
	DeviceClass             types.String `tfsdk:"device_class"`
	BranchSuffixMode        types.String `tfsdk:"branch_suffix_mode"`
}

func NewComponentResource() resource.Resource { return &componentResource{} }

func (r *componentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_component"
}

func (r *componentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A ReARM component or product, identified by name within the organization of the API key. " +
			"Attributes left unset are not managed by Terraform and keep whatever ReARM has.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Same as name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{Required: true, Description: "Component name, unique in the organization.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"type": schema.StringAttribute{Required: true, Description: "COMPONENT or PRODUCT. Cannot change.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"kind":                      schema.StringAttribute{Optional: true, Computed: true, Description: "GENERIC or HELM."},
			"version_schema":            schema.StringAttribute{Optional: true, Computed: true, Description: "Version schema of the base branch, e.g. semver."},
			"marketing_version_schema":  schema.StringAttribute{Optional: true, Computed: true},
			"version_type":              schema.StringAttribute{Optional: true, Computed: true, Description: "DEV or MARKETING."},
			"feature_branch_versioning": schema.StringAttribute{Optional: true, Computed: true, Description: "Version schema for non-base branches, e.g. Branch.Micro."},
			"default_branch": schema.StringAttribute{Optional: true, Description: "Base branch name on create (main by default). Create-only.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"vcs_uri":            schema.StringAttribute{Optional: true, Computed: true, Description: "VCS repository URI; created in the organization when missing."},
			"vcs_type":           schema.StringAttribute{Optional: true, Computed: true, Description: "VCS type, e.g. git."},
			"repo_path":          schema.StringAttribute{Optional: true, Computed: true, Description: "Path inside a monorepo."},
			"nature":             schema.StringAttribute{Optional: true, Computed: true, Description: "SOFTWARE or HARDWARE."},
			"device_class":       schema.StringAttribute{Optional: true, Computed: true, Description: "NONE, MEDICAL_UNTRACKED or MEDICAL_TRACKED."},
			"branch_suffix_mode": schema.StringAttribute{Optional: true, Computed: true},
		},
	}
}

func (r *componentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (m *componentModel) toSpec() *catalog.CatalogFile {
	comp := &rearm.CatalogComponentInput{
		Name:                    m.Name.ValueString(),
		Type:                    rearm.ComponentType(m.Type.ValueString()),
		Kind:                    optEnum[rearm.ComponentKind](m.Kind),
		VersionSchema:           optString(m.VersionSchema),
		MarketingVersionSchema:  optString(m.MarketingVersionSchema),
		VersionType:             optEnum[rearm.VersionType](m.VersionType),
		FeatureBranchVersioning: optString(m.FeatureBranchVersioning),
		DefaultBranch:           optString(m.DefaultBranch),
		VcsUri:                  optString(m.VcsURI),
		VcsType:                 optString(m.VcsType),
		RepoPath:                optString(m.RepoPath),
		Nature:                  optEnum[rearm.ComponentNature](m.Nature),
		DeviceClass:             optEnum[rearm.DeviceClass](m.DeviceClass),
		BranchSuffixMode:        optEnum[rearm.BranchSuffixMode](m.BranchSuffixMode),
	}
	f := false
	return &catalog.CatalogFile{CatalogSpecInput: rearm.CatalogSpecInput{
		Kind: rearm.DeclarativeKindCatalog, Version: 1, Authoritative: &f, Components: []*rearm.CatalogComponentInput{comp}}}
}

func (r *componentResource) apply(ctx context.Context, m *componentModel, diags *diagAdder) bool {
	res, err := catalog.Apply(ctx, r.client, m.toSpec(), false, nil)
	if err != nil {
		diags.err("ReARM apply failed", err.Error())
		return false
	}
	for _, ch := range res.Changes {
		if ch.Action == rearm.DeclarativeActionError {
			diags.err("ReARM rejected the component", fmt.Sprintf("%s %s: %s", ch.Kind, ch.Name, ch.Message))
			return false
		}
	}
	return true
}

// read refreshes the model from the export; returns false when the component no longer exists.
func (r *componentResource) read(ctx context.Context, m *componentModel, diags *diagAdder) bool {
	f, err := catalog.ExportCatalog(ctx, r.client, []string{m.Name.ValueString()})
	if err != nil {
		if catalog.IsNotFound(err) {
			return false // gone on the server: the caller drops it from state
		}
		diags.err("ReARM export failed", err.Error())
		return false
	}
	for _, c := range f.Components {
		if c == nil || c.Name != m.Name.ValueString() {
			continue
		}
		m.ID = types.StringValue(c.Name)
		m.Type = types.StringValue(string(c.Type))
		m.Kind = strOrNull(c.Kind)
		m.VersionSchema = strOrNull(c.VersionSchema)
		m.MarketingVersionSchema = strOrNull(c.MarketingVersionSchema)
		m.VersionType = strOrNull(c.VersionType)
		m.FeatureBranchVersioning = strOrNull(c.FeatureBranchVersioning)
		m.VcsURI = keepEquivalent(m.VcsURI, c.VcsUri, sameVcsURI)
		m.VcsType = keepEquivalent(m.VcsType, c.VcsType, strings.EqualFold)
		m.RepoPath = strOrNull(c.RepoPath)
		m.Nature = strOrNull(c.Nature)
		m.DeviceClass = strOrNull(c.DeviceClass)
		m.BranchSuffixMode = strOrNull(c.BranchSuffixMode)
		return true
	}
	return false
}

func (r *componentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan componentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := &diagAdder{&resp.Diagnostics}
	if !r.apply(ctx, &plan, d) || !r.read(ctx, &plan, d) {
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.AddError("Component not found after create", plan.Name.ValueString())
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *componentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state componentModel
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

func (r *componentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan componentModel
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

// Delete removes the component from Terraform state only. Archiving through the declarative API is
// governed by the organization's prune setting, not by a single resource, so ReARM keeps the row.
func (r *componentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state componentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.AddWarning("Component kept in ReARM",
		fmt.Sprintf("%s was removed from Terraform state but not archived in ReARM. Archive it in the UI or through an authoritative Catalog apply.", state.Name.ValueString()))
}

func (r *componentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
