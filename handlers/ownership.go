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

// resolveOwnedPet проверяет, что питомец petID существует, не удалён и
// принадлежит userID. При ошибке/отсутствии сама пишет 404/500.
func resolveOwnedPet(w http.ResponseWriter, petID uuid.UUID, userID string) (*models.PetIdResponse, bool) {
	pet, err := database.GetPetByIDAndUserID(petID, userID)
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

// resolveOwnedEvent находит событие eventID и проверяет, что оно
// принадлежит userID (event.user_id) — по владельцу записи, а не по питомцу.
// requireVisiblePet=true дополнительно требует хотя бы одного не мягко
// удалённого питомца: запись без видимых питомцев нигде не показывается.
// При ошибке/отсутствии сама пишет 404/500.
func resolveOwnedEvent(w http.ResponseWriter, eventID uuid.UUID, userID string, requireVisiblePet bool) (*database.EventFull, bool) {
	eventFull, err := database.GetEventForUserWith(database.DB, eventID, userID, false)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Событие не найдено")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения события")
		return nil, false
	}
	if requireVisiblePet && len(eventFull.Pets) == 0 {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Событие не найдено")
		return nil, false
	}
	return eventFull, true
}
