package models

import (
	"myauthservice/openapi"
	"testing"
)

func TestIsKnownSpeciesValue(t *testing.T) {
	valid := []string{"DOG", "CAT", "OTHER", "AXOLOTL", "ANT_FARM"}
	for _, v := range valid {
		if !IsKnownSpeciesValue(v) {
			t.Errorf("expected %q to be a known species value", v)
		}
	}

	invalid := []string{"", "dog", "UNKNOWN_ANIMAL"}
	for _, v := range invalid {
		if IsKnownSpeciesValue(v) {
			t.Errorf("expected %q to not be a known species value", v)
		}
	}
}

// TestAllowedGendersMatchOpenAPI и TestAllowedHabitationsMatchOpenAPI —
// сверка перечней допустимых значений gender и habitation, зашитых в
// валидацию бэкенда (allowedGenders/allowedHabitations), с OpenAPI-спекой
// (open-api/spec.json, регенерируется в openapi/types.gen.go командой
// `go generate ./...`). Ссылка на сгенерированные константы гарантирует, что
// расхождение (значение убрано/переименовано в спеке или в Go-валидации) не
// пройдёт компиляцию/тест — см. «Общие требования: Единый источник
// enum-словарей».
//
// species (и, соответственно, knownSpeciesValues) не имеет отдельного enum в
// OpenAPI-спеке — species остаётся свободным текстом на уровне контракта
// API, а knownSpeciesValues используется только внутренне для определения
// применимости типа события (см. eventreg.IsApplicableToSpecies), поэтому
// для него нет аналогичной сверки со спекой.

func TestAllowedGendersMatchOpenAPI(t *testing.T) {
	specGenders := []openapi.GetGenderEnum{
		openapi.Male, openapi.Female, openapi.Other,
	}
	if len(specGenders) != len(allowedGenders) {
		t.Fatalf("spec defines %d gender values, allowedGenders has %d", len(specGenders), len(allowedGenders))
	}
	for _, v := range specGenders {
		if !IsValidGender(string(v)) {
			t.Errorf("openapi gender %q missing from allowedGenders", v)
		}
	}
}

func TestAllowedHabitationsMatchOpenAPI(t *testing.T) {
	specHabitations := []openapi.GetHabilitationEnum{
		openapi.Indoor, openapi.Outside, openapi.Both,
	}
	if len(specHabitations) != len(allowedHabitations) {
		t.Fatalf("spec defines %d habitation values, allowedHabitations has %d", len(specHabitations), len(allowedHabitations))
	}
	for _, v := range specHabitations {
		if !IsValidHabitation(string(v)) {
			t.Errorf("openapi habitation %q missing from allowedHabitations", v)
		}
	}
}
