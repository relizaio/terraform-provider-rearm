package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	rearm "github.com/relizaio/rearm-client-go"
)

const (
	payments  = "6249a257-1111-4a5a-9c3e-000000000001"
	checkout  = "6249a257-2222-4a5a-9c3e-000000000002"
	elsewhere = "6249a257-3333-4a5a-9c3e-000000000003"
)

var held = []heldPerspective{{uuid: payments, name: "payments"}, {uuid: checkout, name: "checkout", product: true}}

func strs(vs ...string) []types.String {
	out := []types.String{}
	for _, v := range vs {
		out = append(out, types.StringValue(v))
	}
	return out
}

func values(ts []types.String) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.ValueString())
	}
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// T-4 (tests/b9115d09/run-2.md; architecture round 2 §3): an entry names a perspective by uuid in any
// case or by exact name, with the product: marker exactly when it is a product.
func TestAnEntryNamesAPerspectiveByUuidOrExactName(t *testing.T) {
	for entry, want := range map[string]bool{
		"payments": true, payments: true, " 6249A257-1111-4A5A-9C3E-000000000001 ": true,
		"Payments": false, "product:payments": false, elsewhere: false,
	} {
		if got := namesPerspective(entry, held[0]); got != want {
			t.Errorf("%q names payments: %v, want %v", entry, got, want)
		}
	}
	for entry, want := range map[string]bool{
		"product:checkout": true, "PRODUCT:" + checkout: true, "product: checkout": true, "checkout": false,
	} {
		if got := namesPerspective(entry, held[1]); got != want {
			t.Errorf("%q names the checkout product: %v, want %v", entry, got, want)
		}
	}
}

func TestTheConfiguredFormOfEachHeldPerspectiveIsKept(t *testing.T) {
	cases := []struct {
		name                           string
		configured, exported, expected []string
	}{
		{"a uuid the export writes as the unique name", []string{payments, "product:" + checkout},
			[]string{"payments", "product:checkout"}, []string{payments, "product:" + checkout}},
		{"a name the export writes as the uuid, shared now", []string{"payments", "product:checkout"},
			[]string{payments, "product:" + checkout}, []string{"payments", "product:checkout"}},
		{"a perspective the configuration does not name keeps the export's form", []string{"payments"},
			[]string{"payments", "product:" + checkout}, []string{"payments", "product:" + checkout}},
		{"one the board does not hold stays as exported", []string{"payments"},
			[]string{elsewhere}, []string{elsewhere}},
	}
	for _, c := range cases {
		got := values(keepConfigured(strs(c.configured...), strs(c.exported...), held))
		if !same(got, c.expected) {
			t.Errorf("%s: %v, want %v", c.name, got, c.expected)
		}
	}
}

// The read path end to end against a stand-in server: after an apply naming the perspective by uuid,
// and one naming a shared name, the state is the configuration, so the next plan is empty; an import
// takes the export's form.
func TestTheReadKeepsTheConfiguredFormAgainstTheServer(t *testing.T) {
	exportEntries := []string{"payments"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var data any
		switch req.OperationName {
		case "ExportBoard":
			data = map[string]any{"exportBoardProgrammatic": map[string]any{
				"name": "platform", "target": "platform-api", "perspectives": exportEntries}}
		case "ExportBoardPerspectives":
			data = map[string]any{"exportBoardPerspectivesProgrammatic": []any{
				map[string]any{"uuid": payments, "name": "payments", "product": false}}}
		default:
			http.Error(w, "unexpected operation "+req.OperationName, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()
	c, err := rearm.New(srv.URL, "id", "secret", rearm.WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	r := &agentBoardResource{client: c}

	read := func(configured []types.String, full bool) []string {
		m := boardModel()
		m.ID = types.StringValue("platform")
		m.Perspectives = configured
		ds := diag.Diagnostics{}
		if !r.read(context.Background(), &m, full, &diagAdder{&ds}) || ds.HasError() {
			t.Fatalf("the board was not read: %v", ds)
		}
		return values(m.Perspectives)
	}
	if got := read(strs(payments), false); !same(got, []string{payments}) {
		t.Errorf("a configured uuid, exported by name, reads back as %v", got)
	}
	exportEntries = []string{payments}
	if got := read(strs("payments"), false); !same(got, []string{"payments"}) {
		t.Errorf("a configured shared name, exported by uuid, reads back as %v", got)
	}
	if got := read(nil, true); !same(got, []string{payments}) {
		t.Errorf("an import reads the export's form, got %v", got)
	}
}
