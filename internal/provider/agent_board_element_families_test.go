package provider

import (
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// element_families (task RD4-6): a family name, or {family, definedIn} where defined_in is set, sent as the board
// file's elementFamilies and read back from the export in the configured order.

func family(prefix, name string) elementFamilyModel {
	return elementFamilyModel{Prefix: types.StringValue(prefix), Family: types.StringValue(name)}
}

func withList(f elementFamilyModel, types_ ...string) elementFamilyModel {
	f.DefinedIn = []types.String{}
	for _, t := range types_ {
		f.DefinedIn = append(f.DefinedIn, types.StringValue(t))
	}
	return f
}

func TestElementFamiliesAreSentAsTheBoardFileDeclaresThem(t *testing.T) {
	m := boardModel()
	if _, ok := m.toSpec().Spec["elementFamilies"]; ok {
		t.Error("unset is not managed, so nothing is sent")
	}
	m.ElementFamilies = []elementFamilyModel{family("SAF", "safety"), withList(family("T", "test"), "TEST_PLAN"),
		withList(family("RISK", "risk"))}
	got := m.toSpec().Spec["elementFamilies"]
	want := map[string]any{
		"SAF":  "safety",
		"T":    map[string]any{"family": "test", "definedIn": []any{"TEST_PLAN"}},
		"RISK": map[string]any{"family": "risk", "definedIn": []any{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sent %#v, want %#v", got, want)
	}
	m.ElementFamilies = []elementFamilyModel{}
	if got, ok := m.toSpec().Spec["elementFamilies"].(map[string]any); !ok || len(got) != 0 {
		t.Errorf("[] is sent as {}, which restores the defaults: %#v", got)
	}
}

func TestElementFamiliesReadBackAsConfigured(t *testing.T) {
	spec := exported()
	spec["elementFamilies"] = map[string]any{
		"T":    map[string]any{"family": "test", "definedIn": []any{"TEST_PLAN"}},
		"SAF":  "safety",
		"RISK": map[string]any{"family": "risk", "definedIn": []any{}},
		"ZED":  "extra",
	}
	m := boardModel()
	m.ID = types.StringValue("platform")
	configured := []elementFamilyModel{withList(family("RISK", "risk")), family("SAF", "safety"),
		withList(family("T", "test"), "TEST_PLAN")}
	m.ElementFamilies = configured
	m.fromExport(spec, false)
	if len(m.ElementFamilies) != 4 {
		t.Fatalf("read %v", m.ElementFamilies)
	}
	if !reflect.DeepEqual(m.ElementFamilies[:3], configured) {
		t.Errorf("the configured entries read back as written and in order: %#v", m.ElementFamilies[:3])
	}
	if m.ElementFamilies[1].DefinedIn != nil {
		t.Error("an entry without defined_in reads back without it")
	}
	if l := m.ElementFamilies[0].DefinedIn; l == nil || len(l) != 0 {
		t.Errorf("[] reads back as [], not unset: %#v", l)
	}
	if m.ElementFamilies[3].Prefix.ValueString() != "ZED" {
		t.Errorf("a family ReARM has and the configuration lacks follows, so the plan removes it: %v", m.ElementFamilies[3])
	}

	moved := exported()
	moved["elementFamilies"] = map[string]any{"T": map[string]any{"family": "test", "definedIn": []any{"BOARD_TEST_REPORT"}}}
	d := boardModel()
	d.ID = types.StringValue("platform")
	d.ElementFamilies = []elementFamilyModel{withList(family("T", "test"), "TEST_PLAN")}
	d.fromExport(moved, false)
	if d.ElementFamilies[0].DefinedIn[0].ValueString() != "BOARD_TEST_REPORT" {
		t.Errorf("a list changed outside Terraform reads back as ReARM has it, so the plan shows the drift: %v", d.ElementFamilies)
	}

	unmanaged := boardModel()
	unmanaged.ID = types.StringValue("platform")
	unmanaged.fromExport(spec, false)
	if unmanaged.ElementFamilies != nil {
		t.Error("unset stays unset whatever the board declares")
	}
	imported := agentBoardModel{Name: types.StringValue("platform"), ID: types.StringNull()}
	imported.fromExport(spec, true)
	if len(imported.ElementFamilies) != 4 || imported.ElementFamilies[0].Prefix.ValueString() != "RISK" {
		t.Errorf("import reads every family, in prefix order: %v", imported.ElementFamilies)
	}
	none := agentBoardModel{Name: types.StringValue("platform"), ID: types.StringNull()}
	none.fromExport(exported(), true)
	if none.ElementFamilies != nil {
		t.Error("import of a board that declares none leaves it unset")
	}
}
