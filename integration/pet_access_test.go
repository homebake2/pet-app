//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"

	"myauthservice/database"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Доступ к питомцу: выдача по коду, роли, видимость и изменение записей,
// автор записи, отзыв, отказ, уведомления о потере доступа, передача и
// удаление питомца, удаление аккаунта.

// newAccessUser регистрирует пользователя с собственным адресом клиента
// (X-Forwarded-For): регистрации ограничены тремя в сутки с одного IP, а в
// сценариях доступа участвуют несколько пользователей.
func newAccessUser(t *testing.T, firstName string) transferUser {
	t.Helper()
	login := uniqueLogin(t)
	ip := uuid.New()
	resp := doRequest(t, http.MethodPost, "/auth/register", map[string]string{
		"login":    login,
		"password": "password123",
	}, "", map[string]string{"X-Forwarded-For": fmt.Sprintf("10.%d.%d.%d", ip[0], ip[1], ip[2])})
	require.Equalf(t, http.StatusOK, resp.status, "регистрация не удалась: %s", resp.body)
	var tokens authTokens
	resp.decode(t, &tokens)
	if firstName != "" {
		createProfile(t, tokens.AccessToken, firstName)
	}
	var id string
	require.NoError(t, database.DB.QueryRow(`SELECT id::text FROM users WHERE login = $1`, login).Scan(&id))
	return transferUser{login: login, token: tokens.AccessToken, id: id}
}

type accessInviteBody struct {
	ID        string   `json:"id"`
	Code      string   `json:"code"`
	Role      string   `json:"role"`
	PetIDs    []string `json:"pet_ids"`
	ExpiresAt string   `json:"expires_at"`
}

func createAccessInvite(t *testing.T, token, petID, role string, petIDs []string) accessInviteBody {
	t.Helper()
	body := map[string]any{"role": role}
	if petIDs != nil {
		body["pet_ids"] = petIDs
	}
	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/access-invites", body, token)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
	var invite accessInviteBody
	resp.decode(t, &invite)
	return invite
}

// grantAccess создаёт приглашение владельца и принимает его участником.
func grantAccess(t *testing.T, owner, member transferUser, petID, role string) {
	t.Helper()
	invite := createAccessInvite(t, owner.token, petID, role, nil)
	resp := doRequest(t, http.MethodPost, "/pet-access-invites/accept", map[string]string{"code": invite.Code}, member.token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
}

func petAccessOf(t *testing.T, token, petID string) (int, string, bool) {
	t.Helper()
	resp := doRequest(t, http.MethodGet, "/pet/"+petID, nil, token)
	if resp.status != http.StatusOK {
		return resp.status, "", false
	}
	var pet struct {
		Access string `json:"access"`
		Shared bool   `json:"shared"`
	}
	resp.decode(t, &pet)
	return resp.status, pet.Access, pet.Shared
}

type petListBody struct {
	Items []struct {
		ID     string `json:"id"`
		Access string `json:"access"`
	} `json:"items"`
	AccessLost []struct {
		PetID  string `json:"pet_id"`
		Reason string `json:"reason"`
	} `json:"access_lost"`
}

func listPets(t *testing.T, token string) petListBody {
	t.Helper()
	resp := doRequest(t, http.MethodGet, "/pet", nil, token)
	require.Equal(t, http.StatusOK, resp.status)
	var list petListBody
	resp.decode(t, &list)
	return list
}

func TestPetAccess_InviteAcceptAndRoles(t *testing.T) {
	owner := newAccessUser(t, "Owner")
	member := newAccessUser(t, "Member")
	petID := createPet(t, owner.token, "Барсик")

	invite := createAccessInvite(t, owner.token, petID, "view", nil)
	require.Equal(t, []string{petID}, invite.PetIDs)

	// Предпросмотр ничего не меняет и показывает владельца.
	resp := doRequest(t, http.MethodPost, "/pet-codes/preview", map[string]string{"code": invite.Code}, member.token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var preview struct {
		Type   string `json:"type"`
		Access struct {
			OwnerName string `json:"owner_name"`
			Role      string `json:"role"`
		} `json:"access"`
	}
	resp.decode(t, &preview)
	require.Equal(t, "access", preview.Type)
	require.Equal(t, "Owner", preview.Access.OwnerName)
	require.Equal(t, "view", preview.Access.Role)

	// Владелец не может принять собственный код.
	resp = doRequest(t, http.MethodPost, "/pet-access-invites/accept", map[string]string{"code": invite.Code}, owner.token)
	require.Equal(t, http.StatusConflict, resp.status)
	require.Equal(t, "ACCESS_OWN_PET", errorCodeOf(t, resp))

	// До принятия питомца не видно.
	status, _, _ := petAccessOf(t, member.token, petID)
	require.Equal(t, http.StatusNotFound, status)

	resp = doRequest(t, http.MethodPost, "/pet-access-invites/accept", map[string]string{"code": invite.Code}, member.token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	// Повторное использование кода — 404.
	resp = doRequest(t, http.MethodPost, "/pet-access-invites/accept", map[string]string{"code": invite.Code}, member.token)
	require.Equal(t, http.StatusNotFound, resp.status)

	status, access, shared := petAccessOf(t, member.token, petID)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "view", access)
	require.True(t, shared)
	_, access, shared = petAccessOf(t, owner.token, petID)
	require.Equal(t, "owner", access)
	require.True(t, shared)

	list := listPets(t, member.token)
	require.Len(t, list.Items, 1)
	require.Equal(t, "view", list.Items[0].Access)

	// view: чтение можно, запись и операции владельца — 403.
	resp = doRequest(t, http.MethodPut, "/pet/"+petID, map[string]any{"name": "Новое"}, member.token)
	require.Equal(t, http.StatusForbidden, resp.status)
	resp = doRequest(t, http.MethodDelete, "/pet/"+petID, nil, member.token)
	require.Equal(t, http.StatusForbidden, resp.status)
	resp = doRequest(t, http.MethodPost, "/pet/"+petID+"/transfer", nil, member.token)
	require.Equal(t, http.StatusForbidden, resp.status)
	resp = doRequest(t, http.MethodGet, "/pet/"+petID+"/access", nil, member.token)
	require.Equal(t, http.StatusForbidden, resp.status)
	resp = doRequest(t, http.MethodPost, "/pet/"+petID+"/vet-visits", map[string]any{"visit_date": "2024-01-01", "reason": "Осмотр"}, member.token)
	require.Equal(t, http.StatusForbidden, resp.status)
	resp = doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_ids": []string{petID}, "date": "2024-01-01T10:00:00Z", "type": "weight", "value": map[string]any{"amount": 4.2},
	}, member.token)
	require.Equal(t, http.StatusForbidden, resp.status)
	resp = doRequest(t, http.MethodGet, "/pet/"+petID+"/vet-visits", nil, member.token)
	require.Equal(t, http.StatusOK, resp.status)

	// Владелец повышает роль до full.
	resp = doRequest(t, http.MethodPut, "/pet/"+petID+"/access/"+member.id, map[string]string{"role": "full"}, owner.token)
	require.Equal(t, http.StatusNoContent, resp.status)
	resp = doRequest(t, http.MethodPut, "/pet/"+petID, map[string]any{"name": "Новое"}, member.token)
	require.Equalf(t, http.StatusNoContent, resp.status, "%s", resp.body)
	resp = doRequest(t, http.MethodPut, "/pet/"+petID, map[string]any{"is_deleted": true}, member.token)
	require.Equal(t, http.StatusForbidden, resp.status)

	resp = doRequest(t, http.MethodGet, "/pet/"+petID+"/access", nil, owner.token)
	require.Equal(t, http.StatusOK, resp.status)
	var accessList struct {
		Members []struct {
			UserID string `json:"user_id"`
			Name   string `json:"name"`
			Role   string `json:"role"`
		} `json:"members"`
		Invites []accessInviteBody `json:"invites"`
	}
	resp.decode(t, &accessList)
	require.Len(t, accessList.Members, 1)
	require.Equal(t, "Member", accessList.Members[0].Name)
	require.Equal(t, "full", accessList.Members[0].Role)
	require.Empty(t, accessList.Invites)
}

func TestPetAccess_DeclineAndCancelInvite(t *testing.T) {
	owner := newAccessUser(t, "Owner")
	other := newAccessUser(t, "Other")
	petID := createPet(t, owner.token, "Барсик")

	invite := createAccessInvite(t, owner.token, petID, "full", nil)
	resp := doRequest(t, http.MethodPost, "/pet-access-invites/decline", map[string]string{"code": invite.Code}, other.token)
	require.Equal(t, http.StatusNoContent, resp.status)
	resp = doRequest(t, http.MethodPost, "/pet-access-invites/accept", map[string]string{"code": invite.Code}, other.token)
	require.Equal(t, http.StatusNotFound, resp.status)

	invite = createAccessInvite(t, owner.token, petID, "full", nil)
	resp = doRequest(t, http.MethodGet, "/pet/"+petID+"/access", nil, owner.token)
	var listed struct {
		Invites []accessInviteBody `json:"invites"`
	}
	resp.decode(t, &listed)
	require.Len(t, listed.Invites, 1)
	resp = doRequest(t, http.MethodDelete, "/pet/"+petID+"/access-invites/"+invite.ID, nil, owner.token)
	require.Equal(t, http.StatusNoContent, resp.status)
	resp = doRequest(t, http.MethodPost, "/pet-codes/preview", map[string]string{"code": invite.Code}, other.token)
	require.Equal(t, http.StatusNotFound, resp.status)

	// Некорректный формат кода — 400.
	resp = doUnvalidatedRequest(t, http.MethodPost, "/pet-codes/preview", map[string]string{"code": "bad"}, other.token)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestPetAccess_MultiPetInviteAndAlreadyGranted(t *testing.T) {
	owner := newAccessUser(t, "Owner")
	member := newAccessUser(t, "Member")
	petA := createPet(t, owner.token, "A")
	petB := createPet(t, owner.token, "B")

	// pet_ids обязан содержать питомца из пути.
	resp := doUnvalidatedRequest(t, http.MethodPost, "/pet/"+petA+"/access-invites",
		map[string]any{"role": "view", "pet_ids": []string{petB}}, owner.token)
	require.Equal(t, http.StatusBadRequest, resp.status)
	resp = doRequest(t, http.MethodPost, "/pet/"+petA+"/access-invites", map[string]any{"role": "view", "pet_ids": []string{petA, uuidOther()}}, owner.token)
	require.Equal(t, http.StatusNotFound, resp.status)

	grantAccess(t, owner, member, petA, "view")
	invite := createAccessInvite(t, owner.token, petA, "full", []string{petA, petB})
	resp = doRequest(t, http.MethodPost, "/pet-codes/preview", map[string]string{"code": invite.Code}, member.token)
	require.Equal(t, http.StatusOK, resp.status)
	var preview struct {
		Access struct {
			Pets []struct {
				Name string `json:"name"`
			} `json:"pets"`
		} `json:"access"`
	}
	resp.decode(t, &preview)
	require.Len(t, preview.Access.Pets, 1)
	require.Equal(t, "B", preview.Access.Pets[0].Name)

	resp = doRequest(t, http.MethodPost, "/pet-access-invites/accept", map[string]string{"code": invite.Code}, member.token)
	require.Equal(t, http.StatusOK, resp.status)
	var accepted struct {
		PetIDs []string `json:"pet_ids"`
	}
	resp.decode(t, &accepted)
	require.Equal(t, []string{petB}, accepted.PetIDs)
	// У A осталась прежняя роль.
	_, access, _ := petAccessOf(t, member.token, petA)
	require.Equal(t, "view", access)
	_, access, _ = petAccessOf(t, member.token, petB)
	require.Equal(t, "full", access)

	// Доступ ко всем питомцам приглашения уже есть.
	invite = createAccessInvite(t, owner.token, petA, "full", []string{petA, petB})
	resp = doRequest(t, http.MethodPost, "/pet-codes/preview", map[string]string{"code": invite.Code}, member.token)
	require.Equal(t, http.StatusConflict, resp.status)
	require.Equal(t, "ACCESS_ALREADY_GRANTED", errorCodeOf(t, resp))
}

func TestPetAccess_MembersLimit(t *testing.T) {
	owner := newAccessUser(t, "Owner")
	petID := createPet(t, owner.token, "Барсик")
	for i := 0; i < database.PetAccessMaxMembers; i++ {
		createAccessInvite(t, owner.token, petID, "view", nil)
	}
	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/access-invites", map[string]string{"role": "view"}, owner.token)
	require.Equal(t, http.StatusConflict, resp.status)
	require.Equal(t, "ACCESS_LIMIT_REACHED", errorCodeOf(t, resp))
}

func TestPetAccess_RecordsVisibilityAuthorAndWrite(t *testing.T) {
	owner := newAccessUser(t, "Owner")
	full := newAccessUser(t, "Full")
	view := newAccessUser(t, "View")
	petA := createPet(t, owner.token, "A")
	petB := createPet(t, owner.token, "B")
	grantAccess(t, owner, full, petA, "full")
	grantAccess(t, owner, view, petA, "view")

	// Пока питомцем пользуется один человек, автора нет.
	solo := createPet(t, owner.token, "Solo")
	resp := doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_ids": []string{solo}, "date": "2024-01-01T10:00:00Z", "type": "weight", "value": map[string]any{"amount": 4.2},
	}, owner.token)
	require.Equal(t, http.StatusCreated, resp.status)
	var soloEvent struct {
		Author any `json:"author"`
	}
	resp.decode(t, &soloEvent)
	require.Nil(t, soloEvent.Author)

	// Участник full создаёт событие; автор — он, владелец видит запись.
	resp = doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_ids": []string{petA}, "date": "2024-01-01T10:00:00Z", "type": "weight", "value": map[string]any{"amount": 4.2},
	}, full.token)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
	var created struct {
		ID     string `json:"id"`
		Author struct {
			Name string `json:"name"`
			IsMe bool   `json:"is_me"`
		} `json:"author"`
	}
	resp.decode(t, &created)
	require.Equal(t, "Full", created.Author.Name)
	require.True(t, created.Author.IsMe)

	resp = doRequest(t, http.MethodGet, "/events/"+created.ID, nil, owner.token)
	require.Equal(t, http.StatusOK, resp.status)
	resp.decode(t, &created)
	require.Equal(t, "Full", created.Author.Name)
	require.False(t, created.Author.IsMe)

	// view читает, но не изменяет и не удаляет.
	resp = doRequest(t, http.MethodGet, "/events/"+created.ID, nil, view.token)
	require.Equal(t, http.StatusOK, resp.status)
	resp = doRequest(t, http.MethodPatch, "/events/"+created.ID, map[string]any{"notes": "x"}, view.token)
	require.Equal(t, http.StatusForbidden, resp.status)
	resp = doRequest(t, http.MethodDelete, "/events/"+created.ID, nil, view.token)
	require.Equal(t, http.StatusForbidden, resp.status)

	// Один владелец у всех питомцев записи не нарушается, а запись с двумя
	// питомцами скрыта от участника, у которого нет доступа ко второму.
	resp = doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_ids": []string{petA, petB}, "date": "2024-01-02T10:00:00Z", "type": "feeding",
		"value": map[string]any{"food": "dry", "unit": "g", "amount": 50},
	}, owner.token)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
	var shared struct {
		ID string `json:"id"`
	}
	resp.decode(t, &shared)
	resp = doRequest(t, http.MethodGet, "/events/"+shared.ID, nil, full.token)
	require.Equal(t, http.StatusNotFound, resp.status)
	resp = doRequest(t, http.MethodGet, "/pet/"+petA+"/events", nil, full.token)
	require.Equal(t, http.StatusOK, resp.status)
	var events struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	resp.decode(t, &events)
	require.Equal(t, 1, events.Total)
	require.Equal(t, created.ID, events.Items[0].ID)

	// Участник с доступом к питомцу другого владельца не объединяет их в записи.
	foreign := createPet(t, view.token, "Own")
	resp = doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_ids": []string{petA, foreign}, "date": "2024-01-02T10:00:00Z", "type": "feeding",
		"value": map[string]any{"food": "dry", "unit": "g", "amount": 50},
	}, full.token)
	require.Equal(t, http.StatusNotFound, resp.status)
	grantAccess(t, view, full, foreign, "full")
	resp = doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_ids": []string{petA, foreign}, "date": "2024-01-02T10:00:00Z", "type": "feeding",
		"value": map[string]any{"food": "dry", "unit": "g", "amount": 50},
	}, full.token)
	require.Equal(t, http.StatusBadRequest, resp.status)

	// Запись Ветпаспорта с автором.
	resp = doRequest(t, http.MethodPost, "/pet/"+petA+"/vet-visits", map[string]any{"visit_date": "2024-01-01", "reason": "Осмотр"}, full.token)
	require.Equal(t, http.StatusCreated, resp.status)
	resp = doRequest(t, http.MethodGet, "/pet/"+petA+"/vet-visits", nil, owner.token)
	require.Equal(t, http.StatusOK, resp.status)
	var visits struct {
		Items []struct {
			Author struct {
				Name string `json:"name"`
				IsMe bool   `json:"is_me"`
			} `json:"author"`
		} `json:"items"`
	}
	resp.decode(t, &visits)
	require.Len(t, visits.Items, 1)
	require.Equal(t, "Full", visits.Items[0].Author.Name)
	require.False(t, visits.Items[0].Author.IsMe)
}

func TestPetAccess_RevokeLeaveAndNotices(t *testing.T) {
	owner := newAccessUser(t, "Owner")
	member := newAccessUser(t, "Member")
	leaver := newAccessUser(t, "Leaver")
	petID := createPet(t, owner.token, "Барсик")
	grantAccess(t, owner, member, petID, "full")
	grantAccess(t, owner, leaver, petID, "view")

	// Запись участника остаётся у питомца после отзыва.
	resp := doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_ids": []string{petID}, "date": "2024-01-01T10:00:00Z", "type": "weight", "value": map[string]any{"amount": 4.2},
	}, member.token)
	require.Equal(t, http.StatusCreated, resp.status)

	// Владельцу отказ недоступен.
	resp = doRequest(t, http.MethodDelete, "/pet/"+petID+"/access/me", nil, owner.token)
	require.Equal(t, http.StatusForbidden, resp.status)

	resp = doRequest(t, http.MethodDelete, "/pet/"+petID+"/access/me", nil, leaver.token)
	require.Equal(t, http.StatusNoContent, resp.status)
	require.Empty(t, listPets(t, leaver.token).AccessLost)
	resp = doRequest(t, http.MethodDelete, "/pet/"+petID+"/access/me", nil, leaver.token)
	require.Equal(t, http.StatusNotFound, resp.status)

	resp = doRequest(t, http.MethodDelete, "/pet/"+petID+"/access/"+member.id, nil, owner.token)
	require.Equal(t, http.StatusNoContent, resp.status)
	resp = doRequest(t, http.MethodDelete, "/pet/"+petID+"/access/"+member.id, nil, owner.token)
	require.Equal(t, http.StatusNotFound, resp.status)

	list := listPets(t, member.token)
	require.Empty(t, list.Items)
	require.Len(t, list.AccessLost, 1)
	require.Equal(t, "revoked", list.AccessLost[0].Reason)
	require.Equal(t, petID, list.AccessLost[0].PetID)

	status, _, _ := petAccessOf(t, member.token, petID)
	require.Equal(t, http.StatusNotFound, status)
	resp = doRequest(t, http.MethodGet, "/pet/"+petID+"/events", nil, owner.token)
	var events struct {
		Total int `json:"total"`
	}
	resp.decode(t, &events)
	require.Equal(t, 1, events.Total)

	resp = doRequest(t, http.MethodDelete, "/pet-access-notices/"+petID, nil, member.token)
	require.Equal(t, http.StatusNoContent, resp.status)
	resp = doRequest(t, http.MethodDelete, "/pet-access-notices/"+petID, nil, member.token)
	require.Equal(t, http.StatusNoContent, resp.status)
	require.Empty(t, listPets(t, member.token).AccessLost)

	// Новая выдача доступа удаляет уведомление.
	grantAccess(t, owner, member, petID, "view")
	require.Empty(t, listPets(t, member.token).AccessLost)
}

func TestPetAccess_EndedByPetDeleteTransferAndAccountDelete(t *testing.T) {
	owner := newAccessUser(t, "Owner")
	member := newAccessUser(t, "Member")
	newOwner := newAccessUser(t, "NewOwner")

	// Мягкое удаление питомца.
	deletedPet := createPet(t, owner.token, "Deleted")
	grantAccess(t, owner, member, deletedPet, "view")
	pendingInvite := createAccessInvite(t, owner.token, deletedPet, "view", nil)
	resp := doRequest(t, http.MethodDelete, "/pet/"+deletedPet, nil, owner.token)
	require.Equal(t, http.StatusNoContent, resp.status)
	list := listPets(t, member.token)
	require.Empty(t, list.Items)
	require.Len(t, list.AccessLost, 1)
	require.Equal(t, "pet_deleted", list.AccessLost[0].Reason)
	resp = doRequest(t, http.MethodPost, "/pet-codes/preview", map[string]string{"code": pendingInvite.Code}, newOwner.token)
	require.Equal(t, http.StatusNotFound, resp.status)

	// Передача питомца прекращает доступы.
	transferred := createPet(t, owner.token, "Moved")
	grantAccess(t, owner, member, transferred, "full")
	transfer := createTransfer(t, owner.token, transferred)
	resp = postTransferCode(t, "accept", newOwner.token, transfer.Code)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	status, access, shared := petAccessOf(t, member.token, transferred)
	require.Equal(t, http.StatusNotFound, status)
	require.Empty(t, access)
	require.False(t, shared)
	_, access, shared = petAccessOf(t, newOwner.token, transferred)
	require.Equal(t, "owner", access)
	require.False(t, shared)
	reasons := map[string]bool{}
	for _, notice := range listPets(t, member.token).AccessLost {
		reasons[notice.Reason] = true
	}
	require.True(t, reasons["pet_transferred"])

	// Удаление аккаунта владельца: участники получают уведомление, записи
	// участника про чужих питомцев переходят к владельцу.
	kept := createPet(t, owner.token, "Kept")
	grantAccess(t, owner, member, kept, "full")
	resp = doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_ids": []string{kept}, "date": "2024-01-01T10:00:00Z", "type": "weight", "value": map[string]any{"amount": 4.2},
	}, member.token)
	require.Equal(t, http.StatusCreated, resp.status)

	ownerSurvivor := newAccessUser(t, "Survivor")
	survivorPet := createPet(t, ownerSurvivor.token, "Pet")
	grantAccess(t, ownerSurvivor, member, survivorPet, "full")
	resp = doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_ids": []string{survivorPet}, "date": "2024-01-01T10:00:00Z", "type": "weight", "value": map[string]any{"amount": 5},
	}, member.token)
	require.Equal(t, http.StatusCreated, resp.status)

	resp = doRequest(t, http.MethodDelete, "/profile", nil, member.token)
	require.Equalf(t, http.StatusNoContent, resp.status, "%s", resp.body)
	resp = doRequest(t, http.MethodGet, "/pet/"+survivorPet+"/events", nil, ownerSurvivor.token)
	require.Equal(t, http.StatusOK, resp.status)
	var events struct {
		Total int `json:"total"`
	}
	resp.decode(t, &events)
	require.Equal(t, 1, events.Total)

	member2 := newAccessUser(t, "Member2")
	grantAccess(t, owner, member2, kept, "view")
	resp = doRequest(t, http.MethodDelete, "/profile", nil, owner.token)
	require.Equalf(t, http.StatusNoContent, resp.status, "%s", resp.body)
	list = listPets(t, member2.token)
	require.Len(t, list.AccessLost, 1)
	require.Equal(t, "pet_deleted", list.AccessLost[0].Reason)
}

func TestPetAccess_CleanupExpiresStaleInvites(t *testing.T) {
	owner := newAccessUser(t, "Owner")
	other := newAccessUser(t, "Other")
	petID := createPet(t, owner.token, "Барсик")
	invite := createAccessInvite(t, owner.token, petID, "view", nil)

	_, err := database.DB.Exec(`UPDATE pet_access_invite SET expires_at = now() - interval '1 minute' WHERE id = $1`, invite.ID)
	require.NoError(t, err)
	changed, err := database.ExpireStalePetAccessInvites()
	require.NoError(t, err)
	require.EqualValues(t, 1, changed)
	var status string
	require.NoError(t, database.DB.QueryRow(`SELECT status FROM pet_access_invite WHERE id = $1`, invite.ID).Scan(&status))
	require.Equal(t, "expired", status)
	resp := doRequest(t, http.MethodPost, "/pet-access-invites/accept", map[string]string{"code": invite.Code}, other.token)
	require.Equal(t, http.StatusNotFound, resp.status)
}

func TestPetAccess_CodeRateLimitSharedWithTransfers(t *testing.T) {
	user := newAccessUser(t, "User")
	for i := 0; i < database.PetTransferCodeFailureLimit; i++ {
		resp := postTransferCode(t, "preview", user.token, "TBCDEFGHJK")
		require.Equal(t, http.StatusNotFound, resp.status)
	}
	resp := doRequest(t, http.MethodPost, "/pet-codes/preview", map[string]string{"code": "TBCDEFGHJK"}, user.token)
	require.Equal(t, http.StatusTooManyRequests, resp.status)
}

func uuidOther() string {
	return "00000000-0000-4000-8000-000000000001"
}

func TestPetOwnerRef_InListAndCard(t *testing.T) {
	owner := newAccessUser(t, "Anna")
	member := newAccessUser(t, "Member")
	noProfile := newAccessUser(t, "")
	_, err := database.DB.Exec(`UPDATE profile SET last_name = 'Ivanova' WHERE user_id = $1`, owner.id)
	require.NoError(t, err)
	ownPet := createPet(t, owner.token, "A")
	plainPet := createPet(t, noProfile.token, "B")
	grantAccess(t, owner, member, ownPet, "view")
	grantAccess(t, noProfile, member, plainPet, "view")
	_, err = database.DB.Exec(`UPDATE profile SET first_name = ' ' WHERE user_id = $1`, noProfile.id)
	require.NoError(t, err)

	resp := doRequest(t, http.MethodGet, "/pet", nil, member.token)
	require.Equal(t, http.StatusOK, resp.status)
	var list struct {
		Items []struct {
			ID    string `json:"id"`
			Owner struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"owner"`
		} `json:"items"`
	}
	resp.decode(t, &list)
	require.Len(t, list.Items, 2)
	for _, item := range list.Items {
		switch item.ID {
		case ownPet:
			require.Equal(t, owner.id, item.Owner.ID)
			require.Equal(t, "Ivanova Anna", item.Owner.Name)
		case plainPet:
			require.Equal(t, noProfile.id, item.Owner.ID)
			require.Equal(t, noProfile.login, item.Owner.Name)
		}
	}

	resp = doRequest(t, http.MethodGet, "/pet/"+ownPet, nil, member.token)
	var card struct {
		Owner struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"owner"`
	}
	resp.decode(t, &card)
	require.Equal(t, owner.id, card.Owner.ID)
	require.Equal(t, "Ivanova Anna", card.Owner.Name)

	// Собственный питомец: владелец — сам пользователь.
	resp = doRequest(t, http.MethodGet, "/pet/"+ownPet, nil, owner.token)
	resp.decode(t, &card)
	require.Equal(t, owner.id, card.Owner.ID)
}
