package models

import (
	"encoding/json"
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

func TestAllowedSizeCategoriesMatchOpenAPI(t *testing.T) {
	specSizeCategories := []openapi.PetSizeCategoryEnum{
		openapi.Small, openapi.Medium, openapi.Large,
	}
	if len(specSizeCategories) != len(allowedSizeCategories) {
		t.Fatalf("spec defines %d size_category values, allowedSizeCategories has %d", len(specSizeCategories), len(allowedSizeCategories))
	}
	for _, v := range specSizeCategories {
		if !IsValidSizeCategory(string(v)) {
			t.Errorf("openapi size_category %q missing from allowedSizeCategories", v)
		}
	}
}

func TestAllowedWaterTypesMatchOpenAPI(t *testing.T) {
	specWaterTypes := []openapi.PetWaterTypeEnum{
		openapi.Freshwater, openapi.Saltwater,
	}
	if len(specWaterTypes) != len(allowedWaterTypes) {
		t.Fatalf("spec defines %d water_type values, allowedWaterTypes has %d", len(specWaterTypes), len(allowedWaterTypes))
	}
	for _, v := range specWaterTypes {
		if !IsValidWaterType(string(v)) {
			t.Errorf("openapi water_type %q missing from allowedWaterTypes", v)
		}
	}
}

// TestApplyExplicitNullClears проверяет, что Clear*-флаги профильных полей
// size_category/water_type/enclosure_volume_l/group_size отличают явный
// JSON null от отсутствия ключа (см. «Редактирование питомца — Backend»):
// отсутствие ключа не должно устанавливать флаг очистки, а явный null —
// должен, независимо от значения соответствующего *T-поля после
// json.Unmarshal (оба случая дают nil).
func TestApplyExplicitNullClears(t *testing.T) {
	var req UpdatePetRequest
	body := []byte(`{"size_category": null, "water_type": "freshwater", "name": "Rex"}`)
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := req.ApplyExplicitNullClears(body); err != nil {
		t.Fatalf("ApplyExplicitNullClears: %v", err)
	}

	if !req.ClearSizeCategory {
		t.Errorf("expected ClearSizeCategory=true for explicit null")
	}
	if req.ClearWaterType {
		t.Errorf("expected ClearWaterType=false: key present with a value, not null")
	}
	if req.ClearEnclosureVolumeL {
		t.Errorf("expected ClearEnclosureVolumeL=false: key absent")
	}
	if req.ClearGroupSize {
		t.Errorf("expected ClearGroupSize=false: key absent")
	}
	if req.WaterType == nil || *req.WaterType != "freshwater" {
		t.Errorf("expected WaterType to be set from the request body")
	}
}
