package models

import (
	"myauthservice/openapi"
	"testing"
)

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

// TestAllowedIconsMatchOpenAPI, TestAllowedGendersMatchOpenAPI и
// TestAllowedHabitationsMatchOpenAPI — сверка перечней допустимых значений
// species/icon, gender и habitation, зашитых в валидацию бэкенда
// (allowedIcons/allowedGenders/allowedHabitations), с OpenAPI-спекой
// (open-api/spec.json, регенерируется в openapi/types.gen.go командой
// `go generate ./...`). Ссылка на сгенерированные константы гарантирует, что
// расхождение (значение убрано/переименовано в спеке или в Go-валидации) не
// пройдёт компиляцию/тест — см. «Общие требования: Единый источник
// enum-словарей».
func TestAllowedIconsMatchOpenAPI(t *testing.T) {
	specIcons := []openapi.PetIconEnum{
		openapi.DOG, openapi.CAT, openapi.HAMSTER, openapi.GUINEAPIG, openapi.RABBIT,
		openapi.PARROT, openapi.CANARY, openapi.FISH, openapi.TURTLE, openapi.RAT,
		openapi.MOUSE, openapi.FERRET, openapi.HEDGEHOG, openapi.CHINCHILLA, openapi.MINIPIG,
		openapi.MINIGOAT, openapi.CHICKEN, openapi.DUCK, openapi.PIGEON, openapi.IGUANA,
		openapi.GECKO, openapi.BEARDEDAGAMA, openapi.SNAKE, openapi.PYTHON, openapi.FROG,
		openapi.AXOLOTL, openapi.TARANTULA, openapi.HERMITCRAB, openapi.ANTFARM, openapi.SNAIL,
		openapi.OTHER,
	}
	if len(specIcons) != len(allowedIcons) {
		t.Fatalf("spec defines %d icon values, allowedIcons has %d", len(specIcons), len(allowedIcons))
	}
	for _, v := range specIcons {
		if !IsValidIcon(string(v)) {
			t.Errorf("openapi icon %q missing from allowedIcons", v)
		}
	}
}

func TestAllowedGendersMatchOpenAPI(t *testing.T) {
	specGenders := []openapi.GetGenderEnum{
		openapi.GetGenderEnumMale, openapi.GetGenderEnumFemale, openapi.GetGenderEnumOther,
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
