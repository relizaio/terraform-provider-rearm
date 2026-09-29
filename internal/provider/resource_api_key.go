package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	_ resource.Resource                = &apiKeyResource{}
	_ resource.ResourceWithConfigure   = &apiKeyResource{}
	_ resource.ResourceWithImportState = &apiKeyResource{}
	_ resource.ResourceWithModifyPlan  = &apiKeyResource{}
)

// apiKeyResource manages one of the organization's FREEFORM API keys by its declared name (task RD3-11), sent
// as an API_KEYS file holding that one key and not authoritative. It manages the key's identity and
// settings only and never holds a secret: secrets are minted by the rearm_api_key_secret ephemeral
// resource, or by a person with `rearm apikey mint`, and nothing of them reaches state but their
// slots' metadata.
type apiKeyResource struct {
	client *rearm.Client
	source *catalog.Source
}

type apiKeyModel struct {
	ID                types.String       `tfsdk:"id"`
	UUID              types.String       `tfsdk:"uuid"`
	Name              types.String       `tfsdk:"name"`
	Permissions       *apiKeyPermissions `tfsdk:"permissions"`
	Notes             types.String       `tfsdk:"notes"`
	Status            types.String       `tfsdk:"status"`
	SecretExpiresDays types.Int64        `tfsdk:"secret_expires_days"`
	SessionMaxMinutes types.Int64        `tfsdk:"session_max_minutes"`
	SecretSlots       types.List         `tfsdk:"secret_slots"`
	Source            *sourceModel       `tfsdk:"provenance"`
}

// apiKeyPermissions is a FREEFORM key's permissions: the organization-wide level, its functions and
// approval roles, and grants on single objects. Sets: ReARM keeps no order among them.
type apiKeyPermissions struct {
	Type      types.String             `tfsdk:"type"`
	Functions types.Set                `tfsdk:"functions"`
	Approvals types.Set                `tfsdk:"approvals"`
	Objects   []apiKeyObjectPermission `tfsdk:"objects"`
}

type apiKeyObjectPermission struct {
	Scope     types.String `tfsdk:"scope"`
	Object    types.String `tfsdk:"object"`
	Type      types.String `tfsdk:"type"`
	Functions types.Set    `tfsdk:"functions"`
	Approvals types.Set    `tfsdk:"approvals"`
}

// apiKeySecretSlot is a secret's metadata: never its value. The attribute holds them as a types.List so a plan
// can carry it unknown -- a new key's slots, and every update's, since a mint in the same run changes them
// (RD3-11 run-1 T-1: a Go slice cannot hold unknown, and every create failed).
type apiKeySecretSlot struct {
	Slot         types.Int64  `tfsdk:"slot"`
	Active       types.Bool   `tfsdk:"active"`
	CreatedDate  types.String `tfsdk:"created_date"`
	ExpiresDate  types.String `tfsdk:"expires_date"`
	LastUsedDate types.String `tfsdk:"last_used_date"`
}

func NewApiKeyResource() resource.Resource { return &apiKeyResource{} }

func (r *apiKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

// ModifyPlan warns when the only change is to the provenance block, which ReARM does not record.
func (r *apiKeyResource) ModifyPlan(_ context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	warnIfProvenanceOnly(req, resp)
}

func functionSetAttribute(desc string) schema.SetAttribute {
	return schema.SetAttribute{Optional: true, ElementType: types.StringType, Description: desc}
}

func (r *apiKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "One of the organization's FREEFORM API keys, by its declared name: its identity and settings, never a " +
			"secret. A key this resource creates has no secret; mint one with the rearm_api_key_secret ephemeral " +
			"resource (Terraform or OpenTofu 1.10 and up) or with `rearm apikey mint`, so no secret value is ever " +
			"written to state or plan files. Attributes left unset are not managed by Terraform. Destroying the " +
			"resource deactivates the key; declaring the name again takes it back. The provider's key must hold " +
			"CONFIGURATION_WRITE and may declare only keys no stronger than itself.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true,
				Description:   "The key id a client presents with a secret (REARM_APIKEYID); stable across rotation, not itself a secret.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"uuid": schema.StringAttribute{Computed: true, Description: "The key's uuid in ReARM.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{Required: true, PlanModifiers: replace,
				Description: "The key's declared name, unique among the organization's live keys. Import by it."},
			"permissions": schema.SingleNestedAttribute{Optional: true,
				Description: "The key's permissions, replaced whole when set.",
				Attributes: map[string]schema.Attribute{
					"type":      schema.StringAttribute{Optional: true, Description: "Organization-wide level: ESSENTIAL_READ, READ_ONLY, READ_WRITE or ADMIN."},
					"functions": functionSetAttribute("Organization-wide functions, e.g. BOARD_WRITE, CONFIGURATION_WRITE."),
					"approvals": functionSetAttribute("Organization-wide approval roles."),
					"objects": schema.ListNestedAttribute{Optional: true,
						Description: "Grants on single objects, by uuid.",
						NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
							"scope":     schema.StringAttribute{Required: true, Description: "COMPONENT, BRANCH, RELEASE, BOARD, PERSPECTIVE or INSTANCE."},
							"object":    schema.StringAttribute{Required: true, Description: "The object's uuid."},
							"type":      schema.StringAttribute{Required: true, Description: "ESSENTIAL_READ, READ_ONLY, READ_WRITE or ADMIN."},
							"functions": functionSetAttribute("Functions on the object."),
							"approvals": functionSetAttribute("Approval roles on the object."),
						}},
					},
				},
			},
			"notes":               schema.StringAttribute{Optional: true, Description: "Free text shown on the keys page."},
			"status":              schema.StringAttribute{Optional: true, Description: "ACTIVE or INACTIVE; INACTIVE refuses every secret of the key."},
			"secret_expires_days": schema.Int64Attribute{Optional: true, Description: "Days a secret minted for the key lives, 1 to 3650; unset mints secrets that do not expire."},
			"session_max_minutes": schema.Int64Attribute{Optional: true, Description: "Bounds device-login sessions approved on the key, in minutes."},
			"secret_slots": schema.ListNestedAttribute{Computed: true,
				Description: "The key's secrets: slot, state and dates -- never a value.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"slot":           schema.Int64Attribute{Computed: true},
					"active":         schema.BoolAttribute{Computed: true},
					"created_date":   schema.StringAttribute{Computed: true},
					"expires_date":   schema.StringAttribute{Computed: true},
					"last_used_date": schema.StringAttribute{Computed: true},
				}},
			},
			"provenance": resourceSourceAttribute(),
		},
	}
}

func (r *apiKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if pd := configured(req, resp); pd != nil {
		r.client, r.source = pd.client, pd.source
	}
}

func apiKeysFile(entry map[string]any) *catalog.ApiKeysFile {
	return &catalog.ApiKeysFile{Spec: map[string]any{
		"kind": string(rearm.DeclarativeKindApiKeys), "version": 1, "authoritative": false,
		"keys": []any{entry},
	}}
}

// toSpec is the key's file entry. A new key is sent ACTIVE when the configuration leaves status
// unset, so a key deactivated by a destroy comes back with the next create.
func (m *apiKeyModel) toSpec(creating bool) map[string]any {
	e := map[string]any{"name": m.Name.ValueString()}
	putString(e, "notes", m.Notes)
	putString(e, "status", m.Status)
	if creating && m.Status.IsNull() {
		e["status"] = "ACTIVE"
	}
	putInt(e, "secretExpiresDays", m.SecretExpiresDays)
	putInt(e, "sessionMaxMinutes", m.SessionMaxMinutes)
	if m.Permissions != nil {
		e["permissions"] = m.Permissions.toSpec()
	}
	return e
}

func (p *apiKeyPermissions) toSpec() map[string]any {
	out := map[string]any{}
	putString(out, "type", p.Type)
	putSet(out, "functions", p.Functions)
	putSet(out, "approvals", p.Approvals)
	if p.Objects != nil {
		objects := make([]any, 0, len(p.Objects))
		for _, o := range p.Objects {
			m := map[string]any{}
			putString(m, "scope", o.Scope)
			putString(m, "object", o.Object)
			putString(m, "type", o.Type)
			putSet(m, "functions", o.Functions)
			putSet(m, "approvals", o.Approvals)
			objects = append(objects, m)
		}
		out["objects"] = objects
	}
	return out
}

func putSet(m map[string]any, k string, v types.Set) {
	if v.IsNull() || v.IsUnknown() {
		return
	}
	var out []any
	for _, e := range v.Elements() {
		if s, ok := e.(types.String); ok {
			out = append(out, s.ValueString())
		}
	}
	if out == nil {
		out = []any{}
	}
	m[k] = out
}

func (r *apiKeyResource) apply(ctx context.Context, m *apiKeyModel, creating bool, d *diagAdder) bool {
	res, err := applySpec(ctx, r.client, apiKeysFile(m.toSpec(creating)), false, sourceFor(r.source, m.Source))
	if err != nil {
		d.err("ReARM apply failed", err.Error())
		return false
	}
	return reportChanges(res, d, "API key")
}

// readApiKey is the key declared under this name as the provider reads it, or nil when there is none.
var readApiKey = defaultReadApiKey

func defaultReadApiKey(ctx context.Context, c *rearm.Client, name string) (map[string]any, error) {
	resp, err := rearm.ApiKeys(ctx, c, []string{name})
	if err != nil {
		return nil, err
	}
	var keys []map[string]any
	js, err := json.Marshal(resp.ApiKeysProgrammatic)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(js, &keys); err != nil {
		return nil, err
	}
	for _, k := range keys {
		if str(k["name"]) == name {
			return k, nil
		}
	}
	return nil, nil
}

func (r *apiKeyResource) read(ctx context.Context, m *apiKeyModel, full bool, d *diagAdder) bool {
	k, err := readApiKey(ctx, r.client, m.Name.ValueString())
	if err != nil {
		d.err("ReARM read failed", err.Error())
		return false
	}
	if k == nil {
		return false
	}
	if !full && str(k["status"]) == "INACTIVE" && m.Status.IsNull() {
		return false // deactivated, as a destroy leaves it: the plan creates it again
	}
	m.fromView(k, full)
	return true
}

// fromView reads the key back: attributes the configuration sets (all of them on import), and the
// computed ones. A value ReARM holds in an equivalent spelling -- a blank note ReARM stores as none,
// object grants in another order -- keeps the configured spelling.
func (m *apiKeyModel) fromView(k map[string]any, full bool) {
	m.ID = strVal(k["keyId"])
	m.UUID = strVal(k["uuid"])
	m.Name = strVal(k["name"])
	if full || !m.Notes.IsNull() {
		m.Notes = blankAsNone(m.Notes, strVal(k["notes"]))
	}
	readString(&m.Status, k, "status", full)
	readInt(&m.SecretExpiresDays, k, "secretExpiresDays", full)
	readInt(&m.SessionMaxMinutes, k, "sessionMaxMinutes", full)
	if full || m.Permissions != nil {
		p, _ := k["permissions"].(map[string]any)
		m.Permissions = permissionsFromView(m.Permissions, p)
	}
	slots := []apiKeySecretSlot{}
	for _, s := range list(k["secretSlots"]) {
		sm, _ := s.(map[string]any)
		slots = append(slots, apiKeySecretSlot{Slot: intVal(sm["slot"]), Active: boolVal(sm["active"]),
			CreatedDate: strVal(sm["createdDate"]), ExpiresDate: strVal(sm["expiresDate"]), LastUsedDate: strVal(sm["lastUsedDate"])})
	}
	m.SecretSlots, _ = types.ListValueFrom(context.Background(), secretSlotType, slots)
}

// secretSlotType is one element of secret_slots.
var secretSlotType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"slot": types.Int64Type, "active": types.BoolType, "created_date": types.StringType,
	"expires_date": types.StringType, "last_used_date": types.StringType,
}}

// blankAsNone keeps a configured blank where ReARM stores none; otherwise ReARM's value.
func blankAsNone(configured, read types.String) types.String {
	if !configured.IsNull() && !configured.IsUnknown() && strings.TrimSpace(configured.ValueString()) == "" && read.IsNull() {
		return configured
	}
	return read
}

// permissionsFromView reads a FREEFORM key's permissions whole. Object grants follow the configured
// order where they match one, so a reordered read is not a change; ReARM's others come after.
func permissionsFromView(prior *apiKeyPermissions, p map[string]any) *apiKeyPermissions {
	out := &apiKeyPermissions{Type: types.StringNull(), Functions: types.SetNull(types.StringType),
		Approvals: types.SetNull(types.StringType)}
	if p != nil {
		out.Type = strVal(p["type"])
		out.Functions = stringSet(p["functions"])
		out.Approvals = stringSet(p["approvals"])
	}
	if prior != nil {
		out.Functions = emptyAsConfigured(prior.Functions, out.Functions)
		out.Approvals = emptyAsConfigured(prior.Approvals, out.Approvals)
	}
	var read []apiKeyObjectPermission
	if p != nil {
		for _, o := range list(p["objects"]) {
			om, _ := o.(map[string]any)
			read = append(read, apiKeyObjectPermission{Scope: strVal(om["scope"]), Object: strVal(om["object"]),
				Type: strVal(om["type"]), Functions: stringSet(om["functions"]), Approvals: stringSet(om["approvals"])})
		}
	}
	if prior != nil && prior.Objects != nil {
		used := make([]bool, len(read))
		for _, c := range prior.Objects {
			for i, o := range read {
				if !used[i] && sameName(c.Scope.ValueString(), o.Scope.ValueString()) && strings.EqualFold(c.Object.ValueString(), o.Object.ValueString()) {
					o.Scope, o.Object = c.Scope, c.Object
					o.Functions = emptyAsConfigured(c.Functions, o.Functions)
					o.Approvals = emptyAsConfigured(c.Approvals, o.Approvals)
					out.Objects = append(out.Objects, o)
					used[i] = true
					break
				}
			}
		}
		for i, o := range read {
			if !used[i] {
				out.Objects = append(out.Objects, o)
			}
		}
		if out.Objects == nil {
			out.Objects = []apiKeyObjectPermission{}
		}
		return out
	}
	out.Objects = read
	return out
}

func stringSet(v any) types.Set {
	l := list(v)
	if l == nil {
		return types.SetNull(types.StringType)
	}
	elems := make([]attr.Value, 0, len(l))
	for _, x := range l {
		elems = append(elems, types.StringValue(fmt.Sprint(x)))
	}
	s, _ := types.SetValue(types.StringType, elems)
	return s
}

// emptyAsConfigured keeps a configured empty set where ReARM writes none.
func emptyAsConfigured(configured, read types.Set) types.Set {
	if !configured.IsNull() && !configured.IsUnknown() && len(configured.Elements()) == 0 && read.IsNull() {
		return configured
	}
	return read
}

func (r *apiKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := &diagAdder{&resp.Diagnostics}
	if !r.apply(ctx, &plan, true, d) || !r.read(ctx, &plan, false, d) {
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.AddError("API key not found after create", plan.Name.ValueString())
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *apiKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state apiKeyModel
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

func (r *apiKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d := &diagAdder{&resp.Diagnostics}
	if !r.apply(ctx, &plan, false, d) || !r.read(ctx, &plan, false, d) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete deactivates the key: its row, name and secrets stay, and every secret is refused.
func (r *apiKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := archiveApiKey(ctx, r.client, state.Name.ValueString()); err != nil && !catalog.IsNotFound(err) {
		resp.Diagnostics.AddError("ReARM archive failed", err.Error())
	}
}

// archiveApiKey is behind a variable so tests can see the delete.
var archiveApiKey = defaultArchiveApiKey

func defaultArchiveApiKey(ctx context.Context, c *rearm.Client, name string) (*rearm.ArchiveApiKeyResponse, error) {
	return rearm.ArchiveApiKey(ctx, c, name)
}

func (r *apiKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
