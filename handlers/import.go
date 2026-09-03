package handlers

import (
	"encoding/json"
	"myauthservice/database"
	"myauthservice/eventreg"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"strings"
)

// validateImportPet провалидирует один элемент pets[] теми же правилами,
// что и POST /pet (см. CreatePetHandler), плюс обязательность local_id.
func validateImportPet(pet models.ImportLocalDataPet) string {
	if strings.TrimSpace(pet.LocalID) == "" {
		return "Поле local_id обязательно для каждого питомца"
	}

	if strings.TrimSpace(pet.Name) == "" || strings.TrimSpace(pet.Species) == "" {
		return "Поля name и species обязательны для питомца"
	}

	if len(pet.Name) > models.PetNameMaxLen {
		return "Поле name питомца превышает допустимую длину"
	}

	if pet.Notes != nil && len(*pet.Notes) > models.PetNotesMaxLen {
		return "Поле notes питомца превышает допустимую длину"
	}

	if pet.Breed != nil && len(*pet.Breed) > models.PetBreedMaxLen {
		return "Поле breed питомца превышает допустимую длину"
	}

	if pet.Color != nil && len(*pet.Color) > models.PetColorMaxLen {
		return "Поле color питомца превышает допустимую длину"
	}

	if pet.Icon != nil && !models.IsValidIcon(*pet.Icon) {
		return "Некорректное значение icon у питомца"
	}

	if pet.Gender != nil && !models.IsValidGender(*pet.Gender) {
		return "Некорректное значение gender у питомца"
	}

	if pet.Habitation != nil && !models.IsValidHabitation(*pet.Habitation) {
		return "Некорректное значение habitation у питомца"
	}

	if pet.BirthDate != nil && !isValidBirthDate(*pet.BirthDate) {
		return "Некорректный формат birth_date у питомца, ожидается YYYY-MM-DD"
	}

	if pet.BirthDate != nil && !isBirthDateNotTooOld(*pet.BirthDate) {
		return "Поле birth_date питомца не может быть раньше, чем 500 лет назад"
	}

	return ""
}

// validateImportEvent провалидирует один элемент events[] теми же
// правилами, что и POST /events (см. CreateEventHandler), кроме проверки
// pet_local_id — она выполняется отдельно, после того как собран набор
// local_id всех питомцев запроса.
func validateImportEvent(event models.ImportLocalDataEvent) string {
	if strings.TrimSpace(event.PetLocalID) == "" {
		return "Поле pet_local_id обязательно для каждого события"
	}

	if event.Date == "" || event.Type == "" || len(event.Value) == 0 {
		return "Обязательные поля события не заполнены"
	}

	if !eventreg.IsValidType(event.Type) {
		return "Некорректное значение type у события"
	}

	if _, err := parseEventDate(event.Date); err != nil {
		return "Некорректный формат даты события"
	}

	if msg := validateEventValue(event.Type, event.Value); msg != "" {
		return msg
	}

	if !validateNotesLength(event.Notes) {
		return "Поле notes события не должно превышать 500 символов"
	}

	return ""
}

// validateImportProfile провалидирует profile теми же правилами, что и
// POST /profile (см. CreateProfileHandler).
func validateImportProfile(profile models.ImportLocalDataProfile) string {
	if strings.TrimSpace(profile.FirstName) == "" {
		return "Поле first_name профиля обязательно"
	}

	if msg := validateProfileNameLength("first_name", profile.FirstName); msg != "" {
		return msg
	}
	if profile.MiddleName != nil {
		if msg := validateProfileNameLength("middle_name", *profile.MiddleName); msg != "" {
			return msg
		}
	}
	if profile.LastName != nil {
		if msg := validateProfileNameLength("last_name", *profile.LastName); msg != "" {
			return msg
		}
	}
	if profile.Email != nil {
		if msg := validateProfileEmail(*profile.Email); msg != "" {
			return msg
		}
	}
	if profile.Phone != nil {
		if msg := validateProfilePhone(*profile.Phone); msg != "" {
			return msg
		}
	}

	return ""
}

// ImportLocalDataHandler обрабатывает POST /import/local-data — единый
// эндпоинт разового переноса локальных данных (профиль, питомцы, события) в
// аккаунт текущего пользователя (см. "Импорт локальных данных — Backend").
func ImportLocalDataHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" || !isValidUUIDv4(idempotencyKey) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Заголовок Idempotency-Key обязателен и должен быть валидным UUID v4")
		return
	}

	reserved, err := database.ReserveImportIdempotencyKey(userID, idempotencyKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки idempotency key")
		return
	}
	if !reserved {
		result, hasResult, err := database.GetImportResultByIdempotencyKey(userID, idempotencyKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки idempotency key")
			return
		}
		if hasResult {
			writeJSON(w, http.StatusOK, result)
			return
		}
		// result ещё не сохранён: редкая гонка параллельных запросов с
		// одним и тем же ключом (см. CreatePetHandler) — fail open,
		// продолжаем обработку как обычный перенос.
	}

	var req models.ImportLocalDataRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	if req.Pets == nil || req.Events == nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поля pets и events обязательны и не должны быть null")
		return
	}

	localIDs := make(map[string]bool, len(req.Pets))
	for _, pet := range req.Pets {
		if msg := validateImportPet(pet); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
		localIDs[pet.LocalID] = true
	}

	for _, event := range req.Events {
		if msg := validateImportEvent(event); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
		if !localIDs[event.PetLocalID] {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле pet_local_id не совпадает ни с одним local_id питомцев запроса")
			return
		}
	}

	if req.Profile != nil {
		if msg := validateImportProfile(*req.Profile); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
	}

	result, err := database.ImportLocalData(userID, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось выполнить перенос данных")
		return
	}

	if err := database.FinalizeImportIdempotencyKey(userID, idempotencyKey, result); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Перенос выполнен, но не удалось завершить idempotency key")
		return
	}

	writeJSON(w, http.StatusOK, result)
}
