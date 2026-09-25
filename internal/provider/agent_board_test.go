package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func boardModel() agentBoardModel {
	return agentBoardModel{
		ID: types.StringNull(), Name: types.StringValue("platform"), Description: types.StringNull(),
		Target: types.StringValue("platform-api"), DocumentsRepo: types.StringNull(), PriorityType: types.StringNull(),
		PerAgentWipLimit: types.Int64Null(), DefaultTaskLevel: types.Int64Null(),
		DefaultInputResolution: types.StringNull(), CoordinatorPrompt: types.StringNull(),
	}
}

func namedRole(name string) roleModel {
	r := emptyRole()
	r.Name = types.StringValue(name)
	return r
}

// The server's copy of the board, as ExportBoard returns it.
func exported() map[string]any {
	return map[string]any{
		"kind": "BOARD", "name": "platform", "target": "platform-api", "description": "set in the UI",
		"priorityType": "LAX", "perAgentWipLimit": float64(4),
		"settings": map[string]any{"cycleCap": float64(6), "budgetMicros": float64(1000)},
		"roles": []any{
			map[string]any{"name": "designer", "prompt": "design it", "orderIndex": float64(10), "active": true,
				"strength": map[string]any{"requiredStrength": 0.75, "strengthHeadroom": float64(0)}},
			map[string]any{"name": "reviewer", "prompt": "review it", "orderIndex": float64(20), "active": true},
			map[string]any{"name": "old", "prompt": "gone", "orderIndex": float64(30), "active": false},
		},
	}
}

func TestBoardSpecCarriesOnlyWhatIsConfigured(t *testing.T) {
	m := boardModel()
	m.Settings = &boardSettingsModel{CycleCap: types.Int64Value(4), BudgetMicros: types.Int64Null(),
		SoftAlertPercent: types.Int64Null(), NoProgressRepeatsToStop: types.Int64Null(),
		BlockingPriority: types.Int64Null(), CompletionPriority: types.Int64Null(), HumanQueueAgeMinutes: types.Int64Null()}
	s := m.toSpec().Spec
	if _, ok := s["description"]; ok {
		t.Error("an unset attribute is not sent")
	}
	if _, ok := s["roles"]; ok {
		t.Error("no roles attribute: the roles are not managed, so none are sent")
	}
	settings := s["settings"].(map[string]any)
	if len(settings) != 1 || settings["cycleCap"] != int64(4) {
		t.Errorf("only the configured setting: %v", settings)
	}
	m.Roles = []roleModel{}
	if roles, ok := m.toSpec().Spec["roles"].([]any); !ok || len(roles) != 0 {
		t.Error("roles = [] is sent as an empty list, which deactivates them all")
	}
}

func TestReadManagesOnlyWhatTheStateHolds(t *testing.T) {
	m := boardModel()
	m.ID = types.StringValue("platform")
	designer := namedRole("designer")
	designer.Prompt = types.StringValue("stale")
	m.Roles = []roleModel{designer}
	m.fromExport(exported(), false)

	if !m.Description.IsNull() || !m.PriorityType.IsNull() || m.Settings != nil {
		t.Error("attributes the configuration leaves unset stay unset")
	}
	if m.Roles[0].Prompt.ValueString() != "design it" {
		t.Errorf("a managed field is read back: %v", m.Roles[0].Prompt)
	}
	if !m.Roles[0].OrderIndex.IsNull() || m.Roles[0].Strength != nil {
		t.Error("a role's unmanaged fields stay unset")
	}
	if len(m.Roles) != 2 || m.Roles[1].Name.ValueString() != "reviewer" {
		t.Fatalf("an active role the configuration lacks appears so the plan removes it: %v", m.Roles)
	}
	for _, r := range m.Roles {
		if r.Name.ValueString() == "old" {
			t.Error("an inactive role the configuration lacks is not brought back")
		}
	}
}

func TestCoordinatorCapabilitiesUnsetEmptyAndSet(t *testing.T) {
	m := boardModel()
	if _, ok := m.toSpec().Spec["coordinatorCapabilities"]; ok {
		t.Error("unset is not managed, so nothing is sent")
	}
	m.CoordinatorCaps = []types.String{}
	if caps, ok := m.toSpec().Spec["coordinatorCapabilities"].([]any); !ok || len(caps) != 0 {
		t.Error("[] is sent as an empty list, which clears them")
	}
	m.CoordinatorCaps = []types.String{types.StringValue("PR_MERGE")}
	if caps := m.toSpec().Spec["coordinatorCapabilities"].([]any); len(caps) != 1 || caps[0] != "PR_MERGE" {
		t.Errorf("set is sent as given: %v", caps)
	}

	withCaps := exported()
	withCaps["coordinatorCapabilities"] = []any{"PR_MERGE"}

	unmanaged := boardModel()
	unmanaged.ID = types.StringValue("platform")
	unmanaged.fromExport(withCaps, false)
	if unmanaged.CoordinatorCaps != nil {
		t.Error("unset stays unset whatever the server holds")
	}

	none := boardModel()
	none.ID = types.StringValue("platform")
	none.CoordinatorCaps = []types.String{}
	none.fromExport(exported(), false)
	if none.CoordinatorCaps == nil || len(none.CoordinatorCaps) != 0 {
		t.Errorf("[] against a board with none reads back as [], not null: %v", none.CoordinatorCaps)
	}

	set := boardModel()
	set.ID = types.StringValue("platform")
	set.CoordinatorCaps = []types.String{types.StringValue("PR_MERGE")}
	set.fromExport(withCaps, false)
	if len(set.CoordinatorCaps) != 1 || set.CoordinatorCaps[0].ValueString() != "PR_MERGE" {
		t.Errorf("a managed set reads back: %v", set.CoordinatorCaps)
	}

	imported := agentBoardModel{Name: types.StringValue("platform"), ID: types.StringNull()}
	imported.fromExport(withCaps, true)
	if len(imported.CoordinatorCaps) != 1 {
		t.Errorf("import reads it: %v", imported.CoordinatorCaps)
	}
	importedNone := agentBoardModel{Name: types.StringValue("platform"), ID: types.StringNull()}
	importedNone.fromExport(exported(), true)
	if importedNone.CoordinatorCaps != nil {
		t.Error("import of a board with none leaves it unset")
	}
}

func TestReadDropsARoleSwitchedOffOutsideTerraform(t *testing.T) {
	m := boardModel()
	m.ID = types.StringValue("platform")
	m.Roles = []roleModel{namedRole("designer"), namedRole("old")}
	m.fromExport(exported(), false)
	for _, r := range m.Roles {
		if r.Name.ValueString() == "old" {
			t.Error("a role deactivated outside Terraform drops out, so the plan declares it again")
		}
	}
}

func TestImportReadsEverything(t *testing.T) {
	m := agentBoardModel{Name: types.StringValue("platform"), ID: types.StringNull()}
	m.fromExport(exported(), true)
	if m.Description.ValueString() != "set in the UI" || m.PerAgentWipLimit.ValueInt64() != 4 {
		t.Errorf("board attributes: %+v", m)
	}
	if m.Settings == nil || m.Settings.CycleCap.ValueInt64() != 6 || !m.Settings.BlockingPriority.IsNull() {
		t.Errorf("settings: %+v", m.Settings)
	}
	if len(m.Roles) != 2 {
		t.Fatalf("active roles only: %v", m.Roles)
	}
	if m.Roles[0].Strength == nil || m.Roles[0].Strength.RequiredStrength.ValueFloat64() != 0.75 {
		t.Errorf("strength: %+v", m.Roles[0].Strength)
	}
	if m.Roles[1].Strength != nil {
		t.Error("a role with no strength imports none")
	}
	if !m.Roles[0].Active.IsNull() {
		t.Error("active is only ever what the configuration says")
	}
}

func TestPresetFileIsOnePresetAndNotAuthoritative(t *testing.T) {
	p := presetModel{Name: types.StringValue("coder"), Prompt: types.StringValue("code"), Active: types.BoolNull(),
		OrderIndex: types.Int64Null(), WipLimit: types.Int64Null(), RequireDistinctAgent: types.BoolNull(),
		Kind: types.StringNull(), Necessity: types.StringNull(), HumanGate: types.StringNull(), HopBudgetMicros: types.Int64Null(),
		BlindReview: types.BoolNull()}
	role := p.role()
	f := presetFile(roleToSpec(&role))
	if f.Spec["authoritative"] != false {
		t.Error("a single preset must never deactivate the organization's others")
	}
	presets := f.Spec["presets"].([]any)
	entry := presets[0].(map[string]any)
	if len(presets) != 1 || entry["name"] != "coder" || entry["prompt"] != "code" || len(entry) != 2 {
		t.Errorf("one preset with what is configured: %v", entry)
	}
}

func TestReadKeepsTheConfiguredSpellingOfSourcesAndRepository(t *testing.T) {
	m := boardModel()
	m.ID = types.StringValue("platform")
	m.Sources = []types.String{types.StringValue("github:Acme/Platform")}
	m.DocumentsRepo = types.StringValue("https://github.com/Acme/Platform-Docs.git")
	e := exported()
	e["sources"] = []any{"github:acme/platform"}
	e["documentsRepo"] = "github.com/acme/platform-docs"
	m.fromExport(e, false)
	if m.Sources[0].ValueString() != "github:Acme/Platform" {
		t.Errorf("sources: %v", m.Sources)
	}
	if m.DocumentsRepo.ValueString() != "https://github.com/Acme/Platform-Docs.git" {
		t.Errorf("documents repo: %v", m.DocumentsRepo)
	}
	e["sources"] = []any{"github:acme/other"}
	m.fromExport(e, false)
	if m.Sources[0].ValueString() != "github:acme/other" {
		t.Errorf("a real change shows: %v", m.Sources)
	}
}

// human_queue_age_minutes (task 82880ea6): sent when set, left out when not, read on import.
func TestHumanQueueAgeMinutesSetAndRead(t *testing.T) {
	m := boardModel()
	m.Settings = &boardSettingsModel{BudgetMicros: types.Int64Null(), SoftAlertPercent: types.Int64Null(),
		CycleCap: types.Int64Null(), NoProgressRepeatsToStop: types.Int64Null(), BlockingPriority: types.Int64Null(),
		CompletionPriority: types.Int64Null(), HumanQueueAgeMinutes: types.Int64Value(30)}
	settings := m.toSpec().Spec["settings"].(map[string]any)
	if len(settings) != 1 || settings["humanQueueAgeMinutes"] != int64(30) {
		t.Errorf("only the configured setting: %v", settings)
	}
	imported := boardModel()
	e := exported()
	e["settings"] = map[string]any{"humanQueueAgeMinutes": float64(45)}
	imported.fromExport(e, true)
	if imported.Settings == nil || imported.Settings.HumanQueueAgeMinutes.ValueInt64() != 45 {
		t.Errorf("an import reads the threshold: %+v", imported.Settings)
	}
}

// blind_review (task 0192a587): left out when unset, so a board file never switches it off by
// omission; sent when set; imported from the export; refreshed only when the state manages it.
func TestBlindReviewUnsetSetAndRead(t *testing.T) {
	r := emptyRole()
	r.Name = types.StringValue("reviewer")
	if _, ok := roleToSpec(&r)["blindReview"]; ok {
		t.Error("an unset blind_review is left out of the spec")
	}
	r.BlindReview = types.BoolValue(true)
	if roleToSpec(&r)["blindReview"] != true {
		t.Error("a set blind_review is sent")
	}

	imported := emptyRole()
	roleFromExport(&imported, map[string]any{"name": "reviewer", "blindReview": true}, true)
	if !imported.BlindReview.ValueBool() {
		t.Error("an import reads blindReview")
	}
	managed := emptyRole()
	roleFromExport(&managed, map[string]any{"name": "reviewer", "blindReview": true}, false)
	if !managed.BlindReview.IsNull() {
		t.Error("a refresh leaves an unmanaged blind_review alone")
	}
	managed.BlindReview = types.BoolValue(false)
	roleFromExport(&managed, map[string]any{"name": "reviewer", "blindReview": true}, false)
	if !managed.BlindReview.ValueBool() {
		t.Error("a managed blind_review follows the server")
	}
}
