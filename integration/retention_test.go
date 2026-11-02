//go:build integration

// Сроки хранения данных: физическое удаление мягко удалённых записей, очистка
// файлов без владельца, предложений и счётчиков IP (database/retention.go).
package integration

import (
	"myauthservice/database"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ageSoftDeleted сдвигает момент мягкого удаления питомца в прошлое.
func ageSoftDeleted(t *testing.T, petID string, age time.Duration) {
	t.Helper()
	_, err := database.DB.Exec(`UPDATE pet SET deleted_at = now() - make_interval(secs => $2) WHERE id = $1`, petID, age.Seconds())
	require.NoError(t, err)
}

func TestRetention_SoftDeletedPetPurgedAfterRetention(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	dog := createSpeciesPet(t, tokens.AccessToken, "Рекс", "DOG")

	var shared eventPetsBody
	resp := postEventPets(t, tokens.AccessToken, []string{cat, dog}, "feeding", feedingValue)
	require.Equal(t, http.StatusCreated, resp.status)
	resp.decode(t, &shared)
	createEvent(t, tokens.AccessToken, cat, "2024-01-01T10:00:00Z", "weight", map[string]any{"amount": 4.2})

	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/pet/"+cat, nil, tokens.AccessToken).status)

	// Срок ещё не вышел: ничего не удаляется.
	ageSoftDeleted(t, cat, database.SoftDeletedRetention-time.Hour)
	purged, err := database.PurgeSoftDeleted(database.SoftDeletedRetention)
	require.NoError(t, err)
	require.Zero(t, purged)
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM pet WHERE id = $1`, cat))

	// Срок вышел: питомец удалён физически вместе со своим событием, общее
	// событие осталось у другого питомца.
	ageSoftDeleted(t, cat, database.SoftDeletedRetention+time.Hour)
	purged, err = database.PurgeSoftDeleted(database.SoftDeletedRetention)
	require.NoError(t, err)
	require.NotZero(t, purged)
	require.Zero(t, countRows(t, `SELECT COUNT(*) FROM pet WHERE id = $1`, cat))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM pet WHERE id = $1`, dog))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event`), "остаётся только общее событие")
	require.Equal(t, []string{dog}, petIDsOf(getEventPets(t, tokens.AccessToken, shared.ID).Pets))

	// Повторный проход ничего не находит.
	purged, err = database.PurgeSoftDeleted(database.SoftDeletedRetention)
	require.NoError(t, err)
	require.Zero(t, purged)
}

func TestRetention_SoftDeletedEventPurgedAfterRetention(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	eventID := createEventReturningID(t, tokens.AccessToken, petID, "2024-01-01T10:00:00Z", "weight", map[string]any{"amount": 4.2})

	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/events/"+eventID, nil, tokens.AccessToken).status)
	_, err := database.DB.Exec(`UPDATE event SET deleted_at = now() - interval '91 days' WHERE id = $1`, eventID)
	require.NoError(t, err)

	_, err = database.PurgeSoftDeleted(database.SoftDeletedRetention)
	require.NoError(t, err)
	require.Zero(t, countRows(t, `SELECT COUNT(*) FROM event`))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM pet WHERE id = $1`, petID))
}

func TestRetention_AccountDeletionLeavesNoFilesOrSuggestions(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	upload := requestPetPhotoUploadURL(t, tokens.AccessToken, petID, "image/jpeg")
	require.Equal(t, http.StatusNoContent,
		doRequest(t, http.MethodPost, "/files/"+upload.FileID+"/complete", nil, tokens.AccessToken).status)

	var userID string
	require.NoError(t, database.DB.QueryRow(`SELECT id FROM users LIMIT 1`).Scan(&userID))
	_, err := database.CreateSuggestion(userID, "other", "Идея", "", "me@example.com")
	require.NoError(t, err)
	_, err = database.CreateSuggestion("anonymous", "other", "Чужая идея", "", "")
	require.NoError(t, err)

	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/profile", nil, tokens.AccessToken).status)
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM suggestion`), "остаётся только чужое предложение")

	// Строка файла остаётся без владельца и убирается очисткой; объект S3
	// удаляется, потому что ссылок на него не осталось.
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM file`))
	deleted, keys, err := database.SweepOrphanFiles(database.UnconfirmedFileRetention)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	require.Len(t, keys, 1)
	require.Zero(t, countRows(t, `SELECT COUNT(*) FROM file`))
}

func TestRetention_SweepKeepsLiveAndFreshFiles(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	confirmed := requestPetPhotoUploadURL(t, tokens.AccessToken, petID, "image/jpeg")
	require.Equal(t, http.StatusNoContent,
		doRequest(t, http.MethodPost, "/files/"+confirmed.FileID+"/complete", nil, tokens.AccessToken).status)
	fresh := requestPetPhotoUploadURL(t, tokens.AccessToken, petID, "image/png")

	// Подтверждённый файл живого питомца и свежая незавершённая загрузка не
	// затрагиваются.
	deleted, _, err := database.SweepOrphanFiles(database.UnconfirmedFileRetention)
	require.NoError(t, err)
	require.Zero(t, deleted)

	// Незавершённая загрузка старше срока удаляется, подтверждённый файл
	// остаётся.
	_, err = database.DB.Exec(`UPDATE file SET created_at = now() - interval '25 hours' WHERE id = $1`, fresh.FileID)
	require.NoError(t, err)
	deleted, keys, err := database.SweepOrphanFiles(database.UnconfirmedFileRetention)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	require.Len(t, keys, 1)
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM file WHERE id = $1`, confirmed.FileID))
}

func TestRetention_StaleSuggestionsAndRegistrationAttempts(t *testing.T) {
	resetDB(t)
	oldID, err := database.CreateSuggestion("anonymous", "other", "Старая", "", "")
	require.NoError(t, err)
	_, err = database.CreateSuggestion("anonymous", "other", "Свежая", "", "")
	require.NoError(t, err)
	_, err = database.DB.Exec(`UPDATE suggestion SET created_at = now() - interval '25 months' WHERE id = $1`, oldID)
	require.NoError(t, err)

	deleted, err := database.DeleteStaleSuggestions(database.SuggestionRetentionMonths)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM suggestion`))

	_, err = database.DB.Exec(`INSERT INTO registration_rate_limit (ip, day, count) VALUES
		('10.0.0.1', (now() AT TIME ZONE 'UTC')::date, 1),
		('10.0.0.2', (now() AT TIME ZONE 'UTC')::date - 1, 1),
		('10.0.0.3', (now() AT TIME ZONE 'UTC')::date - 2, 1)`)
	require.NoError(t, err)
	deleted, err = database.DeleteStaleRegistrationAttempts(database.RegistrationAttemptRetentionDays)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted, "остаются текущие и предыдущие сутки")
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM registration_rate_limit`))
}
