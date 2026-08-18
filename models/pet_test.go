package models

import "testing"

func TestIsValidEventType(t *testing.T) {
	valid := []string{"weight", "urine", "defecation", "vomit", "diarrhea", "other"}
	for _, v := range valid {
		if !IsValidEventType(v) {
			t.Errorf("expected %q to be a valid event type", v)
		}
	}

	invalid := []string{"", "type", "WEIGHT", "unknown", "food"}
	for _, v := range invalid {
		if IsValidEventType(v) {
			t.Errorf("expected %q to be an invalid event type", v)
		}
	}
}

func TestIsValidIcon(t *testing.T) {
	valid := []string{"DOG", "CAT", "OTHER", "AXOLOTL", "ANT_FARM"}
	for _, v := range valid {
		if !IsValidIcon(v) {
			t.Errorf("expected %q to be a valid icon", v)
		}
	}

	invalid := []string{"", "dog", "UNKNOWN_ANIMAL"}
	for _, v := range invalid {
		if IsValidIcon(v) {
			t.Errorf("expected %q to be an invalid icon", v)
		}
	}
}
