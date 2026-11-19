package handlers

import (
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"myauthservice/models"
)

// petRowWithBreed — строка ответа GetPetForUser для питомца-собаки владельца с
// заданной породой (breed) и собственным названием (breed_custom).
func petRowWithBreed(breed, breedCustom any) []driver.Value {
	row := make([]driver.Value, len(petColumns))
	for i, column := range petColumns {
		switch column {
		case "id":
			row[i] = testPetID
		case "name":
			row[i] = "Rex"
		case "species":
			row[i] = "dog"
		case "sterilized", "shared":
			row[i] = false
		case "access":
			row[i] = "owner"
		case "owner_id":
			row[i] = testUserID
		case "owner_name":
			row[i] = "Owner"
		case "breed":
			row[i] = breed
		case "breed_custom":
			row[i] = breedCustom
		}
	}
	return row
}

func str(s string) *string { return &s }

func TestCreatePetHandler_BreedCustomWithoutOtherRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{
		Name: "Rex", Species: "dog", Breed: str("111a9c3e-5b6d-4f9a-8c2e-1d6b9a5f3e01"), BreedCustom: str("Дворняга"),
	}, true)
	CreatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_BreedCustomWithoutBreedRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{
		Name: "Rex", Species: "dog", BreedCustom: str("Дворняга"),
	}, true)
	CreatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_BreedCustomTooLongRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{
		Name: "Rex", Species: "dog", Breed: str(models.PetBreedOther), BreedCustom: str(strings.Repeat("я", 101)),
	}, true)
	CreatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdatePetHandler_BreedCustomForNonOtherBreedRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(petRowWithBreed("111a9c3e", nil)...))
	expectNoWeightEvent(mock)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{BreedCustom: str("Дворняга")}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdatePetHandler_BreedCustomAcceptedWhenStoredBreedIsOther(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(petRowWithBreed(models.PetBreedOther, "Дворняга")...))
	expectNoWeightEvent(mock)
	mock.ExpectExec(`UPDATE pet\s+SET breed_custom = \$1\s+WHERE`).
		WithArgs("Метис", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{BreedCustom: str("  Метис ")}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePetHandler_BreedCustomAcceptedTogetherWithOther(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(petRowWithBreed("111a9c3e", nil)...))
	expectNoWeightEvent(mock)
	mock.ExpectExec(`UPDATE pet\s+SET breed = \$1, breed_custom = \$2\s+WHERE`).
		WithArgs(models.PetBreedOther, "Дворняга", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{
		Breed: str(models.PetBreedOther), BreedCustom: str("Дворняга"),
	}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePetHandler_SwitchingAwayFromOtherClearsBreedCustom(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(petRowWithBreed(models.PetBreedOther, "Дворняга")...))
	expectNoWeightEvent(mock)
	mock.ExpectExec(`UPDATE pet\s+SET breed = \$1, breed_custom = \$2\s+WHERE`).
		WithArgs("111a9c3e", nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Breed: str("111a9c3e")}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePetHandler_EmptyBreedCustomClearsIt(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(petRowWithBreed(models.PetBreedOther, "Дворняга")...))
	expectNoWeightEvent(mock)
	mock.ExpectExec(`UPDATE pet\s+SET breed_custom = \$1\s+WHERE`).
		WithArgs(nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{BreedCustom: str("")}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}
