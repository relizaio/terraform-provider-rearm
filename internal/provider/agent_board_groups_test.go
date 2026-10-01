package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	rearm "github.com/relizaio/rearm-client-go"
)

func keyedGroup(key string) groupModel {
	return groupModel{Key: types.StringValue(key), Name: types.StringNull(), Description: types.StringNull(),
		DefaultWorkLevel: types.Int64Null(), Status: types.StringNull()}
}

func groupKeysOf(gs []groupModel) []string {
	out := []string{}
	for _, g := range gs {
		out = append(out, g.Key.ValueString())
	}
	return out
}

// exportOf is what the server writes back for the groups a board file sent: keys lower-cased, in
// the file's order, then the groups the file left out.
func exportOf(sent []any, more ...map[string]any) []any {
	out := []any{}
	for _, x := range sent {
		e := map[string]any{}
		for k, v := range x.(map[string]any) {
			e[k] = v
		}
		e["key"] = groupKey(str(e["key"]))
		if deps := list(e["dependsOn"]); deps != nil {
			lower := []any{}
			for _, d := range deps {
				lower = append(lower, groupKey(str(d)))
			}
			e["dependsOn"] = lower
			if len(lower) == 0 {
				delete(e, "dependsOn")
			}
		}
		if _, ok := e["status"]; !ok {
			e["status"] = "OPEN"
		}
		out = append(out, e)
	}
	for _, m := range more {
		out = append(out, m)
	}
	return out
}

// Unset is not managed and sends nothing; [] clears; a group sends its key and the members it sets.
func TestGroupsAreSentOnlyWhenSetAndWithTheMembersSet(t *testing.T) {
	m := boardModel()
	if _, ok := m.toSpec().Spec["groups"]; ok {
		t.Fatal("unset groups are sent")
	}
	m.Groups = []groupModel{}
	if got, _ := m.toSpec().Spec["groups"].([]any); got == nil || len(got) != 0 {
		t.Fatalf("[] is sent as %v, want an empty list", m.toSpec().Spec["groups"])
	}
	core := keyedGroup("core-work")
	core.Name = types.StringValue("The core")
	core.DependsOn = strs()
	ui := keyedGroup("ui-work")
	ui.DependsOn = strs("core-work")
	ui.DefaultWorkLevel = types.Int64Value(2)
	m.Groups = []groupModel{core, ui}
	want := []any{
		map[string]any{"key": "core-work", "name": "The core", "dependsOn": []any{}},
		map[string]any{"key": "ui-work", "dependsOn": []any{"core-work"}, "defaultWorkLevel": int64(2)},
	}
	if got := m.toSpec().Spec["groups"]; !reflect.DeepEqual(got, want) {
		t.Errorf("groups sent as %#v, want %#v", got, want)
	}
}

// TestGroupsRoundTrip: what the configuration sends reads back as the configuration, in its order, so
// the plan after an apply is empty. A group ReARM has open that the configuration does not list shows
// (the plan closes it); one it has closed does not (dropping it closed it).
func TestGroupsRoundTrip(t *testing.T) {
	core := keyedGroup("Core-Work")
	core.Name = types.StringValue("The core")
	ui := keyedGroup("ui-work")
	ui.DependsOn = strs("CORE-WORK")
	ui.Status = types.StringValue("OPEN")
	configured := []groupModel{ui, core}
	m := boardModel()
	m.Groups = configured
	exported := exportOf(m.toSpec().Spec["groups"].([]any),
		map[string]any{"key": "old-work", "status": "CLOSED"},
		map[string]any{"key": "extra-work", "name": "Made in the UI", "status": "OPEN", "defaultWorkLevel": float64(1)})
	// Members the configuration leaves unset read back unset whatever ReARM holds.
	exported[1].(map[string]any)["description"] = "set in the UI"

	m.fromExport(map[string]any{"name": "platform", "groups": exported}, false)
	if got := groupKeysOf(m.Groups); !same(got, []string{"ui-work", "Core-Work", "extra-work"}) {
		t.Fatalf("read back %v", got)
	}
	if !reflect.DeepEqual(m.Groups[:2], configured) {
		t.Errorf("the configured groups read back as %#v, want %#v", m.Groups[:2], configured)
	}
	extra := m.Groups[2]
	if extra.Name.ValueString() != "Made in the UI" || extra.DefaultWorkLevel.ValueInt64() != 1 || extra.Status.ValueString() != "OPEN" {
		t.Errorf("an unlisted open group reads every member: %#v", extra)
	}

	// A real difference shows: a dependency changed outside Terraform.
	m.Groups = configured
	exported[0].(map[string]any)["dependsOn"] = []any{"extra-work"}
	m.fromExport(map[string]any{"name": "platform", "groups": exported}, false)
	if got := values(m.Groups[0].DependsOn); !same(got, []string{"extra-work"}) {
		t.Errorf("a changed dependency reads back as %v", got)
	}

	// Unset: not read at all. An import reads every group, closed ones included.
	m.Groups = nil
	m.fromExport(map[string]any{"name": "platform", "groups": exported}, false)
	if m.Groups != nil {
		t.Errorf("unset groups were read: %v", groupKeysOf(m.Groups))
	}
	m.fromExport(map[string]any{"name": "platform", "groups": exported}, true)
	if got := groupKeysOf(m.Groups); !same(got, []string{"ui-work", "core-work", "old-work", "extra-work"}) {
		t.Errorf("an import reads %v", got)
	}
	if m.Groups[1].Description.ValueString() != "set in the UI" || m.Groups[1].DependsOn != nil {
		t.Errorf("an import reads every member: %#v", m.Groups[1])
	}

	// A configured [] with none on the board reads back as [].
	m.Groups = []groupModel{}
	m.fromExport(map[string]any{"name": "platform"}, false)
	if m.Groups == nil || len(m.Groups) != 0 {
		t.Errorf("[] reads back as %v", m.Groups)
	}
}

func TestADroppedGroupIsPlannedAsAClose(t *testing.T) {
	state := []groupModel{keyedGroup("core-work"), keyedGroup("ui-work"), keyedGroup("docs-work")}
	if s, _ := groupPlanWarning(state, state); s != "" {
		t.Errorf("an unchanged list warns %q", s)
	}
	s, d := groupPlanWarning(state, []groupModel{keyedGroup("Core-Work")})
	if s != "Groups closed" || d != "The apply closes docs-work, ui-work: a group the configuration drops is closed, "+
		"not deleted. Its tasks stay in it, and it takes no new ones." {
		t.Errorf("dropping two groups warns %q: %q", s, d)
	}
	if s, d := groupPlanWarning(state, []groupModel{}); s != "Groups deleted" ||
		d != "groups = [] deletes every group of the board (core-work, docs-work, ui-work); ReARM refuses it while a group holds tasks." {
		t.Errorf("[] warns %q: %q", s, d)
	}
}

// The read end to end against a stand-in server: the groups come through the client's export, in
// order, and the configured form stands.
func TestTheReadReturnsTheGroupsFromTheServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.OperationName != "ExportBoard" {
			http.Error(w, "unexpected operation "+req.OperationName, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"exportBoardProgrammatic": map[string]any{
			"name": "platform", "target": "platform-api", "groups": []any{
				map[string]any{"key": "ui-work", "dependsOn": []any{"core-work"}, "status": "OPEN", "defaultWorkLevel": 2},
				map[string]any{"key": "core-work", "name": "The core", "status": "OPEN"},
				map[string]any{"key": "old-work", "status": "CLOSED"},
			}}}})
	}))
	defer srv.Close()
	c, err := rearm.New(srv.URL, "id", "secret", rearm.WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	r := &agentBoardResource{client: c}
	m := boardModel()
	m.ID = types.StringValue("platform")
	core := keyedGroup("core-work")
	core.Name = types.StringValue("The core")
	ui := keyedGroup("ui-work")
	ui.DependsOn = strs("core-work")
	ui.DefaultWorkLevel = types.Int64Value(2)
	m.Groups = []groupModel{ui, core}
	ds := diag.Diagnostics{}
	if !r.read(context.Background(), &m, false, &diagAdder{&ds}) || ds.HasError() {
		t.Fatalf("the board was not read: %v", ds)
	}
	if !reflect.DeepEqual(m.Groups, []groupModel{ui, core}) {
		t.Errorf("read back %#v", m.Groups)
	}
}
