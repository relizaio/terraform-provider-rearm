package provider

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rearm "github.com/relizaio/rearm-client-go"
	"github.com/relizaio/rearm-client-go/catalog"
)

// coordinator_checkpoint on a board's role (task SPLIT-10; T-1 to T-6 are the tests of the task's
// design, design/SPLIT-10/architecture-1.md section 4): sent and read exactly as blind_review, on the
// board's role block only, and the server's person-only refusal reported on the role it is about.

// The server's wording, AgentBoardService.ROUTING_GRAPH_OPERATOR_ONLY and checkpointChanges.
const operatorOnly = "Capabilities, role kind, necessity, human gate, required inputs, produced outputs, model strength, " +
	"blind review, coordinator checkpoint, commissions and the hop allowance are operator-only"

const freeformKeyID = "FREEFORM__11111111-2222-3333-4444-555555555555__ord__0"

// T-1: unset is neither sent nor adopted; true and false are sent; an import reads the server's value.
func TestCoordinatorCheckpointUnsetSetClearAndRead(t *testing.T) {
	r := namedRole("coder")
	if _, ok := roleToSpec(&r)["coordinatorCheckpoint"]; ok {
		t.Error("an unset coordinator_checkpoint is left out of the spec")
	}
	r.CoordinatorCheckpoint = types.BoolValue(true)
	if v, ok := roleToSpec(&r)["coordinatorCheckpoint"]; !ok || v != true {
		t.Errorf("a set coordinator_checkpoint is sent, got %v", v)
	}
	r.CoordinatorCheckpoint = types.BoolValue(false)
	if v, ok := roleToSpec(&r)["coordinatorCheckpoint"]; !ok || v != false {
		t.Errorf("false is sent as false, which clears the flag, got %v (%v)", v, ok)
	}

	for _, want := range []bool{true, false} {
		imported := emptyRole()
		roleFromExport(&imported, map[string]any{"name": "coder", "coordinatorCheckpoint": want}, true)
		if imported.CoordinatorCheckpoint.IsNull() || imported.CoordinatorCheckpoint.ValueBool() != want {
			t.Errorf("an import reads coordinatorCheckpoint %v, got %v", want, imported.CoordinatorCheckpoint)
		}
	}

	unmanaged := namedRole("coder")
	roleFromExport(&unmanaged, map[string]any{"name": "coder", "coordinatorCheckpoint": true}, false)
	if !unmanaged.CoordinatorCheckpoint.IsNull() {
		t.Error("a refresh does not adopt a flag set in the UI")
	}
	if _, ok := roleToSpec(&unmanaged)["coordinatorCheckpoint"]; ok {
		t.Error("so the next apply does not send it either")
	}

	managed := namedRole("coder")
	managed.CoordinatorCheckpoint = types.BoolValue(false)
	roleFromExport(&managed, map[string]any{"name": "coder", "coordinatorCheckpoint": true}, false)
	if !managed.CoordinatorCheckpoint.ValueBool() {
		t.Error("a declared coordinator_checkpoint follows the server")
	}
}

// T-2: the attribute is on the board's roles and not on the preset; a preset never sends or reads it.
func TestCoordinatorCheckpointIsABoardRoleAttributeOnly(t *testing.T) {
	ctx := context.Background()
	var board resource.SchemaResponse
	(&agentBoardResource{}).Schema(ctx, resource.SchemaRequest{}, &board)
	roles, ok := board.Schema.Attributes["roles"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf("roles is a list of nested attributes, got %T", board.Schema.Attributes["roles"])
	}
	a, ok := roles.NestedObject.Attributes["coordinator_checkpoint"].(schema.BoolAttribute)
	if !ok || !a.Optional || a.Required || a.Computed {
		t.Errorf("the board's role has an optional bool coordinator_checkpoint, got %#v", roles.NestedObject.Attributes["coordinator_checkpoint"])
	}

	var preset resource.SchemaResponse
	(&agentRolePresetResource{}).Schema(ctx, resource.SchemaRequest{}, &preset)
	if _, ok := preset.Schema.Attributes["coordinator_checkpoint"]; ok {
		t.Error("a preset has no coordinator_checkpoint: ReARM refuses it there (CHECKPOINT_NOT_ON_PRESET)")
	}
	if _, ok := roleAttributes()["coordinator_checkpoint"]; ok {
		t.Error("the shared role attributes stay free of coordinator_checkpoint")
	}

	p := presetModel{Name: types.StringValue("coder"), Prompt: types.StringValue("code"), BlindReview: types.BoolValue(true)}
	role := p.role()
	entry := presetFile(roleToSpec(&role)).Spec["presets"].([]any)[0].(map[string]any)
	if _, ok := entry["coordinatorCheckpoint"]; ok {
		t.Errorf("a preset never sends coordinatorCheckpoint: %v", entry)
	}
	for _, full := range []bool{true, false} {
		read := p.role()
		roleFromExport(&read, map[string]any{"name": "coder", "prompt": "code", "coordinatorCheckpoint": nil}, full)
		if !read.CoordinatorCheckpoint.IsNull() {
			t.Errorf("a preset's export leaves the field null (full=%v), got %v", full, read.CoordinatorCheckpoint)
		}
	}
}

// T-4: a flag changed in the UI is a plan diff when declared, and nothing when not.
func TestAFlagChangedInTheUIIsAPlanDiffWhenDeclared(t *testing.T) {
	e := exported()
	e["roles"] = []any{map[string]any{"name": "coder", "active": true, "coordinatorCheckpoint": false}}

	declared := boardModel()
	declared.Roles = []roleModel{namedRole("coder")}
	declared.Roles[0].CoordinatorCheckpoint = types.BoolValue(true)
	config := declared
	config.Roles = append([]roleModel(nil), declared.Roles...)
	declared.fromExport(e, false)
	if len(declared.Roles) != 1 || declared.Roles[0].CoordinatorCheckpoint.IsNull() || declared.Roles[0].CoordinatorCheckpoint.ValueBool() {
		t.Fatalf("the state reads the server's false, which Terraform shows as false -> true: %+v", declared.Roles)
	}
	sent := config.toSpec().Spec["roles"].([]any)[0].(map[string]any)
	if sent["coordinatorCheckpoint"] != true {
		t.Errorf("the configuration re-asserts true: %v", sent)
	}

	e["roles"] = []any{map[string]any{"name": "coder", "active": true, "coordinatorCheckpoint": true}}
	undeclared := boardModel()
	undeclared.Roles = []roleModel{namedRole("coder")}
	undeclared.fromExport(e, false)
	if len(undeclared.Roles) != 1 || !undeclared.Roles[0].CoordinatorCheckpoint.IsNull() {
		t.Fatalf("an undeclared attribute stays null when the UI set the flag: %+v", undeclared.Roles)
	}
	if _, ok := undeclared.toSpec().Spec["roles"].([]any)[0].(map[string]any)["coordinatorCheckpoint"]; ok {
		t.Error("and the next apply sends nothing, so the UI's flag is never cleared")
	}
}

// checkpointApply runs the board resource's apply against a stubbed server answer and returns the
// diagnostics it reports.
func checkpointApply(t *testing.T, keyID string, roles []roleModel, changes ...catalog.Change) (bool, diag.Diagnostics) {
	t.Helper()
	applySpec = func(_ context.Context, _ *rearm.Client, _ any, _ bool, _ *catalog.Source) (*catalog.Result, error) {
		return &catalog.Result{Changes: changes}, nil
	}
	t.Cleanup(func() { applySpec = catalog.Apply })
	m := boardModel()
	m.Roles = roles
	var diags diag.Diagnostics
	ok := (&agentBoardResource{keyKind: keyKindOf(keyID)}).apply(context.Background(), &m, &diagAdder{&diags})
	return ok, diags
}

func refused(name, message string) catalog.Change {
	return catalog.Change{Name: name, Action: rearm.DeclarativeActionError, Message: message}
}

func declaredRole(name string, flag *bool) roleModel {
	r := namedRole(name)
	if flag != nil {
		r.CoordinatorCheckpoint = types.BoolValue(*flag)
	}
	return r
}

func errorAt(t *testing.T, d diag.Diagnostic) path.Path {
	t.Helper()
	wp, ok := d.(diag.DiagnosticWithPath)
	if !ok {
		return path.Empty()
	}
	return wp.Path()
}

// T-3: the refusal names the role, the reason and the key's kind, never the rest of the key's id.
func TestCheckpointRefusalNamesTheRoleTheReasonAndTheKey(t *testing.T) {
	yes, no := true, false
	putOn := operatorOnly + " (only a person puts the board's coordinator checkpoint on coder)"
	takeOff := func(role, absent string) string {
		return operatorOnly + " (" + role + " carries the board's coordinator checkpoint" + absent + "; only a person changes which role carries it)"
	}
	const summary = "ReARM refused to change which role carries the coordinator checkpoint"

	t.Run("on the attribute of the role it names", func(t *testing.T) {
		ok, diags := checkpointApply(t, freeformKeyID, []roleModel{declaredRole("architect", nil), declaredRole("coder", &yes)},
			refused("coder", putOn))
		if ok || diags.ErrorsCount() != 1 {
			t.Fatalf("one refusal, the apply fails: ok=%v %v", ok, diags)
		}
		d := diags.Errors()[0]
		if want := path.Root("roles").AtListIndex(1).AtName("coordinator_checkpoint"); !errorAt(t, d).Equal(want) {
			t.Errorf("error at %v, want %v", errorAt(t, d), want)
		}
		if d.Summary() != summary {
			t.Errorf("summary %q", d.Summary())
		}
		for _, s := range []string{"coder: " + putOn, "personal", "USER__", "FREEFORM", "ReARM UI"} {
			if !strings.Contains(d.Detail(), s) {
				t.Errorf("the detail says %q: %s", s, d.Detail())
			}
		}
		for _, s := range []string{"11111111", "2222", "555555555555", "__ord__"} {
			if strings.Contains(d.Detail(), s) {
				t.Errorf("the detail carries nothing of the key id but its kind, found %q: %s", s, d.Detail())
			}
		}
	})

	t.Run("on the roles list when the flagged role is left out", func(t *testing.T) {
		_, diags := checkpointApply(t, freeformKeyID, []roleModel{declaredRole("coder", nil)},
			refused("architect", takeOff("architect", " and is absent from the file")))
		if diags.ErrorsCount() != 1 || !errorAt(t, diags.Errors()[0]).Equal(path.Root("roles")) {
			t.Fatalf("one error at roles: %v", diags)
		}
	})

	t.Run("on the role when its configuration does not set the attribute", func(t *testing.T) {
		// F-1: the flagged role deactivated rather than unflagged.
		architect := declaredRole("architect", nil)
		architect.Active = types.BoolValue(false)
		_, diags := checkpointApply(t, freeformKeyID, []roleModel{architect, declaredRole("coder", nil)},
			refused("architect", takeOff("architect", "")))
		if diags.ErrorsCount() != 1 || !errorAt(t, diags.Errors()[0]).Equal(path.Root("roles").AtListIndex(0)) {
			t.Fatalf("one error at roles[0], not at an attribute it does not set: %v", diags)
		}
	})

	t.Run("a personal key is told the refusal is unexpected", func(t *testing.T) {
		_, diags := checkpointApply(t, "USER__11111111-2222-3333-4444-555555555555__ord__0",
			[]roleModel{declaredRole("coder", &yes)}, refused("coder", putOn))
		if diags.ErrorsCount() != 1 {
			t.Fatalf("one error: %v", diags)
		}
		detail := diags.Errors()[0].Detail()
		for _, s := range []string{"personal (USER) key", "unexpected", "2026-11-02", "active member"} {
			if !strings.Contains(detail, s) {
				t.Errorf("the detail says %q: %s", s, detail)
			}
		}
		if strings.Contains(detail, "Apply this change with a personal key") || strings.Contains(detail, "11111111") {
			t.Errorf("no advice to use the key it already uses, nothing of its id: %s", detail)
		}
	})

	t.Run("another refusal keeps the board's wording", func(t *testing.T) {
		_, diags := checkpointApply(t, freeformKeyID, []roleModel{declaredRole("architect", &yes), declaredRole("coder", &yes)},
			refused("platform", "Board platform already has a coordinator checkpoint before architect; clear it first"))
		if diags.ErrorsCount() != 1 {
			t.Fatalf("one error: %v", diags)
		}
		d := diags.Errors()[0]
		if d.Summary() != "ReARM rejected the board" || !errorAt(t, d).Equal(path.Empty()) {
			t.Errorf("not a person question: %q at %v", d.Summary(), errorAt(t, d))
		}
	})

	t.Run("a refusal and another error are both reported", func(t *testing.T) {
		ok, diags := checkpointApply(t, freeformKeyID, []roleModel{declaredRole("coder", &yes)},
			refused("coder", putOn), refused("tester", "unknown capability X"))
		summaries := []string{}
		for _, d := range diags.Errors() {
			summaries = append(summaries, d.Summary())
		}
		if ok || len(summaries) != 2 || summaries[0] != summary || summaries[1] != "ReARM rejected the board" {
			t.Errorf("both errors, the refusal first: %v", summaries)
		}
	})

	t.Run("a flag moved between two declared roles is two errors", func(t *testing.T) {
		// F-2: checkpointChanges refuses on the role that had it and on the role that would get it.
		ok, diags := checkpointApply(t, freeformKeyID, []roleModel{declaredRole("architect", &no), declaredRole("coder", &yes)},
			refused("architect", takeOff("architect", "")), refused("coder", putOn))
		if ok || diags.ErrorsCount() != 2 {
			t.Fatalf("two errors: %v", diags)
		}
		for i, d := range diags.Errors() {
			want := path.Root("roles").AtListIndex(i).AtName("coordinator_checkpoint")
			if d.Summary() != summary || !errorAt(t, d).Equal(want) {
				t.Errorf("error %d: %q at %v, want %v", i, d.Summary(), errorAt(t, d), want)
			}
		}
	})

	t.Run("warnings on a refused role are still warnings", func(t *testing.T) {
		ch := refused("coder", putOn)
		ch.Warnings = []string{"2 tasks wait on coder"}
		_, diags := checkpointApply(t, freeformKeyID, []roleModel{declaredRole("coder", &yes)}, ch)
		if diags.WarningsCount() != 1 || diags.ErrorsCount() != 1 {
			t.Errorf("one warning, one error: %v", diags)
		}
	})
}

// T-5: the key's kind is the id's prefix and nothing after it.
func TestProviderKeyKindIsTheIdPrefixOnly(t *testing.T) {
	for id, want := range map[string]string{
		"USER__11111111-2222-3333-4444-555555555555": "USER",
		freeformKeyID: "FREEFORM",
		"FREEFORM__11111111-2222-3333-4444-555555555555":     "FREEFORM",
		"ORGANIZATION_RW__11111111-2222-3333-4444-555555555": "ORGANIZATION_RW",
		"no-separator": "no-separator",
		" USER__abc ":  "USER",
	} {
		got := string(keyKindOf(id))
		if got != want {
			t.Errorf("keyKindOf(%q) = %q, want %q", id, got, want)
		}
		if strings.Contains(got, "__") || strings.Contains(got, "1111") || strings.Contains(got, "abc") {
			t.Errorf("keyKindOf(%q) = %q keeps more than the prefix", id, got)
		}
	}
}

// T-6: the example shows the attribute on one role, commented out (it needs a personal key), and the
// attribute's description says the person-only rule, the UI alternative and the preset exclusion.
func TestTheCheckpointIsDocumented(t *testing.T) {
	b, err := os.ReadFile("../../examples/resources/rearm_agent_board/resource.tf")
	if err != nil {
		t.Fatal(err)
	}
	example := string(b)
	if n := strings.Count(example, "coordinator_checkpoint"); n != 1 {
		t.Errorf("the example sets coordinator_checkpoint on exactly one role, found %d", n)
	}
	if !strings.Contains(example, "# coordinator_checkpoint = true") || !strings.Contains(example, "personal key") {
		t.Error("the example's line is commented out and its comment names a personal key")
	}
	for _, s := range []string{"USER__", "UI", "preset", "plan diff", "Unset leaves"} {
		if !strings.Contains(coordinatorCheckpointDescription, s) {
			t.Errorf("the attribute description says %q", s)
		}
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"coordinator_checkpoint", "USER__", "own grants"} {
		if !strings.Contains(string(readme), s) {
			t.Errorf("the README says %q", s)
		}
	}
}
