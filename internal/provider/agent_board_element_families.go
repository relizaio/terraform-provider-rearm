package provider

import (
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// elementFamilyModel is one of a board's element families (gaps §2.A; grammar 1.2, task RD4-6): the id prefix,
// the family it names, and the specification types that define its ids, in order of precedence. defined_in unset
// takes the family's default; [] means the prefix's ids are only ever referenced.
type elementFamilyModel struct {
	Prefix    types.String   `tfsdk:"prefix"`
	Family    types.String   `tfsdk:"family"`
	DefinedIn []types.String `tfsdk:"defined_in"`
}

func elementFamiliesAttribute() schema.Attribute {
	return schema.SetNestedAttribute{
		Optional: true,
		Description: "Element id families over ReARM's defaults (REQ requirement, T and TEST test, Q question, F review-item, ...). " +
			"Each entry names a prefix and its family, and optionally defined_in: the specification types whose documents " +
			"define the prefix's ids, in order of precedence (a document of an earlier type owns an id; elsewhere a heading " +
			"with the id is a reference). Unset defined_in takes the family's default; [] means references only. A set: " +
			"order does not matter, each prefix once. Unset is " +
			"not managed; [] declares none, so the defaults apply.",
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"prefix": schema.StringAttribute{Required: true, Description: "Capital letters and digits, starting with a letter, e.g. REQ."},
			"family": schema.StringAttribute{Required: true, Description: "The family's name, e.g. requirement."},
			"defined_in": schema.ListAttribute{Optional: true, ElementType: types.StringType,
				Description: "Specification types, e.g. [\"TEST_PLAN\", \"BOARD_TEST_REPORT\"]; BOARD_ELEMENT_CHECK_REPORT defines nothing."},
		}},
	}
}

// elementFamiliesSpec is the board file's elementFamilies: a family name, or {family, definedIn} where the list is set.
func elementFamiliesSpec(families []elementFamilyModel) map[string]any {
	out := map[string]any{}
	for _, f := range families {
		if f.DefinedIn == nil {
			out[f.Prefix.ValueString()] = f.Family.ValueString()
			continue
		}
		list := []any{}
		for _, t := range f.DefinedIn {
			list = append(list, t.ValueString())
		}
		out[f.Prefix.ValueString()] = map[string]any{"family": f.Family.ValueString(), "definedIn": list}
	}
	return out
}

// elementFamiliesFromExport reads the export's elementFamilies back in the configured order, a prefix ReARM has and
// the configuration does not after them in prefix order. A configured prefix, family or type that reads the same
// once trimmed and upper-cased where ReARM normalises keeps its spelling.
func elementFamiliesFromExport(configured []elementFamilyModel, exported map[string]any) []elementFamilyModel {
	read := map[string]elementFamilyModel{}
	for prefix, v := range exported {
		m := elementFamilyModel{Prefix: types.StringValue(prefix)}
		switch e := v.(type) {
		case map[string]any:
			m.Family = types.StringValue(str(e["family"]))
			m.DefinedIn = []types.String{}
			for _, t := range list(e["definedIn"]) {
				m.DefinedIn = append(m.DefinedIn, types.StringValue(str(t)))
			}
		default:
			m.Family = types.StringValue(str(v))
		}
		read[prefix] = m
	}
	out := []elementFamilyModel{}
	seen := map[string]bool{}
	for _, c := range configured {
		prefix := strings.TrimSpace(c.Prefix.ValueString())
		m, ok := read[prefix]
		if !ok {
			continue
		}
		seen[prefix] = true
		if strings.TrimSpace(c.Family.ValueString()) == m.Family.ValueString() {
			m.Family = c.Family
		}
		m.Prefix = c.Prefix
		if len(c.DefinedIn) == len(m.DefinedIn) && c.DefinedIn != nil && m.DefinedIn != nil {
			same := true
			for i := range c.DefinedIn {
				if strings.TrimSpace(c.DefinedIn[i].ValueString()) != m.DefinedIn[i].ValueString() {
					same = false
				}
			}
			if same {
				m.DefinedIn = c.DefinedIn
			}
		}
		out = append(out, m)
	}
	var rest []string
	for prefix := range read {
		if !seen[prefix] {
			rest = append(rest, prefix)
		}
	}
	sort.Strings(rest)
	for _, prefix := range rest {
		out = append(out, read[prefix])
	}
	return out
}
