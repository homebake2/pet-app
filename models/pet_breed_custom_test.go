package models

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func strPtr(s string) *string { return &s }

func TestValidateBreedCustom(t *testing.T) {
	other := strPtr(PetBreedOther)
	labrador := strPtr("111a9c3e-5b6d-4f9a-8c2e-1d6b9a5f3e01")

	t.Run("absent name is always valid", func(t *testing.T) {
		assert.Empty(t, ValidateBreedCustom(nil, nil))
		assert.Empty(t, ValidateBreedCustom(labrador, nil))
	})

	t.Run("blank name is treated as absent, even without other", func(t *testing.T) {
		assert.Empty(t, ValidateBreedCustom(labrador, strPtr("   ")))
		assert.Empty(t, ValidateBreedCustom(nil, strPtr("")))
	})

	t.Run("name with breed other is valid", func(t *testing.T) {
		assert.Empty(t, ValidateBreedCustom(other, strPtr("Дворняга")))
	})

	t.Run("name without breed other is rejected", func(t *testing.T) {
		assert.NotEmpty(t, ValidateBreedCustom(labrador, strPtr("Дворняга")))
		assert.NotEmpty(t, ValidateBreedCustom(nil, strPtr("Дворняга")))
	})

	t.Run("length is counted in characters, not bytes", func(t *testing.T) {
		assert.Empty(t, ValidateBreedCustom(other, strPtr(strings.Repeat("я", PetBreedCustomMaxLen))))
		assert.NotEmpty(t, ValidateBreedCustom(other, strPtr(strings.Repeat("я", PetBreedCustomMaxLen+1))))
	})

	t.Run("surrounding spaces do not count toward the length", func(t *testing.T) {
		padded := "  " + strings.Repeat("a", PetBreedCustomMaxLen) + "  "
		assert.Empty(t, ValidateBreedCustom(other, &padded))
	})
}

func TestNormalizeBreedCustom(t *testing.T) {
	assert.Nil(t, NormalizeBreedCustom(nil))
	assert.Nil(t, NormalizeBreedCustom(strPtr("   ")))
	assert.Equal(t, "Дворняга", *NormalizeBreedCustom(strPtr("  Дворняга ")))
}
