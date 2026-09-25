package provider

import (
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// A board role and an organization's role preset are the same record, so the two resources share
// its model, its schema and its mapping to and from the spec the server takes.
//
// Ownership follows the declarative files (declarative-boards D5): an attribute the configuration
// sets is sent and read back; one it leaves unset is neither sent nor read, so a value tuned in the
// UI stays where it is. The spec is a map rather than a typed input because the server reads a
// field sent as null as "clear it", and only a map can leave a field out instead.

type roleModel struct {
	Name                 types.String          `tfsdk:"name"`
	Prompt               types.String          `tfsdk:"prompt"`
	OrderIndex           types.Int64           `tfsdk:"order_index"`
	WipLimit             types.Int64           `tfsdk:"wip_limit"`
	RequireDistinctAgent types.Bool            `tfsdk:"require_distinct_agent"`
	Active               types.Bool            `tfsdk:"active"`
	Kind                 types.String          `tfsdk:"kind"`
	Necessity            types.String          `tfsdk:"necessity"`
	HumanGate            types.String          `tfsdk:"human_gate"`
	RequiredCapabilities []types.String        `tfsdk:"required_capabilities"`
	HopBudgetMicros      types.Int64           `tfsdk:"hop_budget_micros"`
	BlindReview          types.Bool            `tfsdk:"blind_review"`
	RequiredInputs       []requiredInputModel  `tfsdk:"required_inputs"`
	ProducesOutputs      []producedOutputModel `tfsdk:"produces_outputs"`
	Strength             *strengthModel        `tfsdk:"strength"`
}

type requiredInputModel struct {
	Kind          types.String `tfsdk:"kind"`
	Specification types.String `tfsdk:"specification"`
	Scope         types.String `tfsdk:"scope"`
	Component     types.String `tfsdk:"component"`
	MinLifecycle  types.String `tfsdk:"min_lifecycle"`
	Resolution    types.String `tfsdk:"resolution"`
}

type producedOutputModel struct {
	Specification types.String `tfsdk:"specification"`
	Scope         types.String `tfsdk:"scope"`
	Required      types.Bool   `tfsdk:"required"`
}

type strengthModel struct {
	RequiredStrength types.Float64        `tfsdk:"required_strength"`
	StrengthHeadroom types.Float64        `tfsdk:"strength_headroom"`
	StrengthCategory types.String         `tfsdk:"strength_category"`
	ModelStrengths   []modelStrengthModel `tfsdk:"model_strengths"`
}

type modelStrengthModel struct {
	Model    types.String  `tfsdk:"model"`
	Strength types.Float64 `tfsdk:"strength"`
}

// roleAttributes are a role's attributes; name is added by the caller, which decides whether it is
// required or the resource's identity.
//
// Nested attributes rather than blocks throughout: an unset nested attribute is null and an empty
// one is [], while zero blocks and no blocks are the same empty list. Only the first keeps "not
// managed here" apart from "none" -- the difference between leaving a role's inputs alone and
// clearing them.
func roleAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"prompt":                 schema.StringAttribute{Optional: true, Description: "The role's prompt. Use file() to keep it beside the configuration."},
		"order_index":            schema.Int64Attribute{Optional: true, Description: "Order on the board; a new role without one takes ten times its position."},
		"wip_limit":              schema.Int64Attribute{Optional: true, Description: "Most tasks assigned in this role at once."},
		"require_distinct_agent": schema.BoolAttribute{Optional: true},
		"active":                 schema.BoolAttribute{Optional: true, Description: "Declaring a role makes it active unless this says false."},
		"kind":                   schema.StringAttribute{Optional: true, Description: "AGENTIC or HUMAN."},
		"necessity":              schema.StringAttribute{Optional: true, Description: "REQUIRED or OPTIONAL."},
		"human_gate":             schema.StringAttribute{Optional: true, Description: "NONE, ON_PASS or ON_ANY_SIGNOFF."},
		"required_capabilities":  schema.ListAttribute{Optional: true, ElementType: types.StringType},
		"hop_budget_micros":      schema.Int64Attribute{Optional: true, Description: "Allowance for one assignment of this role, in USD micros: the budget projection's estimate when the board has no history for the role, told to the worker at assignment, and flagged on the hop and the board when a hop exceeds it. Not enforced mid-hop."},
		"blind_review":           schema.BoolAttribute{Optional: true, Description: "A session assigned in this role reads its task without the earlier hops' notes, sessions and agents; documents and outcomes stay. Operator-only; default false."},
		"required_inputs": schema.ListNestedAttribute{
			Optional:    true,
			Description: "Inputs the role reads. Set, it replaces the role's list; [] clears it.",
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"kind":          schema.StringAttribute{Required: true, Description: "DOCUMENT or RELEASE."},
				"specification": schema.StringAttribute{Optional: true},
				"scope":         schema.StringAttribute{Optional: true, Description: "TASK, COMPONENT, ..."},
				"component":     schema.StringAttribute{Optional: true, Description: "Component name in the organization."},
				"min_lifecycle": schema.StringAttribute{Optional: true},
				"resolution":    schema.StringAttribute{Optional: true, Description: "LATEST_PASSING or STRICT_LATEST."},
			}},
		},
		"produces_outputs": schema.ListNestedAttribute{
			Optional:    true,
			Description: "Documents the role publishes. Set, it replaces the role's list; [] clears it.",
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"specification": schema.StringAttribute{Required: true},
				"scope":         schema.StringAttribute{Optional: true},
				"required":      schema.BoolAttribute{Optional: true},
			}},
		},
		"strength": schema.SingleNestedAttribute{
			Optional:    true,
			Description: "Model strength the role needs. Only the parts set here are managed.",
			Attributes: map[string]schema.Attribute{
				"required_strength": schema.Float64Attribute{Optional: true},
				"strength_headroom": schema.Float64Attribute{Optional: true},
				"strength_category": schema.StringAttribute{Optional: true, Description: "ARCHITECT, CODER, QA or REVIEWER."},
				"model_strengths": schema.ListNestedAttribute{
					Optional:    true,
					Description: "Per-model overrides. Set, it replaces the list.",
					NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
						"model":    schema.StringAttribute{Required: true, Description: "The model's canonical id, or name-version."},
						"strength": schema.Float64Attribute{Required: true},
					}},
				},
			},
		},
	}
}

// roleToSpec writes the attributes the configuration sets, and nothing else.
func roleToSpec(r *roleModel) map[string]any {
	m := map[string]any{"name": r.Name.ValueString()}
	putString(m, "prompt", r.Prompt)
	putInt(m, "orderIndex", r.OrderIndex)
	putInt(m, "wipLimit", r.WipLimit)
	putBool(m, "requireDistinctAgent", r.RequireDistinctAgent)
	putBool(m, "active", r.Active)
	putString(m, "kind", r.Kind)
	putString(m, "necessity", r.Necessity)
	putString(m, "humanGate", r.HumanGate)
	putInt(m, "hopBudgetMicros", r.HopBudgetMicros)
	putBool(m, "blindReview", r.BlindReview)
	if r.RequiredCapabilities != nil {
		caps := []any{}
		for _, c := range r.RequiredCapabilities {
			caps = append(caps, c.ValueString())
		}
		m["requiredCapabilities"] = caps
	}
	if r.RequiredInputs != nil {
		ins := []any{}
		for _, in := range r.RequiredInputs {
			e := map[string]any{"kind": in.Kind.ValueString()}
			putString(e, "specification", in.Specification)
			putString(e, "scope", in.Scope)
			putString(e, "component", in.Component)
			putString(e, "minLifecycle", in.MinLifecycle)
			putString(e, "resolution", in.Resolution)
			ins = append(ins, e)
		}
		m["requiredInputs"] = ins
	}
	if r.ProducesOutputs != nil {
		outs := []any{}
		for _, o := range r.ProducesOutputs {
			e := map[string]any{"specification": o.Specification.ValueString()}
			putString(e, "scope", o.Scope)
			putBool(e, "required", o.Required)
			outs = append(outs, e)
		}
		m["producesOutputs"] = outs
	}
	if r.Strength != nil {
		s := map[string]any{}
		putFloat(s, "requiredStrength", r.Strength.RequiredStrength)
		putFloat(s, "strengthHeadroom", r.Strength.StrengthHeadroom)
		putString(s, "strengthCategory", r.Strength.StrengthCategory)
		if r.Strength.ModelStrengths != nil {
			ms := []any{}
			for _, o := range r.Strength.ModelStrengths {
				ms = append(ms, map[string]any{"model": o.Model.ValueString(), "strength": o.Strength.ValueFloat64()})
			}
			s["modelStrengths"] = ms
		}
		m["strength"] = s
	}
	return m
}

// roleFromExport refreshes a role from the server's copy: every attribute when full (an import),
// otherwise only those the prior state manages.
func roleFromExport(r *roleModel, e map[string]any, full bool) {
	r.Name = types.StringValue(str(e["name"]))
	readString(&r.Prompt, e, "prompt", full)
	readInt(&r.OrderIndex, e, "orderIndex", full)
	readInt(&r.WipLimit, e, "wipLimit", full)
	readBool(&r.RequireDistinctAgent, e, "requireDistinctAgent", full)
	readBool(&r.Active, e, "active", false) // only ever what the configuration says
	readString(&r.Kind, e, "kind", full)
	readString(&r.Necessity, e, "necessity", full)
	readString(&r.HumanGate, e, "humanGate", full)
	readInt(&r.HopBudgetMicros, e, "hopBudgetMicros", full)
	readBool(&r.BlindReview, e, "blindReview", full)
	if full || r.RequiredCapabilities != nil {
		r.RequiredCapabilities = nil
		for _, c := range list(e["requiredCapabilities"]) {
			r.RequiredCapabilities = append(r.RequiredCapabilities, types.StringValue(str(c)))
		}
		if !full && r.RequiredCapabilities == nil {
			r.RequiredCapabilities = []types.String{}
		}
	}
	if full || r.RequiredInputs != nil {
		r.RequiredInputs = nil
		for _, x := range list(e["requiredInputs"]) {
			in, _ := x.(map[string]any)
			r.RequiredInputs = append(r.RequiredInputs, requiredInputModel{
				Kind: strVal(in["kind"]), Specification: strVal(in["specification"]), Scope: strVal(in["scope"]),
				Component: strVal(in["component"]), MinLifecycle: strVal(in["minLifecycle"]), Resolution: strVal(in["resolution"]),
			})
		}
		if !full && r.RequiredInputs == nil {
			r.RequiredInputs = []requiredInputModel{}
		}
	}
	if full || r.ProducesOutputs != nil {
		r.ProducesOutputs = nil
		for _, x := range list(e["producesOutputs"]) {
			o, _ := x.(map[string]any)
			r.ProducesOutputs = append(r.ProducesOutputs, producedOutputModel{
				Specification: strVal(o["specification"]), Scope: strVal(o["scope"]), Required: boolVal(o["required"]),
			})
		}
		if !full && r.ProducesOutputs == nil {
			r.ProducesOutputs = []producedOutputModel{}
		}
	}
	st, _ := e["strength"].(map[string]any)
	if r.Strength != nil || (full && hasStrength(st)) {
		if r.Strength == nil {
			r.Strength = &strengthModel{}
		}
		readFloat(&r.Strength.RequiredStrength, st, "requiredStrength", full)
		readFloat(&r.Strength.StrengthHeadroom, st, "strengthHeadroom", full)
		readString(&r.Strength.StrengthCategory, st, "strengthCategory", full)
		exported := list(st["modelStrengths"])
		if full || r.Strength.ModelStrengths != nil {
			// The configuration may name a model differently from the export (an alias against
			// its canonical id); keep its names where the list lines up, and read the strengths.
			if len(exported) == len(r.Strength.ModelStrengths) && !full {
				for i, x := range exported {
					o, _ := x.(map[string]any)
					r.Strength.ModelStrengths[i].Strength = floatVal(o["strength"])
				}
			} else {
				r.Strength.ModelStrengths = []modelStrengthModel{}
				for _, x := range exported {
					o, _ := x.(map[string]any)
					r.Strength.ModelStrengths = append(r.Strength.ModelStrengths,
						modelStrengthModel{Model: strVal(o["model"]), Strength: floatVal(o["strength"])})
				}
			}
		}
	}
}

func hasStrength(st map[string]any) bool {
	if st == nil {
		return false
	}
	if st["requiredStrength"] != nil || st["strengthCategory"] != nil || len(list(st["modelStrengths"])) > 0 {
		return true
	}
	h, _ := st["strengthHeadroom"].(float64)
	return h != 0
}

// ---------- map helpers ----------

func putString(m map[string]any, k string, v types.String) {
	if !v.IsNull() && !v.IsUnknown() {
		m[k] = v.ValueString()
	}
}

func putInt(m map[string]any, k string, v types.Int64) {
	if !v.IsNull() && !v.IsUnknown() {
		m[k] = v.ValueInt64()
	}
}

func putFloat(m map[string]any, k string, v types.Float64) {
	if !v.IsNull() && !v.IsUnknown() {
		m[k] = v.ValueFloat64()
	}
}

func putBool(m map[string]any, k string, v types.Bool) {
	if !v.IsNull() && !v.IsUnknown() {
		m[k] = v.ValueBool()
	}
}

func readString(dst *types.String, m map[string]any, k string, full bool) {
	if full || !dst.IsNull() {
		*dst = strVal(m[k])
	}
}

func readInt(dst *types.Int64, m map[string]any, k string, full bool) {
	if full || !dst.IsNull() {
		*dst = intVal(m[k])
	}
}

func readFloat(dst *types.Float64, m map[string]any, k string, full bool) {
	if full || !dst.IsNull() {
		*dst = floatVal(m[k])
	}
}

func readBool(dst *types.Bool, m map[string]any, k string, full bool) {
	if full || !dst.IsNull() {
		*dst = boolVal(m[k])
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func strVal(v any) types.String {
	s, ok := v.(string)
	if !ok || s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func intVal(v any) types.Int64 {
	switch n := v.(type) {
	case float64:
		return types.Int64Value(int64(n))
	case int64:
		return types.Int64Value(n)
	case int:
		return types.Int64Value(int64(n))
	}
	return types.Int64Null()
}

func floatVal(v any) types.Float64 {
	switch n := v.(type) {
	case float64:
		return types.Float64Value(n)
	case int64:
		return types.Float64Value(float64(n))
	}
	return types.Float64Null()
}

func boolVal(v any) types.Bool {
	b, ok := v.(bool)
	if !ok {
		return types.BoolNull()
	}
	return types.BoolValue(b)
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func sameName(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
