package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
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
	Perspectives           []types.String          `tfsdk:"perspectives"`
	TaskPrefix             types.String            `tfsdk:"task_prefix"`
	Documents              *boardDocumentsModel    `tfsdk:"documents"`
	DocumentsRoot          types.String            `tfsdk:"documents_root"`
	Settings               *boardSettingsModel     `tfsdk:"settings"`
	Groups                 []groupModel            `tfsdk:"groups"`
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
	Staleness               *boardStalenessModel `tfsdk:"staleness"`
}

// boardStalenessModel is when the board ALERTs that work went stale (task RD3-4), minutes each, null
// off. Set, it is the whole block: a threshold left out is off on the board.
type boardStalenessModel struct {
	RoleUnstaffedMinutes types.Int64 `tfsdk:"role_unstaffed_minutes"`
	HopNoProgressMinutes types.Int64 `tfsdk:"hop_no_progress_minutes"`
	DeliveryStuckMinutes types.Int64 `tfsdk:"delivery_stuck_minutes"`
	SeatSilentMinutes    types.Int64 `tfsdk:"seat_silent_minutes"`
	RepeatMinutes        types.Int64 `tfsdk:"repeat_minutes"`
}

func (s *boardStalenessModel) toSpec() map[string]any {
	out := map[string]any{}
	putInt(out, "roleUnstaffedMinutes", s.RoleUnstaffedMinutes)
	putInt(out, "hopNoProgressMinutes", s.HopNoProgressMinutes)
	putInt(out, "deliveryStuckMinutes", s.DeliveryStuckMinutes)
	putInt(out, "seatSilentMinutes", s.SeatSilentMinutes)
	putInt(out, "repeatMinutes", s.RepeatMinutes)
	return out
}

// stalenessFromExport reads the block whole, because the board replaces it whole: a threshold the
// configuration leaves out and ReARM has set is drift, not something to keep.
func stalenessFromExport(m map[string]any) *boardStalenessModel {
	return &boardStalenessModel{
		RoleUnstaffedMinutes: intVal(m["roleUnstaffedMinutes"]),
		HopNoProgressMinutes: intVal(m["hopNoProgressMinutes"]),
		DeliveryStuckMinutes: intVal(m["deliveryStuckMinutes"]),
		SeatSilentMinutes:    intVal(m["seatSilentMinutes"]),
		RepeatMinutes:        intVal(m["repeatMinutes"]),
	}
}

// boardDocumentsModel is how the board names and places its documents (board-documents.md D2, D6):
// each member set here is managed, each left unset keeps what ReARM has.
type boardDocumentsModel struct {
	Prefix types.String `tfsdk:"prefix"`
	Shared types.Bool   `tfsdk:"shared"`
	Root   types.String `tfsdk:"root"`
}

func NewAgentBoardResource() resource.Resource { return &agentBoardResource{} }

var _ resource.ResourceWithModifyPlan = &agentBoardResource{}

// ModifyPlan warns when the only change is to the provenance block, which ReARM does not record,
// and when a path template still says {task}.
func (r *agentBoardResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	warnIfProvenanceOnly(req, resp)
	var paths map[string]types.String
	if req.Config.Raw.IsNull() {
		return
	}
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("document_paths"), &paths)...)
	if w := taskPlaceholderWarning(paths); w != "" {
		resp.Diagnostics.AddAttributeWarning(path.Root("document_paths"), "{task} is read as {key}", w)
	}
	keepDocumentsRoot(ctx, req, resp)
	warnGroupChanges(ctx, req, resp)
}

// keepDocumentsRoot plans the resolved root as it stands when neither the documents block nor the
// name changes, so an unrelated change does not show it as known after apply.
func keepDocumentsRoot(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var planDocs, stateDocs *boardDocumentsModel
	var planName, stateName, root types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("documents"), &planDocs)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("documents"), &stateDocs)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("name"), &planName)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("name"), &stateName)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("documents_root"), &root)...)
	if resp.Diagnostics.HasError() || !planName.Equal(stateName) || !sameDocuments(planDocs, stateDocs) {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("documents_root"), root)...)
}

func sameDocuments(a, b *boardDocumentsModel) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Prefix.Equal(b.Prefix) && a.Shared.Equal(b.Shared) && a.Root.Equal(b.Root)
}

// keyTemplate is a path template as the server stores it: {task} was dropped for {key}, the task's key.
func keyTemplate(t string) string { return strings.ReplaceAll(t, "{task}", "{key}") }

// taskPlaceholderWarning names the templates that still say {task}, or is empty.
func taskPlaceholderWarning(paths map[string]types.String) string {
	var specs []string
	for k, v := range paths {
		if !v.IsNull() && !v.IsUnknown() && strings.Contains(v.ValueString(), "{task}") {
			specs = append(specs, k)
		}
	}
	if len(specs) == 0 {
		return ""
	}
	sort.Strings(specs)
	return "document_paths " + strings.Join(specs, ", ") + " use {task}, which ReARM reads as {key} (the task's key, " +
		"e.g. RD-42). Nothing changes on the board; write {key} to silence this."
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
				Description: "Path templates per specification type, e.g. ARCHITECTURE = \"docs/design/{key}/architecture-{round}.md\". " +
					"Placeholders: {key} (the task's key, e.g. RD-42), {round}, {type}, {component}. {task} is read as {key}: " +
					"it plans without a difference, and a warning asks for {key}."},
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
			"perspectives": schema.ListAttribute{Optional: true, ElementType: types.StringType,
				Description: "Perspectives the board hangs off, by name or uuid; product:<name or uuid> marks a PRODUCT " +
					"component used as a perspective. A name several perspectives share needs the uuid, unless the " +
					"board already holds one of them. Each entry stays as written while it names what the board holds. " +
					"The target must be a member of each, and adding or removing one needs BOARD_WRITE and " +
					"CONFIGURATION_WRITE covering it. Unset is not managed; [] is none."},
			"task_prefix": schema.StringAttribute{Optional: true, Computed: true,
				Description: "The task-key prefix, 2 to 8 letters and digits (RD makes keys RD-1, RD-2...). Unset, ReARM " +
					"derives one from the name and it is read back. A change is a rename, applied in place: existing " +
					"keys stay and still resolve, and a prefix once used is never reused in the organization, so " +
					"one another board holds is refused.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"documents": schema.SingleNestedAttribute{
				Optional: true,
				Description: "How the board names and places its documents. Only the members set here are managed; " +
					"one left unset keeps what ReARM has.",
				Attributes: map[string]schema.Attribute{
					"prefix": schema.StringAttribute{Optional: true,
						Description: "Replaces the board name in its document components' names, <prefix>-<specification>."},
					"shared": schema.BoolAttribute{Optional: true,
						Description: "The documents repository serves several boards: the default root becomes boards/{board}/."},
					"root": schema.StringAttribute{Optional: true,
						Description: "The root outright, relative to the repository, with no '..'; \"\" is the repository " +
							"root, which is not the same as leaving it unset on a shared repository."},
				},
			},
			"documents_root": schema.StringAttribute{Computed: true,
				Description: "Where the board's documents sit in the documents repository, as ReARM resolves it from " +
					"the documents block and the name; empty is the repository root."},
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
					"staleness": schema.SingleNestedAttribute{
						Optional: true,
						Description: "When the board ALERTs that work went stale, minutes each (at least 1); a threshold left out is off. " +
							"Only an ALERT is posted: no task changes state. Set, this is the whole block.",
						Attributes: map[string]schema.Attribute{
							"role_unstaffed_minutes":  schema.Int64Attribute{Optional: true, Description: "A task queued for a role this long while no session of the role polled the board."},
							"hop_no_progress_minutes": schema.Int64Attribute{Optional: true, Description: "A task assigned this long with nothing from its holder: no document, sign-off, question or usage report."},
							"delivery_stuck_minutes":  schema.Int64Attribute{Optional: true, Description: "A task delivering this long with a linked PR not delivered."},
							"seat_silent_minutes":     schema.Int64Attribute{Optional: true, Description: "A task waiting on the coordinator this long while the seat is held."},
							"repeat_minutes":          schema.Int64Attribute{Optional: true, Description: "How long a standing breach stays quiet before it is alerted again; unset is 240."},
						},
					},
				},
			},
			"groups": groupsAttribute(),
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
	putString(s, "taskPrefix", m.TaskPrefix)
	if m.Documents != nil {
		d := map[string]any{}
		putString(d, "prefix", m.Documents.Prefix)
		if !m.Documents.Shared.IsNull() && !m.Documents.Shared.IsUnknown() {
			d["shared"] = m.Documents.Shared.ValueBool()
		}
		putString(d, "root", m.Documents.Root) // "" is sent: it is the repository root, not unset
		s["documents"] = d
	}
	if m.Sources != nil {
		src := []any{}
		for _, v := range m.Sources {
			src = append(src, v.ValueString())
		}
		s["sources"] = src
	}
	if m.Perspectives != nil {
		ps := []any{}
		for _, v := range m.Perspectives {
			ps = append(ps, v.ValueString())
		}
		s["perspectives"] = ps
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
			p[k] = keyTemplate(v.ValueString())
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
		if m.Settings.Staleness != nil {
			st["staleness"] = m.Settings.Staleness.toSpec()
		}
		s["settings"] = st
	}
	if m.Groups != nil {
		s["groups"] = groupsToSpec(m.Groups)
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
	if full || m.Perspectives != nil {
		// The export omits the key when there are none; a configured [] then reads back as [].
		ps := []types.String{}
		for _, v := range list(spec["perspectives"]) {
			ps = append(ps, types.StringValue(str(v)))
		}
		m.Perspectives = ps
		if full && len(ps) == 0 {
			m.Perspectives = nil
		}
	}
	if full || m.DocumentPaths != nil {
		paths, _ := spec["documentPaths"].(map[string]any)
		configured := m.DocumentPaths
		m.DocumentPaths = map[string]types.String{}
		for k, v := range paths {
			// The server stores {task} as {key}; a configuration still written with {task} keeps
			// its spelling when that is all that differs, so the state after an apply is the plan.
			server := str(v)
			m.DocumentPaths[k] = keepEquivalent(configured[k], &server, func(a, b string) bool { return keyTemplate(a) == b })
		}
		if full && len(m.DocumentPaths) == 0 {
			m.DocumentPaths = nil
		}
	}
	// Always read: unset, the server derived it; set, the configured spelling stands for the same prefix.
	var prefix *string
	if v := str(spec["taskPrefix"]); v != "" {
		prefix = &v
	}
	m.TaskPrefix = keepEquivalent(m.TaskPrefix, prefix, func(a, b string) bool {
		return strings.EqualFold(strings.TrimSpace(a), b)
	})
	m.readDocuments(spec, full)
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
		stale, _ := st["staleness"].(map[string]any)
		if m.Settings.Staleness != nil || (full && anyNonNull(stale)) {
			m.Settings.Staleness = stalenessFromExport(stale)
		}
	}
	if full || m.Groups != nil {
		m.Groups = groupsFromExport(m.Groups, list(spec["groups"]), full)
		if full && len(m.Groups) == 0 {
			m.Groups = nil
		}
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

// readDocuments reads the documents block: every member on an import, otherwise the members the
// configuration sets, each keeping its configured spelling when ReARM stored the same value. A member
// left unset stays unset whatever the server holds, so a default root reported back is no difference.
// documents_root is always read.
func (m *agentBoardModel) readDocuments(spec map[string]any, full bool) {
	docs, _ := spec["documents"].(map[string]any)
	m.DocumentsRoot = types.StringValue(resolvedDocumentsRoot(str(spec["name"]), docs))
	if full {
		m.Documents = nil
		if anyNonNull(docs) {
			m.Documents = &boardDocumentsModel{Prefix: strOrNullAny(docs["prefix"]), Shared: boolOrNullAny(docs["shared"]),
				Root: strOrNullAny(docs["root"])}
		}
		return
	}
	if m.Documents == nil {
		return
	}
	d := m.Documents
	if !d.Prefix.IsNull() {
		d.Prefix = keepSame(d.Prefix, strOrNullAny(docs["prefix"]), func(a string) string { return strings.TrimSpace(a) })
	}
	if !d.Shared.IsNull() {
		d.Shared = boolOrNullAny(docs["shared"])
	}
	if !d.Root.IsNull() {
		d.Root = keepSame(d.Root, strOrNullAny(docs["root"]), documentsRootAsStored)
	}
}

// keepSame is the server's value, or the configured one when ReARM stores it as that value.
func keepSame(configured, server types.String, stored func(string) string) types.String {
	if !server.IsNull() && !configured.IsNull() && !configured.IsUnknown() &&
		stored(configured.ValueString()) == server.ValueString() {
		return configured
	}
	return server
}

// documentsRootAsStored is a root as ReARM stores it: trimmed, leading slashes dropped.
func documentsRootAsStored(root string) string { return strings.TrimLeft(strings.TrimSpace(root), "/") }

// resolvedDocumentsRoot is ReARM's rule for a board's documents root (AgentBoardData.documentsRoot):
// the root set, else boards/{board}/ on a shared repository, else the repository root; {board} is
// the board name slugged as boardSlug does, and a root that is not empty ends in a slash.
func resolvedDocumentsRoot(board string, docs map[string]any) string {
	root := ""
	if r, ok := docs["root"].(string); ok {
		root = r
	} else if shared, _ := docs["shared"].(bool); shared {
		root = "boards/{board}/"
	}
	root = strings.TrimLeft(strings.ReplaceAll(root, "{board}", boardSlug(board)), "/")
	if root != "" && !strings.HasSuffix(root, "/") {
		root += "/"
	}
	return root
}

// boardSlug is a board name as ReARM slugs it for its documents (AgentBoardData.slug): lower case,
// then each run of anything but a-z and 0-9 one hyphen, none at either end. An accented letter is a
// separator, not a plain letter: "Café" is "caf" (tests/fceb1e57/run-1.md T-4).
//
// Lower case is Java's toLowerCase(Locale.ROOT). Go's per-rune ToLower agrees except for U+0130
// (capital I with dot), which Java lowers to "i" plus a combining dot, so a hyphen follows the i.
func boardSlug(name string) string {
	lower := strings.ToLower(strings.ReplaceAll(name, "\u0130", "i\u0307"))
	return strings.Trim(nonSlug.ReplaceAllString(lower, "-"), "-")
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func strOrNullAny(v any) types.String {
	if s, ok := v.(string); ok {
		return types.StringValue(s)
	}
	return types.StringNull()
}

func boolOrNullAny(v any) types.Bool {
	if b, ok := v.(bool); ok {
		return types.BoolValue(b)
	}
	return types.BoolNull()
}

func emptyRole() roleModel {
	return roleModel{
		Name: types.StringNull(), Prompt: types.StringNull(), OrderIndex: types.Int64Null(), WipLimit: types.Int64Null(),
		RequireDistinctAgent: types.BoolNull(), Active: types.BoolNull(), Kind: types.StringNull(),
		Necessity: types.StringNull(), HumanGate: types.StringNull(), HopBudgetMicros: types.Int64Null(),
		BlindReview: types.BoolNull(),
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
	configured := m.Perspectives
	m.fromExport(f.Spec, full)
	if len(configured) > 0 && len(m.Perspectives) > 0 {
		// The export writes a shared name as its uuid and a unique one as its name, whichever the
		// configuration used; keep the configured form of each perspective the board holds, so the
		// state after an apply is the plan. An older server without the read keeps the export's form.
		if held, err := heldPerspectives(ctx, r.client, m.Name.ValueString()); err == nil {
			m.Perspectives = keepConfigured(configured, m.Perspectives, held)
		}
	}
	return true
}

// heldPerspective is a perspective a board holds, as the server reports it.
type heldPerspective struct {
	uuid, name string
	product    bool
}

func heldPerspectives(ctx context.Context, c *rearm.Client, board string) ([]heldPerspective, error) {
	resp, err := rearm.ExportBoardPerspectives(ctx, c, board)
	if err != nil {
		return nil, err
	}
	out := []heldPerspective{}
	for _, p := range resp.ExportBoardPerspectivesProgrammatic {
		out = append(out, heldPerspective{uuid: p.Uuid, name: p.Name, product: p.Product})
	}
	return out, nil
}

const productMarker = "product:"

// namesPerspective reports whether a board file entry names p the way the server resolves it: its
// uuid in any case, or its exact name, with the product: marker exactly when p is a product.
func namesPerspective(entry string, p heldPerspective) bool {
	ref := strings.TrimSpace(entry)
	product := len(ref) >= len(productMarker) && strings.EqualFold(ref[:len(productMarker)], productMarker)
	if product {
		ref = strings.TrimSpace(ref[len(productMarker):])
	}
	return product == p.product && (strings.EqualFold(ref, p.uuid) || ref == p.name)
}

// keepConfigured gives each exported entry the configured entry that names the same perspective, so
// a uuid the export writes as a name, or a name it writes as a uuid, reads back as configured. An
// entry the configuration does not name keeps the export's form, so a real difference still shows.
func keepConfigured(configured, exported []types.String, held []heldPerspective) []types.String {
	out := make([]types.String, len(exported))
	for i, e := range exported {
		out[i] = e
		for _, p := range held {
			if !namesPerspective(e.ValueString(), p) {
				continue
			}
			for _, c := range configured {
				if !c.IsNull() && !c.IsUnknown() && namesPerspective(c.ValueString(), p) {
					out[i] = c
					break
				}
			}
			break
		}
	}
	return out
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
