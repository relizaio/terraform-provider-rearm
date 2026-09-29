package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ladder (task RD3-6): the levels go to the board file under settings.ladder, in order, with the prompt when
// set; read back whole, so a level or a prompt ReARM has and the configuration does not is drift; imported when
// the board has one; absent when neither sets it.
func TestLadderSentReadBackWholeAndImported(t *testing.T) {
	m := boardModel()
	m.Settings = settingsWithStaleness(nil)
	m.Settings.Ladder = &boardLadderModel{Levels: []boardLadderLevelModel{
		{Name: types.StringValue("requirements"), Description: types.StringValue("what the client asks")},
		{Name: types.StringValue("solution"), Description: types.StringNull()},
	}, Prompt: types.StringNull()}
	settings := m.toSpec().Spec["settings"].(map[string]any)
	ladder, ok := settings["ladder"].(map[string]any)
	if !ok {
		t.Fatalf("no settings.ladder: %v", settings)
	}
	levels := ladder["levels"].([]any)
	if len(levels) != 2 || levels[0].(map[string]any)["name"] != "requirements" ||
		levels[0].(map[string]any)["description"] != "what the client asks" || levels[1].(map[string]any)["name"] != "solution" {
		t.Errorf("the levels in order: %v", levels)
	}
	if _, has := levels[1].(map[string]any)["description"]; has {
		t.Errorf("an unset description is not sent: %v", levels[1])
	}
	if _, has := ladder["prompt"]; has {
		t.Errorf("an unset prompt is not sent: %v", ladder)
	}

	e := exported()
	e["settings"] = map[string]any{"ladder": map[string]any{"prompt": "custom {{levels}}", "levels": []any{
		map[string]any{"number": float64(0), "name": "requirements", "description": "what the client asks"},
		map[string]any{"number": float64(1), "name": "solution"},
		map[string]any{"number": float64(2), "name": "components"},
	}}}
	m.fromExport(e, false)
	got := m.Settings.Ladder
	if len(got.Levels) != 3 || got.Levels[2].Name.ValueString() != "components" {
		t.Errorf("a level set outside the configuration is read, so the plan shows it: %+v", got)
	}
	if got.Prompt.ValueString() != "custom {{levels}}" {
		t.Errorf("the prompt is read back: %+v", got.Prompt)
	}
	if !got.Levels[1].Description.IsNull() {
		t.Errorf("an unset description stays null: %+v", got.Levels[1])
	}

	imported := boardModel()
	ie := exported()
	ie["settings"] = map[string]any{"ladder": map[string]any{"levels": []any{map[string]any{"number": float64(0), "name": "only"}}}}
	imported.fromExport(ie, true)
	if imported.Settings == nil || imported.Settings.Ladder == nil || imported.Settings.Ladder.Levels[0].Name.ValueString() != "only" {
		t.Errorf("an import reads the ladder: %+v", imported.Settings)
	}

	plain := boardModel()
	pe := exported()
	pe["settings"] = map[string]any{"ladder": nil}
	plain.fromExport(pe, true)
	if plain.Settings != nil && plain.Settings.Ladder != nil {
		t.Errorf("no ladder on the board, none in state: %+v", plain.Settings.Ladder)
	}
}

// ReARM trims ladder names and descriptions and stores a blank description or prompt as none: the configured
// spelling is kept where it means the same, so it is not read back as a change; a real change still is.
func TestLadderReadBackKeepsAnEquivalentConfiguredSpelling(t *testing.T) {
	prior := &boardLadderModel{Levels: []boardLadderLevelModel{
		{Name: types.StringValue(" requirements "), Description: types.StringValue("  ")},
		{Name: types.StringValue("solution"), Description: types.StringValue("the decisions")},
	}, Prompt: types.StringValue(" ")}
	got := ladderFromExport(prior, map[string]any{"levels": []any{
		map[string]any{"number": float64(0), "name": "requirements"},
		map[string]any{"number": float64(1), "name": "decisions", "description": "the decisions "},
	}})
	if got.Levels[0].Name.ValueString() != " requirements " || got.Levels[0].Description.ValueString() != "  " {
		t.Errorf("the configured spelling is kept: %+v", got.Levels[0])
	}
	if got.Prompt.ValueString() != " " {
		t.Errorf("a blank prompt read as none keeps the configured blank: %+v", got.Prompt)
	}
	if got.Levels[1].Name.ValueString() != "decisions" {
		t.Errorf("a renamed rung is drift: %+v", got.Levels[1])
	}
	if got.Levels[1].Description.ValueString() != "the decisions" {
		t.Errorf("an equivalent description keeps the configured one: %+v", got.Levels[1])
	}
	if unset := ladderFromExport(&boardLadderModel{Prompt: types.StringNull()}, map[string]any{"prompt": "set elsewhere"}); unset.Prompt.ValueString() != "set elsewhere" {
		t.Errorf("a prompt set outside the configuration is read: %+v", unset.Prompt)
	}
}
