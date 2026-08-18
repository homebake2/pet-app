package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

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
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/pet/"), "/")
	parts := strings.Split(rest, "/")

	if len(parts) == 2 && parts[1] == "events" {
		petID, err := uuid.Parse(parts[0])
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

func parsePetIDFromPath(r *http.Request) (uuid.UUID, error) {
	petIDStr := strings.TrimPrefix(r.URL.Path, "/pet/")
	return uuid.Parse(petIDStr)
}

// requireLanguageCode проверяет обязательный заголовок LanguageCode (ru|en),
// как того требует components.parameters.LanguageCode из open-api/spec.json.
func requireLanguageCode(w http.ResponseWriter, r *http.Request) bool {
	lang := r.Header.Get("LanguageCode")
	if lang != "ru" && lang != "en" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Заголовок LanguageCode обязателен и должен быть ru или en")
		return false
	}
	return true
}

// CreatePetHandler обрабатывает POST /pet
func CreatePetHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req models.CreatePetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Species) == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поля name и species обязательны")
		return
	}

	if req.Icon != nil && !models.IsValidIcon(*req.Icon) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение icon")
		return
	}

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Профиль пользователя не найден")
		return
	}

	newPetID, err := database.InsertPet(profileID, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать питомца")
		return
	}

	pet, err := database.GetPetByIDAndProfileID(newPetID, profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Питомец создан, но не удалось получить его данные")
		return
	}

	writeJSON(w, http.StatusCreated, pet)
}

// GetAllPetHandler обрабатывает GET /pet
func GetAllPetHandler(w http.ResponseWriter, r *http.Request) {
	if !requireLanguageCode(w, r) {
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось получить профиль")
		return
	}

	pets, err := database.GetPetsByProfileID(profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось получить питомцев")
		return
	}

	response := models.PetResponse{
		Items: make([]models.PetItem, 0, len(pets)),
	}

	for _, pet := range pets {
		icon := "OTHER"

		if pet.Icon.Valid && pet.Icon.String != "" {
			icon = pet.Icon.String
		}

		response.Items = append(response.Items, models.PetItem{
			ID:      pet.ID.String(),
			Name:    pet.Name,
			Breed:   pet.Breed,
			Species: pet.Species,
			Icon:    icon,
		})
	}

	writeJSON(w, http.StatusOK, response)
}

// GetPetHandler обрабатывает GET /pet/{id}
func GetPetHandler(w http.ResponseWriter, r *http.Request) {
	if !requireLanguageCode(w, r) {
		return
	}

	petID, err := parsePetIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id питомца")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения профиля")
		return
	}

	pet, err := database.GetPetByIDAndProfileID(petID, profileID)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, fmt.Sprintf("Питомец %s не найден", petID))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения питомца")
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

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения профиля")
		return
	}

	if _, err := database.GetPetByIDAndProfileID(petID, profileID); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, fmt.Sprintf("Питомец %s не найден", petID))
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения питомца")
		return
	}

	var req models.UpdatePetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный JSON")
		return
	}

	if req.Name == nil || strings.TrimSpace(*req.Name) == "" || req.Species == nil || strings.TrimSpace(*req.Species) == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поля name и species обязательны")
		return
	}

	if req.Icon != nil && !models.IsValidIcon(*req.Icon) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение icon")
		return
	}

	log.Printf("REQ: %+v\n", req)
	if err := database.UpdatePet(petID, profileID, req); err != nil {
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

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения профиля")
		return
	}

	if _, err := database.GetPetByIDAndProfileID(petID, profileID); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, fmt.Sprintf("Питомец %s не найден", petID))
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения питомца")
		return
	}

	if err := database.DeletePet(petID, profileID); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка удаления питомца")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
