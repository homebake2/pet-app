package handlers

import (
	"encoding/json"
	"fmt"
	"myauthservice/database"
	"myauthservice/eventreg"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"

	"github.com/google/uuid"
)

// Общие правила набора питомцев записи (события и настроек напоминания), см.
// «Общие требования: Несколько питомцев в событии и напоминании»: от 1 до 10
// различных питомцев пользователя, ни один не мягко удалён; тип и значения
// вложенных словарей применимы к виду каждого питомца; измерительные типы —
// только при одном питомце.

// parsePetIDs разбирает массив pet_ids: от 1 до 10 различных валидных UUID.
// Непустое msg — текст ответа 400.
func parsePetIDs(raw []string) (ids []uuid.UUID, msg string) {
	if len(raw) == 0 {
		return nil, "Поле pet_ids должно содержать от 1 до 10 питомцев"
	}
	if len(raw) > models.MaxEventPets {
		return nil, fmt.Sprintf("Поле pet_ids не должно содержать более %d питомцев", models.MaxEventPets)
	}
	seen := make(map[uuid.UUID]bool, len(raw))
	ids = make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, "Некорректный ID питомца"
		}
		if seen[id] {
			return nil, "Поле pet_ids не должно содержать повторяющихся питомцев"
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, ""
}

// resolvePetSet загружает питомцев ids пользователя userID в порядке ids.
// Питомец не найден либо принадлежит другому пользователю — 404 (без
// раскрытия, какой именно), мягко удалён — 400 (события) либо 404 при
// deletedAsNotFound (настройки напоминания: мягко удалённый питомец для них
// неотличим от несуществующего). Ошибки проверки возвращаются как
// reminderHTTPError, прочие — как есть (500).
func resolvePetSet(exec database.Executor, userID string, ids []uuid.UUID, deletedAsNotFound bool) ([]database.LinkedPet, error) {
	owned, err := database.GetOwnedPetsWith(exec, userID, ids)
	if err != nil {
		return nil, err
	}
	pets := make([]database.LinkedPet, 0, len(ids))
	anyDeleted := false
	for _, id := range ids {
		pet, ok := owned[id]
		if !ok {
			return nil, newReminderHTTPError(http.StatusNotFound, openapi.NOTFOUND, "Питомец "+id.String()+" не найден")
		}
		if pet.Deleted {
			anyDeleted = true
		}
		pets = append(pets, pet.LinkedPet)
	}
	if anyDeleted {
		if deletedAsNotFound {
			return nil, newReminderHTTPError(http.StatusNotFound, openapi.NOTFOUND, "Питомец не найден")
		}
		return nil, newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, "Невозможно использовать питомца, так как он удален")
	}
	return pets, nil
}

// validateEventForPets проверяет тип события и значение вложенных словарей
// value по итоговому набору питомцев записи: тип применим к виду каждого
// питомца (пересечение допустимых типов), измерительный тип допускает ровно
// одного питомца, значение вложенного словаря применимо к виду каждого
// питомца. Пустая строка — сочетание допустимо, иначе текст ответа 400.
func validateEventForPets(eventType string, value json.RawMessage, pets []database.LinkedPet) string {
	if len(pets) > 1 && !eventreg.AllowsMultiplePets(eventType) {
		return "Тип события " + eventType + " допускает ровно одного питомца"
	}
	for _, pet := range pets {
		if !isTypeApplicableToPet(eventType, pet.Species) {
			return "Тип события " + eventType + " неприменим к виду питомца"
		}
		if msg := isNestedValueApplicableToPet(eventType, value, pet.Species); msg != "" {
			return msg
		}
	}
	return ""
}

// petIDsOf возвращает id питомцев в порядке pets.
func petIDsOf(pets []database.LinkedPet) []uuid.UUID {
	ids := make([]uuid.UUID, len(pets))
	for i, pet := range pets {
		ids[i] = pet.ID
	}
	return ids
}

// hasNewPets сообщает, есть ли среди desired питомцы, которых нет в current:
// только привязка нового питомца может нарушить применимость сохранённых
// типа и значения — отвязка набор лишь сужает.
func hasNewPets(current []database.LinkedPet, desired []database.LinkedPet) bool {
	have := make(map[uuid.UUID]bool, len(current))
	for _, pet := range current {
		have[pet.ID] = true
	}
	for _, pet := range desired {
		if !have[pet.ID] {
			return true
		}
	}
	return false
}
