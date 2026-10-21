package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"myauthservice/database"
	"myauthservice/openapi"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Доступ к питомцу (см. database/pet_access.go): владелец выдаёт другому
// пользователю роль full или view приглашением с одноразовым кодом; получатель
// по коду просматривает питомцев и принимает либо отклоняет приглашение.
// Формат кода и ограничение подбора общие с передачей питомца
// (handlers/pet_transfer.go).

// petAccessInviteCodeAttempts — сколько раз повторяется создание приглашения
// при совпадении сгенерированного кода с кодом другого активного приглашения
// либо передачи.
const petAccessInviteCodeAttempts = 5

// isGrantRole сообщает, что role — роль, которую владелец может выдать.
func isGrantRole(role string) bool {
	return role == database.PetRoleFull || role == database.PetRoleView
}

// PetAccessHandler маршрутизирует /pet/{id}/access... и
// /pet/{id}/access-invites...; segments — сегменты пути после /pet/ (первый —
// id питомца), вызывается, только если второй сегмент access либо
// access-invites.
func PetAccessHandler(w http.ResponseWriter, r *http.Request, segments []string) {
	petID, err := uuid.Parse(segments[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный id питомца")
		return
	}

	switch {
	case len(segments) == 2 && segments[1] == "access-invites":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
			return
		}
		createPetAccessInviteHandler(w, r, petID)
	case len(segments) == 3 && segments[1] == "access-invites":
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
			return
		}
		inviteID, err := uuid.Parse(segments[2])
		if err != nil {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный id приглашения")
			return
		}
		cancelPetAccessInviteHandler(w, r, petID, inviteID)
	case len(segments) == 2 && segments[1] == "access":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
			return
		}
		listPetAccessHandler(w, r, petID)
	case len(segments) == 3 && segments[1] == "access" && segments[2] == "me":
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
			return
		}
		leavePetAccessHandler(w, r, petID)
	case len(segments) == 3 && segments[1] == "access":
		memberID, err := uuid.Parse(segments[2])
		if err != nil {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный id участника")
			return
		}
		switch r.Method {
		case http.MethodPut:
			updatePetAccessRoleHandler(w, r, petID, memberID)
		case http.MethodDelete:
			revokePetAccessHandler(w, r, petID, memberID)
		default:
			writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		}
	default:
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Не найдено")
	}
}

// writePetAccessInvite отвечает приглашением владельца.
func writePetAccessInvite(w http.ResponseWriter, status int, invite database.PetAccessInvite) {
	writeJSON(w, status, petAccessInviteResponse(invite))
}

func petAccessInviteResponse(invite database.PetAccessInvite) openapi.PetAccessInviteResponse {
	petIDs := make([]openapi_types.UUID, 0, len(invite.PetIDs))
	for _, id := range invite.PetIDs {
		petIDs = append(petIDs, openapi_types.UUID(id))
	}
	return openapi.PetAccessInviteResponse{
		Id:        openapi_types.UUID(invite.ID),
		Code:      invite.Code,
		Role:      openapi.PetAccessGrantRoleEnum(invite.Role),
		PetIds:    petIDs,
		ExpiresAt: invite.ExpiresAt,
	}
}

// createPetAccessInviteHandler обрабатывает POST /pet/{id}/access-invites.
func createPetAccessInviteHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, ok := resolvePetRole(w, petID, userID, petNeedOwner); !ok {
		return
	}

	var req struct {
		Role   string    `json:"role"`
		PetIDs *[]string `json:"pet_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	if !isGrantRole(req.Role) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле role должно быть full или view")
		return
	}

	petIDs := []uuid.UUID{petID}
	if req.PetIDs != nil {
		if len(*req.PetIDs) > database.PetAccessMaxInvitePets {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "В приглашении слишком много питомцев")
			return
		}
		petIDs = petIDs[:0]
		seen := make(map[uuid.UUID]bool, len(*req.PetIDs))
		for _, raw := range *req.PetIDs {
			id, err := uuid.Parse(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный ID питомца в pet_ids")
				return
			}
			if seen[id] {
				writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле pet_ids не должно содержать повторяющихся питомцев")
				return
			}
			seen[id] = true
			petIDs = append(petIDs, id)
		}
		if !seen[petID] {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле pet_ids должно содержать питомца из пути запроса")
			return
		}
	}

	for attempt := 0; attempt < petAccessInviteCodeAttempts; attempt++ {
		code, err := generatePetCode(petCodePrefixAccess)
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать код приглашения")
			return
		}
		invite, err := database.CreatePetAccessInvite(userID, petIDs, req.Role, code)
		if err == nil {
			writePetAccessInvite(w, http.StatusCreated, *invite)
			return
		}
		if errors.Is(err, database.ErrAccessCodeCollision) {
			continue
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Питомец не найден")
			return
		}
		if errors.Is(err, database.ErrAccessLimitReached) {
			writeError(w, http.StatusConflict, openapi.ACCESSLIMITREACHED, "Достигнут лимит участников у питомца")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать приглашение")
		return
	}
	writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать уникальный код приглашения")
}

// listPetAccessHandler обрабатывает GET /pet/{id}/access.
func listPetAccessHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, ok := resolvePetRole(w, petID, userID, petNeedOwner); !ok {
		return
	}

	members, invites, err := database.ListPetAccess(petID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения участников")
		return
	}

	response := openapi.PetAccessResponse{
		Members: make([]openapi.PetAccessMember, 0, len(members)),
		Invites: make([]openapi.PetAccessInviteResponse, 0, len(invites)),
	}
	for _, member := range members {
		response.Members = append(response.Members, openapi.PetAccessMember{
			UserId:    openapi_types.UUID(member.UserID),
			Name:      member.Name,
			Role:      openapi.PetAccessGrantRoleEnum(member.Role),
			GrantedAt: member.GrantedAt,
		})
	}
	for _, invite := range invites {
		response.Invites = append(response.Invites, petAccessInviteResponse(invite))
	}
	writeJSON(w, http.StatusOK, response)
}

// updatePetAccessRoleHandler обрабатывает PUT /pet/{id}/access/{user_id}.
func updatePetAccessRoleHandler(w http.ResponseWriter, r *http.Request, petID, memberID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, ok := resolvePetRole(w, petID, userID, petNeedOwner); !ok {
		return
	}

	var req openapi.PetAccessUpdateRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	if !isGrantRole(string(req.Role)) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле role должно быть full или view")
		return
	}

	if err := database.UpdatePetAccessRole(petID, memberID.String(), string(req.Role)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Участник не найден")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось изменить роль")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// revokePetAccessHandler обрабатывает DELETE /pet/{id}/access/{user_id}.
func revokePetAccessHandler(w http.ResponseWriter, r *http.Request, petID, memberID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, ok := resolvePetRole(w, petID, userID, petNeedOwner); !ok {
		return
	}

	if err := database.RevokePetAccess(petID, memberID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Участник не найден")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось отозвать доступ")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cancelPetAccessInviteHandler обрабатывает
// DELETE /pet/{id}/access-invites/{invite_id}.
func cancelPetAccessInviteHandler(w http.ResponseWriter, r *http.Request, petID, inviteID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, ok := resolvePetRole(w, petID, userID, petNeedOwner); !ok {
		return
	}

	if err := database.CancelPetAccessInvite(petID, inviteID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Приглашение не найдено")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось отменить приглашение")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// leavePetAccessHandler обрабатывает DELETE /pet/{id}/access/me: участник
// отказывается от собственного доступа. Владельцу отказ недоступен (403).
func leavePetAccessHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	role, err := database.GetPetRole(petID, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Доступ не найден")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения питомца")
		return
	}
	if role == database.PetRoleOwner {
		writeForbidden(w)
		return
	}

	if err := database.LeavePetAccess(petID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Доступ не найден")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось отказаться от доступа")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePetAccessCodeError пишет ответ по ошибке операции с кодом
// приглашения.
func handlePetAccessCodeError(w http.ResponseWriter, userID string, err error, internalMessage string) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeInvalidCode(w, userID, "Код приглашения недействителен")
	case errors.Is(err, database.ErrAccessOwnPet):
		writeError(w, http.StatusConflict, openapi.ACCESSOWNPET, "Нельзя принять или отклонить собственное приглашение")
	case errors.Is(err, database.ErrAccessAlreadyGranted):
		writeError(w, http.StatusConflict, openapi.ACCESSALREADYGRANTED, "Доступ ко всем питомцам приглашения уже есть")
	case errors.Is(err, database.ErrAccessLimitReached):
		writeError(w, http.StatusConflict, openapi.ACCESSLIMITREACHED, "Достигнут лимит участников у питомцев приглашения")
	default:
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, internalMessage)
	}
}

// previewPetAccessInvite пишет предпросмотр кода приглашения (ветка типа
// access в POST /pet-codes/preview).
func previewPetAccessInvite(w http.ResponseWriter, r *http.Request, userID, code string) {
	preview, err := database.PreviewPetAccessInvite(code, userID)
	if err != nil {
		handlePetAccessCodeError(w, userID, err, "Ошибка поиска приглашения")
		return
	}

	ownerName, err := database.GetUserDisplayName(preview.FromUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения владельца")
		return
	}

	petIDs := make([]uuid.UUID, 0, len(preview.Pets))
	for _, pet := range preview.Pets {
		petIDs = append(petIDs, pet.ID)
	}
	photos, err := database.GetConfirmedFilesForOwners(petPhotoOwnerType, petIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения фотографий питомцев")
		return
	}
	if len(photos) > 0 && storage == nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Хранилище файлов не сконфигурировано")
		return
	}

	response := openapi.PetAccessInvitePreviewResponse{
		Pets:      make([]openapi.PetAccessInvitePreviewPet, 0, len(preview.Pets)),
		Role:      openapi.PetAccessGrantRoleEnum(preview.Role),
		OwnerName: ownerName,
		ExpiresAt: preview.ExpiresAt,
	}
	for _, pet := range preview.Pets {
		item := openapi.PetAccessInvitePreviewPet{Name: pet.Name, Species: pet.Species}
		if pet.Breed.Valid {
			item.Breed = pet.Breed.String
		}
		if pet.Gender.Valid {
			gender := openapi.GetGenderEnum(pet.Gender.String)
			item.Gender = &gender
		}
		if pet.BirthDate.Valid {
			item.BirthDate = &openapi_types.Date{Time: pet.BirthDate.Time}
		}
		if photo, found := photos[pet.ID]; found {
			signed, err := storage.PresignGetURL(r.Context(), photo.ObjectKey)
			if err != nil {
				writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось подписать ссылку на фотографию")
				return
			}
			item.PhotoUrl = &signed
		}
		response.Pets = append(response.Pets, item)
	}
	writeJSON(w, http.StatusOK, openapi.PetCodePreviewResponse{
		Type:   openapi.PetCodeTypeEnum("access"),
		Access: &response,
	})
}

// PetAccessInviteDeclineHandler обрабатывает POST /pet-access-invites/decline.
func PetAccessInviteDeclineHandler(w http.ResponseWriter, r *http.Request) {
	userID, code, ok := readCodeRequest(w, r)
	if !ok {
		return
	}

	if err := database.DeclinePetAccessInvite(code, userID); err != nil {
		handlePetAccessCodeError(w, userID, err, "Ошибка отклонения приглашения")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PetAccessInviteAcceptHandler обрабатывает POST /pet-access-invites/accept.
func PetAccessInviteAcceptHandler(w http.ResponseWriter, r *http.Request) {
	userID, code, ok := readCodeRequest(w, r)
	if !ok {
		return
	}

	granted, err := database.AcceptPetAccessInvite(code, userID)
	if err != nil {
		handlePetAccessCodeError(w, userID, err, "Ошибка принятия приглашения")
		return
	}

	petIDs := make([]openapi_types.UUID, 0, len(granted))
	for _, id := range granted {
		petIDs = append(petIDs, openapi_types.UUID(id))
	}
	writeJSON(w, http.StatusOK, openapi.PetAccessInviteAcceptResponse{PetIds: petIDs})
}

// PetAccessNoticeHandler обрабатывает DELETE /pet-access-notices/{pet_id}:
// отмечает уведомление о потере доступа просмотренным. Отсутствие уведомления
// не ошибка; чужие уведомления недоступны ни по какому pet_id.
func PetAccessNoticeHandler(w http.ResponseWriter, r *http.Request) {
	segments := pathSegments(r, "/pet-access-notices/")
	if len(segments) != 1 {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Не найдено")
		return
	}
	petID, err := uuid.Parse(segments[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный id питомца")
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	if err := database.DeletePetAccessNotice(userID, petID); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось удалить уведомление")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
