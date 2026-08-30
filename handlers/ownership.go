package handlers

import (
	"database/sql"
	"fmt"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// pathSegments возвращает сегменты пути после prefix, разделённые "/".
// Общий разбор путей для /pet/{id}[/events] и /events/{id} — единственное
// место, где ServeMux-путь превращается в id/сегменты.
func pathSegments(r *http.Request, prefix string) []string {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if rest == "" {
		return nil
	}
	return strings.Split(rest, "/")
}

func parsePetIDFromPath(r *http.Request) (uuid.UUID, error) {
	segments := pathSegments(r, "/pet/")
	if len(segments) == 0 {
		return uuid.Nil, fmt.Errorf("пустой id питомца")
	}
	return uuid.Parse(segments[0])
}

func parseEventIDFromPath(r *http.Request) (uuid.UUID, error) {
	segments := pathSegments(r, "/events/")
	if len(segments) == 0 {
		return uuid.Nil, fmt.Errorf("пустой id события")
	}
	return uuid.Parse(segments[0])
}

// authProfile проверяет авторизацию и находит profile_id вызывающего
// пользователя. При ошибке сама пишет ответ и возвращает ok=false.
func authProfile(w http.ResponseWriter, r *http.Request) (profileID uuid.UUID, ok bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return uuid.Nil, false
	}

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Профиль не найден")
			return uuid.Nil, false
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения профиля")
		return uuid.Nil, false
	}

	return profileID, true
}

// resolveOwnedPet проверяет, что питомец petID существует, не удалён и
// принадлежит profileID. При ошибке/отсутствии сама пишет 404/500.
func resolveOwnedPet(w http.ResponseWriter, petID, profileID uuid.UUID) (*models.PetIdResponse, bool) {
	pet, err := database.GetPetByIDAndProfileID(petID, profileID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, fmt.Sprintf("Питомец %s не найден", petID))
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения питомца")
		return nil, false
	}
	return pet, true
}

// resolveOwnedEvent находит событие eventID и проверяет, что его питомец
// принадлежит profileID. При ошибке/отсутствии сама пишет 404/500.
func resolveOwnedEvent(w http.ResponseWriter, eventID, profileID uuid.UUID) (eventDB *models.EventDB, petID uuid.UUID, petName string, ok bool) {
	eventDB, petID, petName, err := database.GetEventByID(eventID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Событие не найдено")
			return nil, uuid.Nil, "", false
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения события")
		return nil, uuid.Nil, "", false
	}

	belongs, err := database.CheckPetBelongsToProfile(petID, profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки принадлежности питомца")
		return nil, uuid.Nil, "", false
	}
	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Событие не найдено")
		return nil, uuid.Nil, "", false
	}

	return eventDB, petID, petName, true
}
