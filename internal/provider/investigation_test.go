package provider

import (
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Investigation tasks (task RD4-12): a role's commissions and the board's investigation_overdue_minutes are sent
// only when configured, read back from the export, imported, and left as configured when the export does not carry
// them (a client from before them).

func commissioner() roleModel {
	r := namedRole("architect")
	r.Commissions = &commissionsModel{Roles: []types.String{types.StringValue("Tester")},
		Intake: types.StringValue("COORDINATOR"), DefaultBudgetMicros: types.Int64Null(), Review: types.StringNull()}
	return r
}

func TestCommissionsAreSentOnlyWhenConfigured(t *testing.T) {
	plain := namedRole("architect")
	if _, ok := roleToSpec(&plain)["commissions"]; ok {
		t.Error("unset commissions are left out of the spec")
	}
	r := commissioner()
	want := map[string]any{"roles": []any{"Tester"}, "intake": "COORDINATOR"}
	if got := roleToSpec(&r)["commissions"]; !reflect.DeepEqual(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
	none := namedRole("architect")
	none.Commissions = &commissionsModel{Roles: []types.String{}}
	if got := roleToSpec(&none)["commissions"]; !reflect.DeepEqual(got, map[string]any{"roles": []any{}}) {
		t.Errorf("roles = [] is sent, commissioning nobody: %v", got)
	}
}

func TestCommissionsReadBackImportedAndKeptWhenTheExportSaysNothing(t *testing.T) {
	server := map[string]any{"name": "architect", "commissions": map[string]any{"roles": []any{"tester"},
		"intake": "COORDINATOR", "defaultBudgetMicros": float64(3000000), "review": "lead"}}

	managed := commissioner()
	roleFromExport(&managed, server, false)
	if managed.Commissions.Roles[0].ValueString() != "Tester" {
		t.Errorf("the configured spelling of the same role is kept: %v", managed.Commissions.Roles)
	}
	if managed.Commissions.Intake.ValueString() != "COORDINATOR" {
		t.Errorf("a managed intake follows the server: %v", managed.Commissions.Intake)
	}
	if !managed.Commissions.DefaultBudgetMicros.IsNull() || !managed.Commissions.Review.IsNull() {
		t.Errorf("parts the configuration leaves unset stay unset: %+v", managed.Commissions)
	}

	imported := emptyRole()
	roleFromExport(&imported, server, true)
	if imported.Commissions == nil || imported.Commissions.DefaultBudgetMicros.ValueInt64() != 3000000 ||
		imported.Commissions.Review.ValueString() != "lead" || imported.Commissions.Roles[0].ValueString() != "tester" {
		t.Errorf("an import reads the whole block: %+v", imported.Commissions)
	}

	unmanaged := namedRole("architect")
	roleFromExport(&unmanaged, server, false)
	if unmanaged.Commissions != nil {
		t.Error("a refresh leaves unmanaged commissions alone")
	}

	old := commissioner()
	roleFromExport(&old, map[string]any{"name": "architect"}, false)
	if old.Commissions == nil || old.Commissions.Roles[0].ValueString() != "Tester" {
		t.Error("an export without the key (an older client) says nothing; the configuration stands")
	}

	drifted := commissioner()
	roleFromExport(&drifted, map[string]any{"name": "architect", "commissions": nil}, false)
	if drifted.Commissions == nil || len(drifted.Commissions.Roles) != 0 {
		t.Errorf("commissions removed on the server read back as none, so the plan shows it: %+v", drifted.Commissions)
	}

	empty := namedRole("architect")
	empty.Commissions = &commissionsModel{Roles: []types.String{}}
	roleFromExport(&empty, map[string]any{"name": "architect", "commissions": nil}, false)
	if empty.Commissions == nil || len(empty.Commissions.Roles) != 0 {
		t.Error("roles = [] and none on the server are the same, so the configuration stands")
	}
}

func TestAPresetCarriesCommissions(t *testing.T) {
	p := presetModel{Name: types.StringValue("architect"), Commissions: commissioner().Commissions}
	if p.role().Commissions != p.Commissions {
		t.Error("a preset's commissions reach its role model")
	}
	var back presetModel
	back.setRole(commissioner())
	if back.Commissions == nil || back.Commissions.Intake.ValueString() != "COORDINATOR" {
		t.Error("and come back from it")
	}
}

func TestInvestigationOverdueIsSentReadAndKeptWhenTheExportSaysNothing(t *testing.T) {
	m := boardModel()
	m.Settings = settingsWithStaleness(&boardStalenessModel{InvestigationOverdueMinutes: types.Int64Value(0)})
	st := m.toSpec().Spec["settings"].(map[string]any)["staleness"].(map[string]any)
	if st["investigationOverdueMinutes"] != int64(0) || len(st) != 1 {
		t.Fatalf("0 is a threshold and is sent: %v", st)
	}

	e := exported()
	e["settings"] = map[string]any{"staleness": map[string]any{"investigationOverdueMinutes": float64(30)}}
	m.fromExport(e, false)
	if m.Settings.Staleness.InvestigationOverdueMinutes.ValueInt64() != 30 {
		t.Errorf("read back from the export: %+v", m.Settings.Staleness)
	}

	kept := boardModel()
	kept.Settings = settingsWithStaleness(&boardStalenessModel{InvestigationOverdueMinutes: types.Int64Value(15)})
	ke := exported()
	ke["settings"] = map[string]any{"staleness": map[string]any{"roleUnstaffedMinutes": float64(60)}}
	kept.fromExport(ke, false)
	if kept.Settings.Staleness.InvestigationOverdueMinutes.ValueInt64() != 15 {
		t.Errorf("an export without the key keeps the configured threshold: %+v", kept.Settings.Staleness)
	}
	if kept.Settings.Staleness.RoleUnstaffedMinutes.ValueInt64() != 60 {
		t.Errorf("the rest of the block is still read whole: %+v", kept.Settings.Staleness)
	}
}
