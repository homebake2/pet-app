package eventreg

import (
	"encoding/json"
	"testing"
)

func TestQuickActionDefaultsPassValidation(t *testing.T) {
	for eventType, value := range quickActionDefaults {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("%s: %v", eventType, err)
		}
		if msg := ValidateValue(eventType, raw); msg != "" {
			t.Errorf("%s: default value %s rejected: %s", eventType, raw, msg)
		}
		if IsSystemType(eventType) {
			t.Errorf("%s: system type cannot be a quick action", eventType)
		}
	}
}

func TestQuickActionCandidatesFollowDictionaryOrder(t *testing.T) {
	got := QuickActionCandidates("OTHER")
	if len(got) != len(quickActionDefaults) {
		t.Fatalf("OTHER must get every candidate: got %d, want %d", len(got), len(quickActionDefaults))
	}
	order := map[string]int{}
	for i, eventType := range Types() {
		order[eventType] = i
	}
	for i := 1; i < len(got); i++ {
		if order[got[i-1].Type] >= order[got[i].Type] {
			t.Errorf("candidates out of dictionary order: %s before %s", got[i-1].Type, got[i].Type)
		}
	}
}

func TestQuickActionCandidatesFilterBySpecies(t *testing.T) {
	for _, species := range []string{"CAT", "DOG", "BIRD", "FISH"} {
		for _, candidate := range QuickActionCandidates(species) {
			if !IsApplicableToSpecies(candidate.Type, species) {
				t.Errorf("%s: %s is not applicable", species, candidate.Type)
			}
		}
	}
}

func TestQuickActionCandidatesReturnIndependentValues(t *testing.T) {
	first := QuickActionCandidates("OTHER")
	first[0].Value["status"] = "mutated"
	second := QuickActionCandidates("OTHER")
	if second[0].Value["status"] == "mutated" {
		t.Error("candidate value must be a copy of the defaults")
	}
}
