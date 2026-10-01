package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	rearm "github.com/relizaio/rearm-client-go"
)

// Task fceb1e57 (board documents 5/5, §3.3): task_prefix, the documents block and documents_root.

// fakeBoardServer answers ExportBoard with the board it holds and records each applied board file,
// applying taskPrefix and documents the way ReARM does (normalised, members left out kept).
type fakeBoardServer struct {
	board   map[string]any
	applied []map[string]any
}

func (f *fakeBoardServer) start(t *testing.T) *agentBoardResource {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var data any
		switch req.OperationName {
		case "ExportBoard":
			data = map[string]any{"exportBoardProgrammatic": f.board}
		default:
			http.Error(w, "unexpected operation "+req.OperationName, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	c, err := rearm.New(srv.URL, "id", "secret", rearm.WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	return &agentBoardResource{client: c}
}

// apply is ReARM's apply of the two fields from a board file (AgentBoardService applyBoardFields).
func (f *fakeBoardServer) apply(spec map[string]any) {
	f.applied = append(f.applied, spec)
	if p, ok := spec["taskPrefix"].(string); ok {
		f.board["taskPrefix"] = strings.ToUpper(strings.TrimSpace(p))
	}
	if d, ok := spec["documents"].(map[string]any); ok {
		held, _ := f.board["documents"].(map[string]any)
		next := map[string]any{"prefix": nil, "shared": nil, "root": nil}
		for k := range next {
			if held != nil {
				next[k] = held[k]
			}
			if v, sent := d[k]; sent {
				if s, isStr := v.(string); isStr && k == "root" {
					v = strings.TrimLeft(strings.TrimSpace(s), "/")
				}
				next[k] = v
			}
		}
		f.board["documents"] = next
	}
}

func (f *fakeBoardServer) read(t *testing.T, r *agentBoardResource, m *agentBoardModel, full bool) {
	ds := diag.Diagnostics{}
	if !r.read(context.Background(), m, full, &diagAdder{&ds}) || ds.HasError() {
		t.Fatalf("the board was not read: %v", ds)
	}
}

func TestTaskPrefixUnsetIsReadBackAndSetIsSent(t *testing.T) {
	f := &fakeBoardServer{board: map[string]any{"name": "platform", "target": "platform-api", "taskPrefix": "PL"}}
	r := f.start(t)

	// Unset: nothing sent (the plan holds an unknown on create), the derived prefix read back.
	m := boardModel()
	m.TaskPrefix = types.StringUnknown()
	if _, ok := m.toSpec().Spec["taskPrefix"]; ok {
		t.Error("an unset prefix is not sent")
	}
	f.read(t, r, &m, false)
	if m.TaskPrefix.ValueString() != "PL" {
		t.Errorf("the server's derived prefix fills the state: %v", m.TaskPrefix)
	}
	// A second plan is empty: the next read gives the state it started from.
	again := m
	f.read(t, r, &again, false)
	if !again.TaskPrefix.Equal(m.TaskPrefix) || !again.DocumentsRoot.Equal(m.DocumentsRoot) {
		t.Errorf("a second read changed the state: %v %v", again.TaskPrefix, again.DocumentsRoot)
	}

	// Set: sent as written; ReARM upper-cases it and the configured spelling stands.
	m.TaskPrefix = types.StringValue("pf")
	spec := m.toSpec().Spec
	if spec["taskPrefix"] != "pf" {
		t.Errorf("a set prefix is sent: %v", spec["taskPrefix"])
	}
	f.apply(spec)
	f.read(t, r, &m, false)
	if m.TaskPrefix.ValueString() != "pf" {
		t.Errorf("the configured spelling of the same prefix stands: %v", m.TaskPrefix)
	}

	// Renamed elsewhere: the read shows it, so the plan puts it back.
	f.board["taskPrefix"] = "QQ"
	f.read(t, r, &m, false)
	if m.TaskPrefix.ValueString() != "QQ" {
		t.Errorf("a rename outside Terraform is read: %v", m.TaskPrefix)
	}
}

func TestATaskPrefixChangeIsAnUpdateInPlace(t *testing.T) {
	resp := &resource.SchemaResponse{}
	(&agentBoardResource{}).Schema(context.Background(), resource.SchemaRequest{}, resp)
	a := resp.Schema.Attributes["task_prefix"]
	if !a.IsOptional() || !a.IsComputed() {
		t.Fatal("task_prefix is Optional and Computed")
	}
	for _, pm := range a.(schema.StringAttribute).PlanModifiers {
		if pm.Description(context.Background()) == stringplanmodifier.RequiresReplace().Description(context.Background()) {
			t.Error("a prefix change replaces the board; it must be a rename in place")
		}
	}
	if r := resp.Schema.Attributes["documents_root"]; !r.IsComputed() || r.IsOptional() {
		t.Error("documents_root is computed only")
	}
}

func TestTheDocumentsBlockRoundTrips(t *testing.T) {
	f := &fakeBoardServer{board: map[string]any{"name": "ReARM Dogfood", "target": "platform-api", "taskPrefix": "RD"}}
	r := f.start(t)
	m := boardModel()
	m.Name = types.StringValue("ReARM Dogfood")

	// A shared repository with no root: root is not sent, and the default ReARM reports is no difference.
	m.Documents = &boardDocumentsModel{Prefix: types.StringValue("dogfood"), Shared: types.BoolValue(true), Root: types.StringNull()}
	spec := m.toSpec().Spec
	docs := spec["documents"].(map[string]any)
	if _, sent := docs["root"]; sent || docs["prefix"] != "dogfood" || docs["shared"] != true {
		t.Errorf("only the set members are sent: %v", docs)
	}
	f.apply(spec)
	f.read(t, r, &m, false)
	if m.Documents.Prefix.ValueString() != "dogfood" || !m.Documents.Shared.ValueBool() || !m.Documents.Root.IsNull() {
		t.Errorf("the block reads back as configured: %+v", m.Documents)
	}
	if m.DocumentsRoot.ValueString() != "boards/rearm-dogfood/" {
		t.Errorf("documents_root is the resolved default: %v", m.DocumentsRoot)
	}

	// An explicit empty root is sent as "" and is the repository root, not the shared default.
	m.Documents.Root = types.StringValue("")
	spec = m.toSpec().Spec
	if root, sent := spec["documents"].(map[string]any)["root"]; !sent || root != "" {
		t.Errorf("an empty root is sent as \"\": %v %v", root, sent)
	}
	f.apply(spec)
	f.read(t, r, &m, false)
	if m.Documents.Root.IsNull() || m.Documents.Root.ValueString() != "" || m.DocumentsRoot.ValueString() != "" {
		t.Errorf("an empty root reads back as \"\" and resolves to the repository root: %v %v", m.Documents.Root, m.DocumentsRoot)
	}

	// A root written with a leading slash keeps its spelling; ReARM stores it without.
	m.Documents.Root = types.StringValue("/team/{board}")
	f.apply(m.toSpec().Spec)
	f.read(t, r, &m, false)
	if m.Documents.Root.ValueString() != "/team/{board}" || m.DocumentsRoot.ValueString() != "team/rearm-dogfood/" {
		t.Errorf("the configured spelling stands: %v %v", m.Documents.Root, m.DocumentsRoot)
	}

	// A member left unset stays unset though the server has one: the root set above.
	partial := boardModel()
	partial.Name = types.StringValue("ReARM Dogfood")
	partial.Documents = &boardDocumentsModel{Prefix: types.StringValue("dogfood"), Shared: types.BoolNull(), Root: types.StringNull()}
	f.read(t, r, &partial, false)
	if !partial.Documents.Root.IsNull() || !partial.Documents.Shared.IsNull() {
		t.Errorf("unset members stay unset: %+v", partial.Documents)
	}

	// Unmanaged, the block stays unset whatever the server holds; an import reads all of it.
	unmanaged := boardModel()
	unmanaged.Name = types.StringValue("ReARM Dogfood")
	f.read(t, r, &unmanaged, false)
	if unmanaged.Documents != nil || unmanaged.DocumentsRoot.ValueString() != "team/rearm-dogfood/" {
		t.Errorf("an unmanaged block stays unset, documents_root still read: %+v %v", unmanaged.Documents, unmanaged.DocumentsRoot)
	}
	imported := boardModel()
	f.read(t, r, &imported, true)
	if imported.Documents == nil || imported.Documents.Root.ValueString() != "team/{board}" ||
		imported.Documents.Prefix.ValueString() != "dogfood" || imported.TaskPrefix.ValueString() != "RD" {
		t.Errorf("an import reads the whole block and the prefix: %+v %v", imported.Documents, imported.TaskPrefix)
	}
}

func TestTheResolvedRootFollowsReARMsRule(t *testing.T) {
	for _, c := range []struct {
		docs map[string]any
		want string
	}{
		{nil, ""},
		{map[string]any{"shared": true}, "boards/caf-cr-me-2/"},
		{map[string]any{"shared": true, "root": ""}, ""},
		{map[string]any{"shared": false, "root": "/mine"}, "mine/"},
		{map[string]any{"root": "team/{board}/"}, "team/caf-cr-me-2/"},
	} {
		if got := resolvedDocumentsRoot("Café Crème 2", c.docs); got != c.want {
			t.Errorf("%v: got %q, want %q", c.docs, got, c.want)
		}
	}
	// The tester's board (tests/fceb1e57/run-1.md T-4): the server's documentsRoot, byte for byte.
	if got := resolvedDocumentsRoot("Tf Ünïcödé Straße Øre mujm01q1", map[string]any{"shared": true}); got != "boards/tf-n-c-d-stra-e-re-mujm01q1/" {
		t.Errorf("an accented name: got %q", got)
	}
	for name, want := range map[string]string{"ReARM Dogfood": "rearm-dogfood", "  --Payments!! ": "payments",
		"a__//b": "a-b", "日本": "", "İstanbul": "i-stanbul", "KELVIN \u212A": "kelvin-k"} {
		if got := boardSlug(name); got != want {
			t.Errorf("boardSlug(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestAComponentReadsTheDocumentKind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"exportCatalogProgrammatic": map[string]any{
			"kind": "CATALOG", "version": 1, "components": []any{
				map[string]any{"name": "dogfood-architecture", "type": "COMPONENT", "kind": "BOARD_DOCUMENT"}}}}})
	}))
	defer srv.Close()
	c, err := rearm.New(srv.URL, "id", "secret", rearm.WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	m := componentModel{Name: types.StringValue("dogfood-architecture")}
	ds := diag.Diagnostics{}
	if !(&componentResource{client: c}).read(context.Background(), &m, &diagAdder{&ds}) || ds.HasError() {
		t.Fatalf("the component was not read: %v", ds)
	}
	if m.Kind.ValueString() != "BOARD_DOCUMENT" {
		t.Errorf("kind reads as BOARD_DOCUMENT: %v", m.Kind)
	}
}

func TestTheBoardExampleSetsBothFields(t *testing.T) {
	b, err := os.ReadFile("../../examples/resources/rearm_agent_board/resource.tf")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"task_prefix", "documents = {", "shared", "root"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the example does not set %s", want)
		}
	}
}
