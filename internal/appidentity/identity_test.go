package appidentity

import "testing"

func TestAppNames(t *testing.T) {
	if AppName != "groundskeeper" {
		t.Errorf("AppName = %q, want groundskeeper", AppName)
	}
	if LegacyName != "agent-deck" {
		t.Errorf("LegacyName = %q, want agent-deck", LegacyName)
	}
}

func TestIsLegacy(t *testing.T) {
	if !IsLegacy("agent-deck") {
		t.Error("agent-deck should be legacy")
	}
	if !IsLegacy(".agent-deck") {
		t.Error(".agent-deck should be legacy")
	}
	if IsLegacy("groundskeeper") {
		t.Error("groundskeeper should not be legacy")
	}
}

func TestShouldUseLegacy(t *testing.T) {
	if !ShouldUseLegacy("agent-deck") {
		t.Error("agent-deck path should be legacy fallback")
	}
	if ShouldUseLegacy("groundskeeper") {
		t.Error("groundskeeper should not be legacy")
	}
}
