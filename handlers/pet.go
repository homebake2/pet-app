package handlers

import (
	"database/sql"
	"encoding/json"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// petPhotoOwnerType — owner_type, под которым фотография питомца
// зарегистрирована в реестре типов владельцев generic-механизма файлов
// сущностей (см. handlers/files.go, «Фотография питомца — Backend»).
const petPhotoOwnerType = "pet_photo"

// attachPetPhoto заполняет photo_url/photo_file_id детального ответа
// питомца по подтверждённой строке file (кардинальность «ровно 1», см.
// «Фотография питомца — Backend», раздел «Чтение»). Если фотографии нет —
// оба поля остаются nil. Отдельная проверка владения не нужна: она уже
// выполнена при выборке самого питомца (resolveOwnedPet).
func attachPetPhoto(w http.ResponseWriter, r *http.Request, petID uuid.UUID, pet *models.PetIdResponse) bool {
	file, err := database.GetConfirmedFileForOwner(petPhotoOwnerType, petID)
	if err != nil {
		if err == sql.ErrNoRows {
			return true
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения фотографии питомца")
		return false
	}

	if storage == nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Хранилище файлов не сконфигурировано")
		return false
	}

	url, err := storage.PresignGetURL(r.Context(), file.ObjectKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось подписать ссылку на фотографию")
		return false
	}

	fileIDStr := file.ID.String()
	pet.PhotoURL = &url
	pet.PhotoFileID = &fileIDStr
	return true
}

// attachPetPhotos — батч-версия attachPetPhoto для GET /pet: один запрос к
// file под все id питомцев списка вместо N+1 (presign-запросы к S3 не
// требуют сети, поэтому цикл по каждому найденному файлу не создаёт лишних
// обращений к БД).
func attachPetPhotos(w http.ResponseWriter, r *http.Request, petIDs []uuid.UUID, items []models.PetItem) bool {
	files, err := database.GetConfirmedFilesForOwners(petPhotoOwnerType, petIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения фотографий питомцев")
		return false
	}
	if len(files) == 0 {
		return true
	}
	if storage == nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Хранилище файлов не сконфигурировано")
		return false
	}

	for i := range items {
		id, err := uuid.Parse(items[i].ID)
		if err != nil {
			continue
		}
		file, found := files[id]
		if !found {
			continue
		}
		url, err := storage.PresignGetURL(r.Context(), file.ObjectKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось подписать ссылку на фотографию")
			return false
		}
		items[i].PhotoURL = &url
	}
	return true
}

// isValidBirthDate проверяет, что дата рождения питомца в формате "YYYY-MM-DD".
func isValidBirthDate(birthDate string) bool {
	_, err := time.Parse("2006-01-02", birthDate)
	return err == nil
}

// isBirthDateNotTooOld проверяет, что дата рождения питомца не раньше, чем
// 500 лет назад от текущей даты сервера. Вызывается только для уже
// прошедшего проверку формата значения birthDate.
func isBirthDateNotTooOld(birthDate string) bool {
	t, err := time.Parse("2006-01-02", birthDate)
	if err != nil {
		return false
	}
	return !t.Before(time.Now().AddDate(-500, 0, 0))
}

// PetHandler обрабатывает /pet (без id): список и создание.
func PetHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		GetAllPetHandler(w, r)
	case http.MethodPost:
		CreatePetHandler(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

// PetByIDHandler обрабатывает /pet/{id} (получение, изменение, удаление)
// и /pet/{id}/events (получение событий питомца).
func PetByIDHandler(w http.ResponseWriter, r *http.Request) {
	segments := pathSegments(r, "/pet/")

	if len(segments) == 2 && segments[1] == "events" {
		petID, err := uuid.Parse(segments[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id питомца")
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
			return
		}
		GetPetEventsHandler(w, r, petID)
		return
	}

	switch r.Method {
	case http.MethodGet:
		GetPetHandler(w, r)
	case http.MethodPut:
		UpdatePetHandler(w, r)
	case http.MethodDelete:
		DeletePetHandler(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

// CreatePetHandler обрабатывает POST /pet
func CreatePetHandler(w http.ResponseWriter, r *http.Request) {
	var req models.CreatePetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey != "" && !isValidUUIDv4(idempotencyKey) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Заголовок Idempotency-Key должен быть валидным UUID v4")
		return
	}

	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Species) == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поля name и species обязательны")
		return
	}

	if len(req.Name) > models.PetNameMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле name превышает допустимую длину")
		return
	}

	if req.Notes != nil && len(*req.Notes) > models.PetNotesMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле notes превышает допустимую длину")
		return
	}

	if req.Breed != nil && len(*req.Breed) > models.PetBreedMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле breed превышает допустимую длину")
		return
	}

	if req.Color != nil && len(*req.Color) > models.PetColorMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле color превышает допустимую длину")
		return
	}

	if req.Icon != nil && !models.IsValidIcon(*req.Icon) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение icon")
		return
	}

	if req.Gender != nil && !models.IsValidGender(*req.Gender) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение gender")
		return
	}

	if req.Habitation != nil && !models.IsValidHabitation(*req.Habitation) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение habitation")
		return
	}

	if req.BirthDate != nil && !isValidBirthDate(*req.BirthDate) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат birth_date, ожидается YYYY-MM-DD")
		return
	}

	if req.BirthDate != nil && !isBirthDateNotTooOld(*req.BirthDate) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле birth_date не может быть раньше, чем 500 лет назад")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	if idempotencyKey != "" {
		reserved, err := database.ReservePetIdempotencyKey(userID, idempotencyKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки idempotency key")
			return
		}
		if !reserved {
			existingPetID, hasPetID, err := database.GetPetIDByIdempotencyKey(userID, idempotencyKey)
			if err != nil {
				writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки idempotency key")
				return
			}
			if hasPetID {
				pet, err := database.GetPetByIDAndUserID(existingPetID, userID)
				if err != nil {
					writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения питомца")
					return
				}
				writeJSON(w, http.StatusCreated, pet)
				return
			}
			// pet_id IS NULL: редкая гонка параллельных запросов с одним
			// ключом — fail open, продолжаем как обычное создание (см.
			// «Добавление питомца — Backend»).
		}
	}

	newPetID, err := database.InsertPet(userID, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать питомца")
		return
	}

	if idempotencyKey != "" {
		if err := database.FinalizePetIdempotencyKey(userID, idempotencyKey, newPetID); err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Питомец создан, но не удалось завершить idempotency key")
			return
		}
	}

	pet, err := database.GetPetByIDAndUserID(newPetID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Питомец создан, но не удалось получить его данные")
		return
	}

	writeJSON(w, http.StatusCreated, pet)
}

// GetAllPetHandler обрабатывает GET /pet
func GetAllPetHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	pets, err := database.GetPetsByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось получить питомцев")
		return
	}

	response := models.PetResponse{
		Items: make([]models.PetItem, 0, len(pets)),
	}
	petIDs := make([]uuid.UUID, 0, len(pets))

	for _, pet := range pets {
		icon := "OTHER"

		if pet.Icon.Valid && pet.Icon.String != "" {
			icon = pet.Icon.String
		}

		response.Items = append(response.Items, models.PetItem{
			ID:      pet.ID.String(),
			Name:    pet.Name,
			Breed:   pet.Breed.String,
			Species: pet.Species,
			Icon:    icon,
		})
		petIDs = append(petIDs, pet.ID)
	}

	if !attachPetPhotos(w, r, petIDs, response.Items) {
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// GetPetHandler обрабатывает GET /pet/{id}
func GetPetHandler(w http.ResponseWriter, r *http.Request) {
	petID, err := parsePetIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id питомца")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	pet, ok := resolveOwnedPet(w, petID, userID)
	if !ok {
		return
	}

	if !attachPetPhoto(w, r, petID, pet) {
		return
	}

	writeJSON(w, http.StatusOK, pet)
}

func UpdatePetHandler(w http.ResponseWriter, r *http.Request) {
	petID, err := parsePetIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id питомца")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	if _, ok := resolveOwnedPet(w, petID, userID); !ok {
		return
	}

	var req models.UpdatePetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный JSON")
		return
	}

	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле name не может быть пустым")
		return
	}

	if req.Name != nil && len(*req.Name) > models.PetNameMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле name превышает допустимую длину")
		return
	}

	if req.Notes != nil && len(*req.Notes) > models.PetNotesMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле notes превышает допустимую длину")
		return
	}

	if req.Breed != nil && len(*req.Breed) > models.PetBreedMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле breed превышает допустимую длину")
		return
	}

	if req.Color != nil && len(*req.Color) > models.PetColorMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле color превышает допустимую длину")
		return
	}

	if req.Species != nil && strings.TrimSpace(*req.Species) == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле species не может быть пустым")
		return
	}

	if req.Icon != nil && !models.IsValidIcon(*req.Icon) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение icon")
		return
	}

	if req.Gender != nil && !models.IsValidGender(*req.Gender) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение gender")
		return
	}

	if req.Habitation != nil && !models.IsValidHabitation(*req.Habitation) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение habitation")
		return
	}

	if req.BirthDate != nil && !isValidBirthDate(*req.BirthDate) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат birth_date, ожидается YYYY-MM-DD")
		return
	}

	if req.BirthDate != nil && !isBirthDateNotTooOld(*req.BirthDate) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле birth_date не может быть раньше, чем 500 лет назад")
		return
	}

	if err := database.UpdatePet(petID, userID, req); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка обновления питомца")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeletePetHandler обрабатывает DELETE /pet/{id}
func DeletePetHandler(w http.ResponseWriter, r *http.Request) {
	petID, err := parsePetIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id питомца")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	if _, ok := resolveOwnedPet(w, petID, userID); !ok {
		return
	}

	if err := database.DeletePet(petID, userID); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка удаления питомца")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
