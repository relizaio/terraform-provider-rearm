package provider

import (
	"context"
	"fmt"

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
	_ resource.Resource                = &agentBoardResource{}
	_ resource.ResourceWithConfigure   = &agentBoardResource{}
	_ resource.ResourceWithImportState = &agentBoardResource{}
)

// agentBoardResource manages one task board by name, through the same board file the CLI applies
// (ai-plans/agentic/declarative-boards.md): the board, its settings and its roles in one apply.
type agentBoardResource struct {
	client *rearm.Client
	source *catalog.Source
}

type agentBoardModel struct {
	ID                     types.String            `tfsdk:"id"`
	Name                   types.String            `tfsdk:"name"`
	Description            types.String            `tfsdk:"description"`
	Target                 types.String            `tfsdk:"target"`
	Sources                []types.String          `tfsdk:"sources"`
	DocumentsRepo          types.String            `tfsdk:"documents_repo"`
	DocumentPaths          map[string]types.String `tfsdk:"document_paths"`
	PriorityType           types.String            `tfsdk:"priority_type"`
	PerAgentWipLimit       types.Int64             `tfsdk:"per_agent_wip_limit"`
	DefaultTaskLevel       types.Int64             `tfsdk:"default_task_level"`
	DefaultInputResolution types.String            `tfsdk:"default_input_resolution"`
	CoordinatorPrompt      types.String            `tfsdk:"coordinator_prompt"`
	CoordinatorCaps        []types.String          `tfsdk:"coordinator_capabilities"`
	Settings               *boardSettingsModel     `tfsdk:"settings"`
	Roles                  []roleModel             `tfsdk:"roles"`
	Source                 *sourceModel            `tfsdk:"provenance"`
}

type boardSettingsModel struct {
	BudgetMicros            types.Int64 `tfsdk:"budget_micros"`
	SoftAlertPercent        types.Int64 `tfsdk:"soft_alert_percent"`
	CycleCap                types.Int64 `tfsdk:"cycle_cap"`
	NoProgressRepeatsToStop types.Int64 `tfsdk:"no_progress_repeats_to_stop"`
	BlockingPriority        types.Int64 `tfsdk:"blocking_priority"`
	CompletionPriority      types.Int64 `tfsdk:"completion_priority"`
	HumanQueueAgeMinutes    types.Int64 `tfsdk:"human_queue_age_minutes"`
}

func NewAgentBoardResource() resource.Resource { return &agentBoardResource{} }

var _ resource.ResourceWithModifyPlan = &agentBoardResource{}

// ModifyPlan warns when the only change is to the provenance block, which ReARM does not record.
func (r *agentBoardResource) ModifyPlan(_ context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	warnIfProvenanceOnly(req, resp)
}

func (r *agentBoardResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent_board"
}

func (r *agentBoardResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	role := roleAttributes()
	role["name"] = schema.StringAttribute{Required: true, Description: "Role name; roles are matched by name."}
	resp.Schema = schema.Schema{
		Description: "A ReARM agent task board, identified by name within the organization of the API key. " +
			"Attributes left unset are not managed by Terraform and keep whatever ReARM has. Destroying the " +
			"resource archives the board; its tasks and history stay.",
		Attributes: map[string]schema.Attribute{
			"provenance": resourceSourceAttribute(),
			"id": schema.StringAttribute{Computed: true, Description: "Same as name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{Required: true, Description: "Board name, unique in the organization.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"description": schema.StringAttribute{Optional: true},
			"target":      schema.StringAttribute{Optional: true, Description: "Name of the component the board builds; required to create one."},
			"sources": schema.ListAttribute{Optional: true, ElementType: types.StringType,
				Description: "Wired trackers, e.g. github:acme/platform."},
			"documents_repo": schema.StringAttribute{Optional: true, Description: "Repository the board's documents go to; must be among the sources when there are any."},
			"document_paths": schema.MapAttribute{Optional: true, ElementType: types.StringType,
				Description: "Path templates per specification type, e.g. ARCHITECTURE = \"docs/design/{task}.md\"."},
			"priority_type":            schema.StringAttribute{Optional: true, Description: "LAX or STRICT."},
			"per_agent_wip_limit":      schema.Int64Attribute{Optional: true},
			"default_task_level":       schema.Int64Attribute{Optional: true},
			"default_input_resolution": schema.StringAttribute{Optional: true, Description: "LATEST_PASSING or STRICT_LATEST."},
			"coordinator_prompt": schema.StringAttribute{Optional: true,
				Description: "Left unset on a new board, it is seeded from the organization's coordinator preset."},
			"coordinator_capabilities": schema.SetAttribute{Optional: true, ElementType: types.StringType,
				Description: "Verbs the coordinator seat performs itself on this board: PR_MERGE when it merges once " +
					"the last required role has passed, CODE_PUSH on a docs-only board. The tracker verbs are always " +
					"the coordinator's and are refused here. Unset is not managed; [] is none."},
			"settings": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Budget and stops. Only the settings set here are managed.",
				Attributes: map[string]schema.Attribute{
					"budget_micros":               schema.Int64Attribute{Optional: true, Description: "What the board may spend, in USD micros."},
					"soft_alert_percent":          schema.Int64Attribute{Optional: true},
					"cycle_cap":                   schema.Int64Attribute{Optional: true},
					"no_progress_repeats_to_stop": schema.Int64Attribute{Optional: true},
					"blocking_priority":           schema.Int64Attribute{Optional: true, Description: "Findings at or above this priority send work back."},
					"completion_priority":         schema.Int64Attribute{Optional: true, Description: "Findings at or above this priority stop completion."},
					"human_queue_age_minutes":     schema.Int64Attribute{Optional: true, Description: "Minutes a task may wait on a person before the board sends AGENT_TASK_QUEUE_AGE; 0 is off."},
				},
			},
			"roles": schema.ListNestedAttribute{
				Optional: true,
				Description: "The board's roles, in order. Set, it is the role list: a role not in it is deactivated, " +
					"never deleted. Unset, the roles are not managed here.",
				NestedObject: schema.NestedAttributeObject{Attributes: role},
			},
		},
	}
}

func (r *agentBoardResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if pd := configured(req, resp); pd != nil {
		r.client, r.source = pd.client, pd.source
	}
}

// toSpec builds the board file from the plan: only what the configuration sets.
func (m *agentBoardModel) toSpec() *catalog.BoardFile {
	s := map[string]any{"kind": string(rearm.DeclarativeKindBoard), "version": 1, "name": m.Name.ValueString()}
	putString(s, "description", m.Description)
	putString(s, "target", m.Target)
	putString(s, "documentsRepo", m.DocumentsRepo)
	putString(s, "priorityType", m.PriorityType)
	putInt(s, "perAgentWipLimit", m.PerAgentWipLimit)
	putInt(s, "defaultTaskLevel", m.DefaultTaskLevel)
	putString(s, "defaultInputResolution", m.DefaultInputResolution)
	putString(s, "coordinatorPrompt", m.CoordinatorPrompt)
	if m.Sources != nil {
		src := []any{}
		for _, v := range m.Sources {
			src = append(src, v.ValueString())
		}
		s["sources"] = src
	}
	if m.CoordinatorCaps != nil {
		caps := []any{}
		for _, v := range m.CoordinatorCaps {
			caps = append(caps, v.ValueString())
		}
		s["coordinatorCapabilities"] = caps
	}
	if m.DocumentPaths != nil {
		p := map[string]any{}
		for k, v := range m.DocumentPaths {
			p[k] = v.ValueString()
		}
		s["documentPaths"] = p
	}
	if m.Settings != nil {
		st := map[string]any{}
		putInt(st, "budgetMicros", m.Settings.BudgetMicros)
		putInt(st, "softAlertPercent", m.Settings.SoftAlertPercent)
		putInt(st, "cycleCap", m.Settings.CycleCap)
		putInt(st, "noProgressRepeatsToStop", m.Settings.NoProgressRepeatsToStop)
		putInt(st, "blockingPriority", m.Settings.BlockingPriority)
		putInt(st, "completionPriority", m.Settings.CompletionPriority)
		putInt(st, "humanQueueAgeMinutes", m.Settings.HumanQueueAgeMinutes)
		s["settings"] = st
	}
	if m.Roles != nil {
		roles := []any{}
		for i := range m.Roles {
			roles = append(roles, roleToSpec(&m.Roles[i]))
		}
		s["roles"] = roles
	}
	return &catalog.BoardFile{Spec: s}
}

// fromExport refreshes the model from the server's board file: everything on an import (full),
// otherwise only what the state manages. With roles managed, a role the server has that the state
// does not appears so the plan removes it, and one deactivated outside Terraform drops out so the
// plan declares it again.
func (m *agentBoardModel) fromExport(spec map[string]any, full bool) {
	m.ID = types.StringValue(str(spec["name"]))
	m.Name = m.ID
	readString(&m.Description, spec, "description", full)
	readString(&m.Target, spec, "target", full)
	if full || !m.DocumentsRepo.IsNull() {
		var server *string
		if v := str(spec["documentsRepo"]); v != "" {
			server = &v
		}
		m.DocumentsRepo = keepEquivalent(m.DocumentsRepo, server, sameVcsURI)
	}
	readString(&m.PriorityType, spec, "priorityType", full)
	readInt(&m.PerAgentWipLimit, spec, "perAgentWipLimit", full)
	readInt(&m.DefaultTaskLevel, spec, "defaultTaskLevel", full)
	readString(&m.DefaultInputResolution, spec, "defaultInputResolution", full)
	readString(&m.CoordinatorPrompt, spec, "coordinatorPrompt", full)
	if full || m.Sources != nil {
		server := []types.String{}
		for _, v := range list(spec["sources"]) {
			server = append(server, types.StringValue(str(v)))
		}
		// The server stores its own rendering of a source (github:Acme/X becomes github:acme/x);
		// the configured spelling stands when that is all that differs.
		if !sameSources(m.Sources, server) {
			m.Sources = server
		}
		if full && len(m.Sources) == 0 {
			m.Sources = nil
		}
	}
	if full || m.CoordinatorCaps != nil {
		// The export omits the key when there are none; a configured [] then reads back as [].
		caps := []types.String{}
		for _, v := range list(spec["coordinatorCapabilities"]) {
			caps = append(caps, types.StringValue(str(v)))
		}
		m.CoordinatorCaps = caps
		if full && len(caps) == 0 {
			m.CoordinatorCaps = nil
		}
	}
	if full || m.DocumentPaths != nil {
		paths, _ := spec["documentPaths"].(map[string]any)
		m.DocumentPaths = map[string]types.String{}
		for k, v := range paths {
			m.DocumentPaths[k] = types.StringValue(str(v))
		}
		if full && len(m.DocumentPaths) == 0 {
			m.DocumentPaths = nil
		}
	}
	st, _ := spec["settings"].(map[string]any)
	if m.Settings != nil || (full && anyNonNull(st)) {
		if m.Settings == nil {
			m.Settings = &boardSettingsModel{
				BudgetMicros: types.Int64Null(), SoftAlertPercent: types.Int64Null(), CycleCap: types.Int64Null(),
				NoProgressRepeatsToStop: types.Int64Null(), BlockingPriority: types.Int64Null(), CompletionPriority: types.Int64Null(),
				HumanQueueAgeMinutes: types.Int64Null(),
			}
		}
		readInt(&m.Settings.BudgetMicros, st, "budgetMicros", full)
		readInt(&m.Settings.SoftAlertPercent, st, "softAlertPercent", full)
		readInt(&m.Settings.CycleCap, st, "cycleCap", full)
		readInt(&m.Settings.NoProgressRepeatsToStop, st, "noProgressRepeatsToStop", full)
		readInt(&m.Settings.BlockingPriority, st, "blockingPriority", full)
		readInt(&m.Settings.CompletionPriority, st, "completionPriority", full)
		readInt(&m.Settings.HumanQueueAgeMinutes, st, "humanQueueAgeMinutes", full)
	}
	if !full && m.Roles == nil {
		return
	}
	exported := list(spec["roles"])
	byName := func(name string) map[string]any {
		for _, x := range exported {
			if e, ok := x.(map[string]any); ok && sameName(str(e["name"]), name) {
				return e
			}
		}
		return nil
	}
	isActive := func(e map[string]any) bool { a, ok := e["active"].(bool); return !ok || a }
	roles := []roleModel{}
	seen := map[string]bool{}
	if !full {
		for _, r := range m.Roles {
			e := byName(r.Name.ValueString())
			if e == nil || (!isActive(e) && r.Active.IsNull()) {
				continue // gone, or switched off outside Terraform: the plan declares it again
			}
			roleFromExport(&r, e, false)
			roles = append(roles, r)
			seen[str(e["name"])] = true
		}
	}
	for _, x := range exported {
		e, ok := x.(map[string]any)
		if !ok || seen[str(e["name"])] || !isActive(e) {
			continue
		}
		r := emptyRole()
		roleFromExport(&r, e, full)
		roles = append(roles, r)
	}
	m.Roles = roles
}

func emptyRole() roleModel {
	return roleModel{
		Name: types.StringNull(), Prompt: types.StringNull(), OrderIndex: types.Int64Null(), WipLimit: types.Int64Null(),
		RequireDistinctAgent: types.BoolNull(), Active: types.BoolNull(), Kind: types.StringNull(),
		Necessity: types.StringNull(), HumanGate: types.StringNull(), HopBudgetMicros: types.Int64Null(),
	}
}

func anyNonNull(m map[string]any) bool {
	for _, v := range m {
		if v != nil {
			return true
		}
	}
	return false
}

func (r *agentBoardResource) apply(ctx context.Context, m *agentBoardModel, d *diagAdder) bool {
	res, err := applySpec(ctx, r.client, m.toSpec(), false, sourceFor(r.source, m.Source))
	if err != nil {
		d.err("ReARM apply failed", err.Error())
		return false
	}
	return reportChanges(res, d, "board")
}

func (r *agentBoardResource) read(ctx context.Context, m *agentBoardModel, full bool, d *diagAdder) bool {
	f, err := catalog.ExportBoard(ctx, r.client, m.Name.ValueString())
	if err != nil {
		if catalog.IsNotFound(err) {
			return false
		}
		d.err("ReARM export failed", err.Error())
		return false
	}
	m.fromExport(f.Spec, full)
	return true
}

// reportChanges turns ERROR entries into diagnostics and warnings into Terraform warnings.
func reportChanges(res *catalog.Result, d *diagAdder, what string) bool {
	ok := true
	for _, ch := range res.Changes {
		for _, w := range ch.Warnings {
			d.warn("ReARM: "+ch.Name, w)
		}
		if ch.Action == rearm.DeclarativeActionError {
			d.err("ReARM rejected the "+what, fmt.Sprintf("%s: %s", ch.Name, ch.Message))
			ok = false
		}
	}
	return ok
}

func (r *agentBoardResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan agentBoardModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := &diagAdder{&resp.Diagnostics}
	if !r.apply(ctx, &plan, d) || !r.read(ctx, &plan, false, d) {
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.AddError("Board not found after create", plan.Name.ValueString())
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *agentBoardResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state agentBoardModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := &diagAdder{&resp.Diagnostics}
	// An import carries only the name; read everything so the first plan shows real differences.
	full := state.ID.IsNull() || state.ID.IsUnknown()
	if !r.read(ctx, &state, full, d) {
		if !resp.Diagnostics.HasError() {
			resp.State.RemoveResource(ctx)
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *agentBoardResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan agentBoardModel
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

// Delete archives the board (declarative-boards D7): its tasks, roles and history stay, and an
// archived board takes no new work. A board already gone is not an error.
func (r *agentBoardResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state agentBoardModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := catalog.ArchiveBoard(ctx, r.client, state.Name.ValueString()); err != nil && !catalog.IsNotFound(err) {
		resp.Diagnostics.AddError("ReARM archive failed", err.Error())
	}
}

func (r *agentBoardResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func sameSources(configured, server []types.String) bool {
	if configured == nil || len(configured) != len(server) {
		return false
	}
	for i := range configured {
		if !sameVcsURI(configured[i].ValueString(), server[i].ValueString()) {
			return false
		}
	}
	return true
}
