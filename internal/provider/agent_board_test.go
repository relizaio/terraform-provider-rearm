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
		BlockingPriority: types.Int64Null(), CompletionPriority: types.Int64Null()}
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
		Kind: types.StringNull(), Necessity: types.StringNull(), HumanGate: types.StringNull(), HopBudgetMicros: types.Int64Null()}
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
