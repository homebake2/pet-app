//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"myauthservice/database"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Передача питомца: создание/просмотр/отмена передачи владельцем,
// предпросмотр/принятие/отклонение по коду получателем, перенос данных при
// принятии.

const transferCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

type transferBody struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	ExpiresAt string `json:"expires_at"`
}

type transferUser struct {
	login string
	token string
	id    string
}

func newTransferUser(t *testing.T, firstName string) transferUser {
	t.Helper()
	login := uniqueLogin(t)
	tokens := registerUser(t, login, "password123")
	if firstName != "" {
		createProfile(t, tokens.AccessToken, firstName)
	}
	var id string
	require.NoError(t, database.DB.QueryRow(`SELECT id::text FROM users WHERE login = $1`, login).Scan(&id))
	return transferUser{login: login, token: tokens.AccessToken, id: id}
}

func createTransfer(t *testing.T, token, petID string) transferBody {
	t.Helper()
	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/transfer", nil, token)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
	var body transferBody
	resp.decode(t, &body)
	return body
}

func postTransferCode(t *testing.T, action, token, code string) apiResponse {
	t.Helper()
	return doRequest(t, http.MethodPost, "/pet-transfers/"+action, map[string]string{"code": code}, token)
}

func errorCodeOf(t *testing.T, resp apiResponse) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	resp.decode(t, &body)
	return body.Code
}

func transferStatus(t *testing.T, transferID string) string {
	t.Helper()
	var status string
	require.NoError(t, database.DB.QueryRow(`SELECT status FROM pet_transfer WHERE id = $1`, transferID).Scan(&status))
	return status
}

func insertConfirmedFile(t *testing.T, ownerType, ownerID, userID, objectKey string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := database.DB.Exec(`
		INSERT INTO file (id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at)
		VALUES ($1, $2, $3, $4, $5, 'image/png', NULL, 0, now())
	`, id, ownerType, ownerID, userID, objectKey)
	require.NoError(t, err)
	return id
}

func fileUserID(t *testing.T, fileID string) string {
	t.Helper()
	var userID string
	require.NoError(t, database.DB.QueryRow(`SELECT user_id::text FROM file WHERE id = $1`, fileID).Scan(&userID))
	return userID
}

func TestPetTransfer_CreateGetCancel(t *testing.T) {
	resetDB(t)
	owner := newTransferUser(t, "")
	stranger := newTransferUser(t, "")
	petID := createSpeciesPet(t, owner.token, "Барсик", "CAT")

	created := createTransfer(t, owner.token, petID)
	require.Len(t, created.Code, 10)
	for _, r := range created.Code {
		require.Truef(t, strings.ContainsRune(transferCodeAlphabet, r), "символ %q вне алфавита кода", r)
	}
	require.NotEmpty(t, created.ExpiresAt)
	require.Equal(t, "pending", transferStatus(t, created.ID))

	var snapshot struct{ name, species string }
	require.NoError(t, database.DB.QueryRow(`SELECT pet_name, pet_species FROM pet_transfer WHERE id = $1`, created.ID).Scan(&snapshot.name, &snapshot.species))
	require.Equal(t, "Барсик", snapshot.name)
	require.Equal(t, "CAT", snapshot.species)

	got := doRequest(t, http.MethodGet, "/pet/"+petID+"/transfer", nil, owner.token)
	require.Equalf(t, http.StatusOK, got.status, "%s", got.body)
	var active transferBody
	got.decode(t, &active)
	require.Equal(t, created, active)

	// У питомца не более одной активной передачи.
	dup := doRequest(t, http.MethodPost, "/pet/"+petID+"/transfer", nil, owner.token)
	require.Equal(t, http.StatusConflict, dup.status)
	require.Equal(t, "TRANSFER_ALREADY_PENDING", errorCodeOf(t, dup))

	// Чужой питомец — 404 на всех трёх методах.
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		resp := doRequest(t, method, "/pet/"+petID+"/transfer", nil, stranger.token)
		require.Equal(t, http.StatusNotFound, resp.status, method)
	}
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodPost, "/pet/"+uuid.NewString()+"/transfer", nil, owner.token).status)

	del := doRequest(t, http.MethodDelete, "/pet/"+petID+"/transfer", nil, owner.token)
	require.Equal(t, http.StatusNoContent, del.status)
	require.Equal(t, "cancelled", transferStatus(t, created.ID))
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodGet, "/pet/"+petID+"/transfer", nil, owner.token).status)
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodDelete, "/pet/"+petID+"/transfer", nil, owner.token).status)

	// Отменённый код больше не действует; новая передача получает новый код.
	require.Equal(t, http.StatusNotFound, postTransferCode(t, "preview", stranger.token, created.Code).status)
	again := createTransfer(t, owner.token, petID)
	require.NotEqual(t, created.Code, again.Code)
	require.NotEqual(t, created.ID, again.ID)
}

func TestPetTransfer_ExpiredTransferDoesNotBlockCreate(t *testing.T) {
	resetDB(t)
	owner := newTransferUser(t, "")
	petID := createSpeciesPet(t, owner.token, "Барсик", "CAT")
	created := createTransfer(t, owner.token, petID)

	_, err := database.DB.Exec(`UPDATE pet_transfer SET expires_at = now() - interval '1 minute' WHERE id = $1`, created.ID)
	require.NoError(t, err)

	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodGet, "/pet/"+petID+"/transfer", nil, owner.token).status)
	require.Equal(t, "expired", transferStatus(t, created.ID))

	again := createTransfer(t, owner.token, petID)
	require.NotEqual(t, created.ID, again.ID)
	require.Equal(t, "expired", transferStatus(t, created.ID))
	require.Equal(t, "pending", transferStatus(t, again.ID))
}

func TestPetTransfer_PreviewAndDecline(t *testing.T) {
	resetDB(t)
	sender := newTransferUser(t, "Анна")
	receiver := newTransferUser(t, "")
	petID := createSpeciesPet(t, sender.token, "Барсик", "CAT")
	created := createTransfer(t, sender.token, petID)

	// Код вводится в «человеческом» виде: регистр, пробелы и дефисы
	// игнорируются.
	formatted := strings.ToLower(created.Code[:5]) + " - " + created.Code[5:]
	preview := postTransferCode(t, "preview", receiver.token, formatted)
	require.Equalf(t, http.StatusOK, preview.status, "%s", preview.body)
	var body struct {
		Pet struct {
			Name    string  `json:"name"`
			Species string  `json:"species"`
			Photo   *string `json:"photo_url"`
		} `json:"pet"`
		SenderName string `json:"sender_name"`
		ExpiresAt  string `json:"expires_at"`
	}
	preview.decode(t, &body)
	require.Equal(t, "Барсик", body.Pet.Name)
	require.Equal(t, "CAT", body.Pet.Species)
	require.Nil(t, body.Pet.Photo)
	require.Equal(t, "Анна", body.SenderName)
	require.NotEmpty(t, body.ExpiresAt)
	require.Equal(t, "pending", transferStatus(t, created.ID), "предпросмотр ничего не меняет")

	decline := postTransferCode(t, "decline", receiver.token, created.Code)
	require.Equal(t, http.StatusNoContent, decline.status)
	require.Equal(t, "declined", transferStatus(t, created.ID))
	var toUser string
	require.NoError(t, database.DB.QueryRow(`SELECT to_user_id::text FROM pet_transfer WHERE id = $1`, created.ID).Scan(&toUser))
	require.Equal(t, receiver.id, toUser)

	// Питомец остался у отправителя, код больше не действует.
	require.Equal(t, http.StatusOK, doRequest(t, http.MethodGet, "/pet/"+petID, nil, sender.token).status)
	for _, action := range []string{"preview", "accept", "decline"} {
		require.Equal(t, http.StatusNotFound, postTransferCode(t, action, receiver.token, created.Code).status, action)
	}
}

func TestPetTransfer_PreviewSenderNameFallsBackToLogin(t *testing.T) {
	resetDB(t)
	sender := newTransferUser(t, "")
	receiver := newTransferUser(t, "")
	petID := createSpeciesPet(t, sender.token, "Барсик", "CAT")
	created := createTransfer(t, sender.token, petID)

	preview := postTransferCode(t, "preview", receiver.token, created.Code)
	require.Equal(t, http.StatusOK, preview.status)
	var body struct {
		SenderName string `json:"sender_name"`
	}
	preview.decode(t, &body)
	require.Equal(t, sender.login, body.SenderName)
}

func TestPetTransfer_OwnCodeConflict(t *testing.T) {
	resetDB(t)
	sender := newTransferUser(t, "")
	petID := createSpeciesPet(t, sender.token, "Барсик", "CAT")
	created := createTransfer(t, sender.token, petID)

	for _, action := range []string{"preview", "accept", "decline"} {
		resp := postTransferCode(t, action, sender.token, created.Code)
		require.Equal(t, http.StatusConflict, resp.status, action)
		require.Equal(t, "TRANSFER_OWN_PET", errorCodeOf(t, resp), action)
	}
	require.Equal(t, "pending", transferStatus(t, created.ID))
	// Собственный код не считается неудачной попыткой подбора.
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM pet_transfer_code_failure`))
}

func TestPetTransfer_InvalidExpiredAndUsedCodes(t *testing.T) {
	resetDB(t)
	sender := newTransferUser(t, "")
	receiver := newTransferUser(t, "")
	other := newTransferUser(t, "")
	petID := createSpeciesPet(t, sender.token, "Барсик", "CAT")

	// Невалидный по формату код — 400, неизвестный — 404.
	require.Equal(t, http.StatusBadRequest, postTransferCode(t, "preview", receiver.token, "short").status)
	empty := doUnvalidatedRequest(t, http.MethodPost, "/pet-transfers/preview", map[string]string{"code": ""}, receiver.token)
	require.Equal(t, http.StatusBadRequest, empty.status)
	require.Equal(t, http.StatusNotFound, postTransferCode(t, "preview", receiver.token, "ABCDE23456").status)

	// Истёкший код.
	expired := createTransfer(t, sender.token, petID)
	_, err := database.DB.Exec(`UPDATE pet_transfer SET expires_at = now() - interval '1 second' WHERE id = $1`, expired.ID)
	require.NoError(t, err)
	for _, action := range []string{"preview", "accept", "decline"} {
		require.Equal(t, http.StatusNotFound, postTransferCode(t, action, receiver.token, expired.Code).status, action)
	}
	require.Equal(t, "expired", transferStatus(t, expired.ID))

	// Использованный код.
	used := createTransfer(t, sender.token, petID)
	require.Equal(t, http.StatusOK, postTransferCode(t, "accept", receiver.token, used.Code).status)
	for _, action := range []string{"preview", "accept", "decline"} {
		require.Equal(t, http.StatusNotFound, postTransferCode(t, action, other.token, used.Code).status, action)
	}
	require.Equal(t, "accepted", transferStatus(t, used.ID))
}

func TestPetTransfer_PetDeletedInvalidatesCode(t *testing.T) {
	resetDB(t)
	sender := newTransferUser(t, "")
	receiver := newTransferUser(t, "")
	petID := createSpeciesPet(t, sender.token, "Барсик", "CAT")
	created := createTransfer(t, sender.token, petID)

	// Состояние, при котором питомец уже удалён, а передача ещё pending
	// (например, данные, оставшиеся от прежних версий): код всё равно
	// недействителен.
	_, err := database.DB.Exec(`UPDATE pet SET deleted_at = now() WHERE id = $1`, petID)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, postTransferCode(t, "preview", receiver.token, created.Code).status)
	require.Equal(t, http.StatusNotFound, postTransferCode(t, "accept", receiver.token, created.Code).status)
}

func TestPetTransfer_RateLimit(t *testing.T) {
	resetDB(t)
	sender := newTransferUser(t, "")
	guesser := newTransferUser(t, "")
	bystander := newTransferUser(t, "")
	petID := createSpeciesPet(t, sender.token, "Барсик", "CAT")
	created := createTransfer(t, sender.token, petID)

	for i := 0; i < 10; i++ {
		require.Equalf(t, http.StatusNotFound, postTransferCode(t, "preview", guesser.token, "ABCDE23456").status, "попытка %d", i+1)
	}
	// Лимит исчерпан: даже верный код отклоняется, на всех трёх эндпоинтах.
	for _, action := range []string{"preview", "accept", "decline"} {
		resp := postTransferCode(t, action, guesser.token, created.Code)
		require.Equal(t, http.StatusTooManyRequests, resp.status, action)
		require.Equal(t, "RATE_LIMITED", errorCodeOf(t, resp), action)
	}
	require.Equal(t, "pending", transferStatus(t, created.ID))

	// Учёт идёт по пользователю: остальные не затронуты.
	require.Equal(t, http.StatusOK, postTransferCode(t, "preview", bystander.token, created.Code).status)

	// Записи старше часа перестают учитываться.
	_, err := database.DB.Exec(`UPDATE pet_transfer_code_failure SET created_at = now() - interval '2 hours'`)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, postTransferCode(t, "preview", guesser.token, created.Code).status)
}

func TestPetTransfer_AcceptMovesDataToReceiver(t *testing.T) {
	resetDB(t)
	sender := newTransferUser(t, "Анна")
	receiver := newTransferUser(t, "")
	cat := createSpeciesPet(t, sender.token, "Барсик", "CAT")
	dog := createSpeciesPet(t, sender.token, "Рекс", "DOG")

	// Событие только передаваемого питомца — переносится целиком.
	singleID := createEventReturningID(t, sender.token, cat, "2024-01-01T10:00:00Z", "feeding", feedingValue)
	singleFile := insertConfirmedFile(t, "event_file", singleID, sender.id, "events/single.png")

	// Событие передаваемого питомца и другого питомца отправителя — копируется.
	multiResp := postEventPets(t, sender.token, []string{cat, dog}, "feeding", feedingValue)
	require.Equalf(t, http.StatusCreated, multiResp.status, "%s", multiResp.body)
	var multi eventPetsBody
	multiResp.decode(t, &multi)
	multiFile := insertConfirmedFile(t, "event_file", multi.ID, sender.id, "events/multi.png")

	// Событие только другого питомца отправителя не затрагивается.
	dogOnlyID := createEventReturningID(t, sender.token, dog, "2024-01-02T10:00:00Z", "feeding", feedingValue)

	// Напоминания: настройки только передаваемого питомца и общие с другим.
	catPlan := createOncePlan(t, sender.token, cat, "other", map[string]any{"label": "кот"}, futureDate(3), "09:00")
	catPlanFile := insertConfirmedFile(t, "reminder_plan_file", catPlan.ID, sender.id, "plans/cat.png")
	sharedPlan := createPlan(t, sender.token, map[string]any{
		"pet_ids":        []string{cat, dog},
		"type":           "other",
		"value":          map[string]any{"label": "оба"},
		"frequency_type": "once",
		"times":          []string{"10:00"},
		"start_date":     futureDate(4),
	})

	photoFile := insertConfirmedFile(t, "pet_photo", cat, sender.id, "pets/cat.png")

	created := createTransfer(t, sender.token, cat)
	accept := postTransferCode(t, "accept", receiver.token, strings.ToLower(created.Code))
	require.Equalf(t, http.StatusOK, accept.status, "%s", accept.body)
	var accepted struct {
		PetID string `json:"pet_id"`
	}
	accept.decode(t, &accepted)
	require.Equal(t, cat, accepted.PetID)

	require.Equal(t, "accepted", transferStatus(t, created.ID))
	var toUser string
	var resolved bool
	require.NoError(t, database.DB.QueryRow(`SELECT to_user_id::text, resolved_at IS NOT NULL FROM pet_transfer WHERE id = $1`, created.ID).Scan(&toUser, &resolved))
	require.Equal(t, receiver.id, toUser)
	require.True(t, resolved)

	// Питомец: у получателя как собственный, у отправителя — 404 везде.
	require.Equal(t, http.StatusOK, doRequest(t, http.MethodGet, "/pet/"+cat, nil, receiver.token).status)
	for _, path := range []string{"/pet/" + cat, "/pet/" + cat + "/events", "/pet/" + cat + "/transfer"} {
		require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodGet, path, nil, sender.token).status, path)
	}
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodPut, "/pet/"+cat, map[string]any{"name": "Х"}, sender.token).status)
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodDelete, "/pet/"+cat, nil, sender.token).status)
	require.Equal(t, http.StatusNotFound, postEventPets(t, sender.token, []string{cat}, "feeding", feedingValue).status)

	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	resp := doRequest(t, http.MethodGet, "/pet", nil, receiver.token)
	require.Equal(t, http.StatusOK, resp.status)
	resp.decode(t, &list)
	require.Len(t, list.Items, 1)
	require.Equal(t, cat, list.Items[0].ID)
	resp = doRequest(t, http.MethodGet, "/pet", nil, sender.token)
	resp.decode(t, &list)
	require.Len(t, list.Items, 1)
	require.Equal(t, dog, list.Items[0].ID)

	// Событие одного питомца перенесено целиком.
	require.Equal(t, []string{cat}, petIDsOf(getEventPets(t, receiver.token, singleID).Pets))
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodGet, "/events/"+singleID, nil, sender.token).status)
	require.Equal(t, receiver.id, fileUserID(t, singleFile))
	require.Equal(t, "events/single.png", objectKeyOf(t, singleFile))

	// Общее событие осталось у отправителя только с другим питомцем, а
	// получатель получил копию с одной связью и ссылочной строкой file.
	require.Equal(t, []string{dog}, petIDsOf(getEventPets(t, sender.token, multi.ID).Pets))
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodGet, "/events/"+multi.ID, nil, receiver.token).status)
	require.Equal(t, sender.id, fileUserID(t, multiFile))

	receiverEvents := listPetEvents(t, receiver.token, cat)
	require.Len(t, receiverEvents, 2)
	var copyID string
	for _, ev := range receiverEvents {
		if ev.ID != singleID {
			copyID = ev.ID
		}
	}
	require.NotEmpty(t, copyID)
	require.NotEqual(t, multi.ID, copyID)
	require.Equal(t, []string{cat}, petIDsOf(getEventPets(t, receiver.token, copyID).Pets))
	var copyType string
	var copyKey *string
	require.NoError(t, database.DB.QueryRow(`SELECT type, idempotency_key FROM event WHERE id = $1`, copyID).Scan(&copyType, &copyKey))
	require.Equal(t, "feeding", copyType)
	require.Nil(t, copyKey)
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM file WHERE owner_type = 'event_file' AND owner_id = $1 AND user_id = $2 AND object_key = 'events/multi.png' AND id <> $3`, copyID, receiver.id, multiFile))

	// События отправителя другого питомца не затронуты.
	require.Len(t, listPetEvents(t, sender.token, dog), 2)
	require.Equal(t, sender.id, eventOwner(t, dogOnlyID))

	// Напоминания не переносятся: связи удалены, настройки без питомцев
	// удалены вместе с файлами, общие остались у отправителя с другим питомцем.
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan_pet WHERE pet_id = $1`, cat))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE id = $1`, catPlan.ID))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder WHERE plan_id = $1`, catPlan.ID))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM file WHERE id = $1`, catPlanFile))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE id = $1 AND user_id = $2`, sharedPlan.ID, sender.id))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan_pet WHERE plan_id = $1 AND pet_id = $2`, sharedPlan.ID, dog))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder WHERE plan_id = $1`, sharedPlan.ID))
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodGet, "/reminder-plans/"+catPlan.ID, nil, receiver.token).status)

	// Файлы переносимых сущностей: владелец сменился, объекты остались.
	require.Equal(t, receiver.id, fileUserID(t, photoFile))
	require.Equal(t, "pets/cat.png", objectKeyOf(t, photoFile))
}

func objectKeyOf(t *testing.T, fileID string) string {
	t.Helper()
	var key string
	require.NoError(t, database.DB.QueryRow(`SELECT object_key FROM file WHERE id = $1`, fileID).Scan(&key))
	return key
}

func eventOwner(t *testing.T, eventID string) string {
	t.Helper()
	var userID string
	require.NoError(t, database.DB.QueryRow(`SELECT user_id::text FROM event WHERE id = $1`, eventID).Scan(&userID))
	return userID
}

func TestPetTransfer_AcceptDropsHiddenLinksOfMovedEvent(t *testing.T) {
	resetDB(t)
	sender := newTransferUser(t, "")
	receiver := newTransferUser(t, "")
	cat := createSpeciesPet(t, sender.token, "Барсик", "CAT")
	dog := createSpeciesPet(t, sender.token, "Рекс", "DOG")

	multiResp := postEventPets(t, sender.token, []string{cat, dog}, "feeding", feedingValue)
	require.Equalf(t, http.StatusCreated, multiResp.status, "%s", multiResp.body)
	var multi eventPetsBody
	multiResp.decode(t, &multi)

	// Другой питомец события удалён мягко: у события не остаётся других
	// видимых питомцев, поэтому оно переносится целиком, а скрытая связь
	// удаляется.
	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/pet/"+dog, nil, sender.token).status)

	created := createTransfer(t, sender.token, cat)
	require.Equal(t, http.StatusOK, postTransferCode(t, "accept", receiver.token, created.Code).status)

	require.Equal(t, receiver.id, eventOwner(t, multi.ID))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event_pet WHERE event_id = $1`, multi.ID))
	require.Equal(t, []string{cat}, petIDsOf(getEventPets(t, receiver.token, multi.ID).Pets))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event`))
}

// Принятие обрабатывает события группами (по одному запросу на группу):
// несколько общих событий копируются разом, у каждой копии сохраняется
// порядок файлов, вакцинация переключается на копию своего события введения,
// удалённое общее событие лишь отвязывается от питомца, файлы записей
// Ветпаспорта переходят получателю.
func TestPetTransfer_AcceptHandlesEventGroupsInBatches(t *testing.T) {
	resetDB(t)
	sender := newTransferUser(t, "")
	receiver := newTransferUser(t, "")
	cat := createSpeciesPet(t, sender.token, "Барсик", "CAT")
	dog := createSpeciesPet(t, sender.token, "Рекс", "DOG")

	createShared := func() string {
		resp := postEventPets(t, sender.token, []string{cat, dog}, "feeding", feedingValue)
		require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
		var body eventPetsBody
		resp.decode(t, &body)
		return body.ID
	}
	sharedWithFiles := createShared()
	sharedPlain := createShared()
	sharedDeleted := createShared()
	_, err := database.DB.Exec(`UPDATE event SET deleted_at = now() WHERE id = $1`, sharedDeleted)
	require.NoError(t, err)
	moved := createEventReturningID(t, sender.token, cat, "2024-01-01T10:00:00Z", "feeding", feedingValue)

	for position, key := range []string{"events/first.png", "events/second.png"} {
		_, err := database.DB.Exec(`
			INSERT INTO file (id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at)
			VALUES ($1, 'event_file', $2, $3, $4, 'image/png', NULL, $5, now())
		`, uuid.NewString(), sharedWithFiles, sender.id, key, position)
		require.NoError(t, err)
	}
	// Неподтверждённый файл в копию не попадает.
	_, err = database.DB.Exec(`
		INSERT INTO file (id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at)
		VALUES ($1, 'event_file', $2, $3, 'events/pending.png', 'image/png', NULL, 2, NULL)
	`, uuid.NewString(), sharedWithFiles, sender.id)
	require.NoError(t, err)

	var vaccinationID string
	require.NoError(t, database.DB.QueryRow(`
		INSERT INTO vaccination (pet_id, name, administered_date, administered_event_id)
		VALUES ($1, 'Бешенство', '2024-01-01', $2) RETURNING id::text
	`, cat, sharedWithFiles).Scan(&vaccinationID))
	vaccinationFile := insertConfirmedFile(t, "vaccination_file", vaccinationID, sender.id, "vaccinations/cert.png")

	created := createTransfer(t, sender.token, cat)
	require.Equal(t, http.StatusOK, postTransferCode(t, "accept", receiver.token, created.Code).status)

	// Получатель владеет перенесённым событием и двумя копиями общих.
	require.Equal(t, receiver.id, eventOwner(t, moved))
	require.Equal(t, 3, countRows(t, `
		SELECT COUNT(*) FROM event e JOIN event_pet ep ON ep.event_id = e.id
		WHERE ep.pet_id = $1 AND e.user_id = $2 AND e.deleted_at IS NULL
	`, cat, receiver.id))
	var copyWithFiles string
	require.NoError(t, database.DB.QueryRow(`
		SELECT e.id::text FROM event e JOIN event_pet ep ON ep.event_id = e.id
		JOIN file f ON f.owner_type = 'event_file' AND f.owner_id = e.id
		WHERE ep.pet_id = $1 AND e.user_id = $2 GROUP BY e.id
	`, cat, receiver.id).Scan(&copyWithFiles))
	require.NotEqual(t, sharedWithFiles, copyWithFiles)

	// Копия хранит подтверждённые файлы в прежнем порядке, позиции с нуля.
	rows, err := database.DB.Query(`
		SELECT object_key, position, user_id::text FROM file
		WHERE owner_type = 'event_file' AND owner_id = $1 ORDER BY position
	`, copyWithFiles)
	require.NoError(t, err)
	defer rows.Close()
	var keys []string
	for position := 0; rows.Next(); position++ {
		var key, userID string
		var gotPosition int
		require.NoError(t, rows.Scan(&key, &gotPosition, &userID))
		require.Equal(t, position, gotPosition)
		require.Equal(t, receiver.id, userID)
		keys = append(keys, key)
	}
	require.Equal(t, []string{"events/first.png", "events/second.png"}, keys)
	// Файлы исходного события остались у отправителя.
	require.Equal(t, 3, countRows(t, `SELECT COUNT(*) FROM file WHERE owner_type = 'event_file' AND owner_id = $1 AND user_id = $2`, sharedWithFiles, sender.id))

	// Исходные общие события остались у отправителя только с другим питомцем.
	for _, id := range []string{sharedWithFiles, sharedPlain, sharedDeleted} {
		require.Equal(t, sender.id, eventOwner(t, id))
		require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM event_pet WHERE event_id = $1 AND pet_id = $2`, id, cat))
		require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event_pet WHERE event_id = $1 AND pet_id = $2`, id, dog))
	}
	// Четыре исходных события плюс две копии; удалённое общее не копируется.
	require.Equal(t, 6, countRows(t, `SELECT COUNT(*) FROM event`))

	// Вакцинация ссылается на копию события введения.
	var administered string
	require.NoError(t, database.DB.QueryRow(`SELECT administered_event_id::text FROM vaccination WHERE id = $1`, vaccinationID).Scan(&administered))
	require.Equal(t, copyWithFiles, administered)

	// Файл записи Ветпаспорта переходит получателю.
	require.Equal(t, receiver.id, fileUserID(t, vaccinationFile))
}

func TestPetTransfer_PetSoftDeleteCancelsPendingTransfer(t *testing.T) {
	resetDB(t)
	sender := newTransferUser(t, "")
	receiver := newTransferUser(t, "")
	deleted := createSpeciesPet(t, sender.token, "Барсик", "CAT")
	viaPut := createSpeciesPet(t, sender.token, "Мурка", "CAT")

	first := createTransfer(t, sender.token, deleted)
	second := createTransfer(t, sender.token, viaPut)

	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/pet/"+deleted, nil, sender.token).status)
	require.Equal(t, "cancelled", transferStatus(t, first.ID))
	require.Equal(t, "pending", transferStatus(t, second.ID))
	require.Equal(t, http.StatusNotFound, postTransferCode(t, "preview", receiver.token, first.Code).status)

	put := doRequest(t, http.MethodPut, "/pet/"+viaPut, map[string]any{"is_deleted": true}, sender.token)
	require.Equalf(t, http.StatusNoContent, put.status, "%s", put.body)
	require.Equal(t, "cancelled", transferStatus(t, second.ID))
	require.Equal(t, http.StatusNotFound, postTransferCode(t, "accept", receiver.token, second.Code).status)

	// История сохраняется.
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM pet_transfer`))
}

func TestPetTransfer_AccountDeletionCancelsPendingTransferAndKeepsHistory(t *testing.T) {
	resetDB(t)
	sender := newTransferUser(t, "Анна")
	receiver := newTransferUser(t, "")
	petID := createSpeciesPet(t, sender.token, "Барсик", "CAT")
	created := createTransfer(t, sender.token, petID)

	del := doRequest(t, http.MethodDelete, "/profile", nil, sender.token)
	require.Equal(t, http.StatusNoContent, del.status)

	require.Equal(t, "cancelled", transferStatus(t, created.ID))
	var fromNull bool
	var name string
	require.NoError(t, database.DB.QueryRow(`SELECT from_user_id IS NULL, pet_name FROM pet_transfer WHERE id = $1`, created.ID).Scan(&fromNull, &name))
	require.True(t, fromNull)
	require.Equal(t, "Барсик", name)
	require.Equal(t, http.StatusNotFound, postTransferCode(t, "preview", receiver.token, created.Code).status)
}

func TestPetTransfer_ExpireStaleCleanup(t *testing.T) {
	resetDB(t)
	owner := newTransferUser(t, "")
	stale := createSpeciesPet(t, owner.token, "Барсик", "CAT")
	fresh := createSpeciesPet(t, owner.token, "Мурка", "CAT")
	staleTransfer := createTransfer(t, owner.token, stale)
	freshTransfer := createTransfer(t, owner.token, fresh)
	_, err := database.DB.Exec(`UPDATE pet_transfer SET expires_at = now() - interval '1 hour' WHERE id = $1`, staleTransfer.ID)
	require.NoError(t, err)

	expired, err := database.ExpireStalePetTransfers()
	require.NoError(t, err)
	require.Equal(t, int64(1), expired)
	require.Equal(t, "expired", transferStatus(t, staleTransfer.ID))
	require.Equal(t, "pending", transferStatus(t, freshTransfer.ID))
}
