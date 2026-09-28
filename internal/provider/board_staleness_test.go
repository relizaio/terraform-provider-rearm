package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func settingsWithStaleness(st *boardStalenessModel) *boardSettingsModel {
	return &boardSettingsModel{BudgetMicros: types.Int64Null(), SoftAlertPercent: types.Int64Null(),
		CycleCap: types.Int64Null(), NoProgressRepeatsToStop: types.Int64Null(), BlockingPriority: types.Int64Null(),
		CompletionPriority: types.Int64Null(), HumanQueueAgeMinutes: types.Int64Null(), Staleness: st}
}

// staleness (task RD3-4): the block goes to the board file under settings, only the thresholds set;
// read back whole, so a threshold ReARM has and the configuration does not is drift; imported when
// the board has one; absent when neither sets it.
func TestStalenessSentReadBackWholeAndImported(t *testing.T) {
	m := boardModel()
	m.Settings = settingsWithStaleness(&boardStalenessModel{RoleUnstaffedMinutes: types.Int64Value(60),
		HopNoProgressMinutes: types.Int64Value(90), DeliveryStuckMinutes: types.Int64Null(),
		SeatSilentMinutes: types.Int64Null(), RepeatMinutes: types.Int64Null()})
	settings := m.toSpec().Spec["settings"].(map[string]any)
	st, ok := settings["staleness"].(map[string]any)
	if !ok || len(st) != 2 || st["roleUnstaffedMinutes"] != int64(60) || st["hopNoProgressMinutes"] != int64(90) {
		t.Fatalf("only the thresholds set, under settings.staleness: %v", settings)
	}

	e := exported()
	e["settings"] = map[string]any{"staleness": map[string]any{"roleUnstaffedMinutes": float64(60),
		"hopNoProgressMinutes": float64(90), "repeatMinutes": float64(300)}}
	m.fromExport(e, false)
	got := m.Settings.Staleness
	if got.RoleUnstaffedMinutes.ValueInt64() != 60 || got.HopNoProgressMinutes.ValueInt64() != 90 {
		t.Errorf("read back: %+v", got)
	}
	if got.RepeatMinutes.IsNull() || got.RepeatMinutes.ValueInt64() != 300 {
		t.Errorf("a threshold set outside the configuration is read, so the plan shows it: %+v", got)
	}
	if !got.DeliveryStuckMinutes.IsNull() {
		t.Errorf("an unset threshold stays null: %+v", got)
	}

	imported := boardModel()
	ie := exported()
	ie["settings"] = map[string]any{"staleness": map[string]any{"seatSilentMinutes": float64(30)}}
	imported.fromExport(ie, true)
	if imported.Settings == nil || imported.Settings.Staleness == nil || imported.Settings.Staleness.SeatSilentMinutes.ValueInt64() != 30 {
		t.Errorf("an import reads the block: %+v", imported.Settings)
	}

	plain := boardModel()
	pe := exported()
	pe["settings"] = map[string]any{"staleness": nil}
	plain.fromExport(pe, true)
	if plain.Settings != nil && plain.Settings.Staleness != nil {
		t.Errorf("no block on the board, none in state: %+v", plain.Settings.Staleness)
	}
	bare := boardModel()
	if _, ok := bare.toSpec().Spec["settings"]; ok {
		t.Error("no settings configured, none sent")
	}
}
