package provider

import (
	"context"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// groupModel is one task group as the board file declares it (task RD2-30): configuration only,
// dependencies by key. Each member set here is managed; one left unset keeps what ReARM has.
type groupModel struct {
	Key          types.String   `tfsdk:"key"`
	Name         types.String   `tfsdk:"name"`
	Description  types.String   `tfsdk:"description"`
	DependsOn    []types.String `tfsdk:"depends_on"`
	DefaultLevel types.Int64    `tfsdk:"default_level"`
	Status       types.String   `tfsdk:"status"`
}

func groupsAttribute() schema.Attribute {
	return schema.ListNestedAttribute{
		Optional: true,
		Description: "The board's task groups, in display order. Set, it is the group list: a group left out is " +
			"closed by the apply, never deleted, and the plan warns which. [] deletes every group, refused while " +
			"one holds tasks. Unset, the groups are not managed here.",
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"key": schema.StringAttribute{Required: true,
				Description: "2 to 24 lower-case letters, digits and hyphens; groups are matched by key, so a new key is a new group."},
			"name":        schema.StringAttribute{Optional: true},
			"description": schema.StringAttribute{Optional: true},
			"depends_on": schema.ListAttribute{Optional: true, ElementType: types.StringType,
				Description: "Keys of the groups this one waits on: of this list, or groups the board already has. A loop is refused."},
			"default_level": schema.Int64Attribute{Optional: true,
				Description: "The level its tasks read when they set none (task, then group, then board)."},
			"status": schema.StringAttribute{Optional: true, Description: "OPEN or CLOSED; a closed group takes no new tasks."},
		}},
	}
}

func groupKey(k string) string { return strings.ToLower(strings.TrimSpace(k)) }

// groupsToSpec is the groups as the board file sends them: each group's key and the members it sets.
func groupsToSpec(groups []groupModel) []any {
	out := []any{}
	for _, g := range groups {
		e := map[string]any{"key": g.Key.ValueString()}
		putString(e, "name", g.Name)
		putString(e, "description", g.Description)
		if g.DependsOn != nil {
			deps := []any{}
			for _, d := range g.DependsOn {
				deps = append(deps, d.ValueString())
			}
			e["dependsOn"] = deps
		}
		putInt(e, "defaultLevel", g.DefaultLevel)
		putString(e, "status", g.Status)
		out = append(out, e)
	}
	return out
}

// groupsFromExport reads the groups back in the export's order. A configured group keeps its
// configured form -- the key's spelling, members it leaves unset, dependency spellings -- when ReARM
// holds the same; one ReARM has open and the configuration does not list appears, so the plan closes
// it; one it has closed and the configuration does not list is what dropping a group leaves, and
// stays out. On an import (full) every group is read with every member.
func groupsFromExport(configured []groupModel, exported []any, full bool) []groupModel {
	byKey := map[string]groupModel{}
	for _, g := range configured {
		byKey[groupKey(g.Key.ValueString())] = g
	}
	out := []groupModel{}
	for _, x := range exported {
		e, ok := x.(map[string]any)
		if !ok {
			continue
		}
		key := str(e["key"])
		g, listed := byKey[groupKey(key)]
		if !listed {
			if !full && strings.EqualFold(str(e["status"]), "CLOSED") {
				continue
			}
			g = groupModel{Key: types.StringValue(key), Name: types.StringNull(), Description: types.StringNull(),
				DefaultLevel: types.Int64Null(), Status: types.StringNull()}
		}
		readGroup(&g, e, full || !listed)
		out = append(out, g)
	}
	return out
}

func readGroup(g *groupModel, e map[string]any, full bool) {
	readString(&g.Name, e, "name", full)
	readString(&g.Description, e, "description", full)
	readInt(&g.DefaultLevel, e, "defaultLevel", full)
	readString(&g.Status, e, "status", full)
	if full || g.DependsOn != nil {
		// The export omits dependsOn when there are none; a configured [] then reads back as [].
		server := []types.String{}
		for _, v := range list(e["dependsOn"]) {
			server = append(server, types.StringValue(str(v)))
		}
		if !sameKeys(g.DependsOn, server) {
			g.DependsOn = server
		}
		if full && len(server) == 0 {
			g.DependsOn = nil
		}
	}
}

// sameKeys reports whether a configured key list names the server's, in order, as ReARM lower-cases keys.
func sameKeys(configured, server []types.String) bool {
	if configured == nil || len(configured) != len(server) {
		return false
	}
	for i := range configured {
		if groupKey(configured[i].ValueString()) != server[i].ValueString() {
			return false
		}
	}
	return true
}

// droppedGroups are the keys the state holds and the plan does not: the groups an apply closes.
func droppedGroups(state, plan []groupModel) []string {
	kept := map[string]bool{}
	for _, g := range plan {
		kept[groupKey(g.Key.ValueString())] = true
	}
	out := []string{}
	for _, g := range state {
		if k := groupKey(g.Key.ValueString()); !kept[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// warnGroupChanges says in the plan what dropping groups does: an empty list deletes them all, a
// shorter one closes the groups it leaves out. Nothing when the groups are not managed.
func warnGroupChanges(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state, plan []groupModel
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("groups"), &state)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("groups"), &plan)...)
	if resp.Diagnostics.HasError() || plan == nil {
		return
	}
	if summary, detail := groupPlanWarning(state, plan); summary != "" {
		resp.Diagnostics.AddAttributeWarning(path.Root("groups"), summary, detail)
	}
}

func groupPlanWarning(state, plan []groupModel) (string, string) {
	dropped := droppedGroups(state, plan)
	if len(dropped) == 0 {
		return "", ""
	}
	if len(plan) == 0 {
		return "Groups deleted", "groups = [] deletes every group of the board (" + strings.Join(dropped, ", ") +
			"); ReARM refuses it while a group holds tasks."
	}
	return "Groups closed", "The apply closes " + strings.Join(dropped, ", ") + ": a group the configuration drops " +
		"is closed, not deleted. Its tasks stay in it, and it takes no new ones."
}
