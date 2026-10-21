package handlers

import (
	"database/sql"
	"errors"
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

// petNeed — какая роль пользователя у питомца нужна операции (см. «Общие
// требования: Доступ к питомцу», матрица прав).
type petNeed int

const (
	// petNeedRead — чтение: достаточно любой роли (owner, full, view).
	petNeedRead petNeed = iota
	// petNeedWrite — изменение питомца и его данных: owner или full.
	petNeedWrite
	// petNeedOwner — операция владельца: передача, удаление питомца, выдача
	// доступа и управление им.
	petNeedOwner
)

// roleSatisfies сообщает, достаточно ли роли для операции.
func roleSatisfies(role string, need petNeed) bool {
	switch need {
	case petNeedOwner:
		return role == database.PetRoleOwner
	case petNeedWrite:
		return database.CanWriteRole(role)
	default:
		return role != ""
	}
}

// writeForbidden отвечает 403: доступ к ресурсу есть, но роли недостаточно
// для операции.
func writeForbidden(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, openapi.FORBIDDEN, "Недостаточно прав для этой операции")
}

// resolvePetRole определяет роль userID у питомца petID для операции need:
// питомец не найден, удалён либо у пользователя нет доступа — 404, доступ
// есть, но роли недостаточно — 403. При отказе сама пишет ответ и
// возвращает ok=false.
func resolvePetRole(w http.ResponseWriter, petID uuid.UUID, userID string, need petNeed) (role string, ok bool) {
	role, err := database.GetPetRole(petID, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, fmt.Sprintf("Питомец %s не найден", petID))
			return "", false
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения питомца")
		return "", false
	}
	if !roleSatisfies(role, need) {
		writeForbidden(w)
		return "", false
	}
	return role, true
}

// resolveAccessiblePet загружает питомца petID (вместе с ролью access и
// признаком shared) и проверяет, что роль userID удовлетворяет need: питомец
// не найден, удалён либо недоступен — 404, роли недостаточно — 403. При
// отказе сама пишет ответ и возвращает ok=false.
func resolveAccessiblePet(w http.ResponseWriter, petID uuid.UUID, userID string, need petNeed) (*models.PetIdResponse, bool) {
	pet, err := database.GetPetForUser(petID, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, fmt.Sprintf("Питомец %s не найден", petID))
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения питомца")
		return nil, false
	}
	if !roleSatisfies(pet.Access, need) {
		writeForbidden(w)
		return nil, false
	}
	return pet, true
}

// resolvePetChildAccess проверяет доступ userID к питомцу сущности
// Ветпаспорта по роли (need) и пишет ответ при отказе: нет доступа — 404 с
// notFoundMessage, роли недостаточно — 403. В отличие от resolveAccessiblePet
// мягко удалённый питомец остаётся доступным владельцу (очистка истории), а
// участнику скрыт.
func resolvePetChildAccess(w http.ResponseWriter, petID uuid.UUID, userID string, need petNeed, notFoundMessage string) bool {
	role, err := database.GetPetRoleIncludingDeleted(petID, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, notFoundMessage)
			return false
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return false
	}
	if !roleSatisfies(role, need) {
		writeForbidden(w)
		return false
	}
	return true
}

// resolveAccessibleEvent находит событие eventID, видимое userID: у него есть
// видимый питомец и пользователю доступны все видимые питомцы (автор события
// доступ не определяет). forWrite=true дополнительно требует роль owner или
// full у каждого видимого питомца (иначе 403). allowNoVisiblePets=true
// допускает событие без видимых питомцев (удаление очищает историю мягко
// удалённого питомца). При ошибке/отказе сама пишет 404/403/500.
func resolveAccessibleEvent(w http.ResponseWriter, eventID uuid.UUID, userID string, forWrite bool, allowNoVisiblePets bool) (*database.EventFull, bool) {
	var eventFull *database.EventFull
	var err error
	if forWrite {
		eventFull, err = database.GetEventForWriteWith(database.DB, eventID, userID, false, allowNoVisiblePets)
	} else {
		eventFull, err = database.GetEventForUserWith(database.DB, eventID, userID, false, allowNoVisiblePets)
	}
	if err != nil {
		if errors.Is(err, database.ErrForbidden) {
			writeForbidden(w)
			return nil, false
		}
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Событие не найдено")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения события")
		return nil, false
	}
	return eventFull, true
}
