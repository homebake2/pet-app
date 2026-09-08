package handlers

import (
	"encoding/json"
	"myauthservice/eventreg"
	"time"

	"github.com/google/uuid"
)

// validateEventValue проверяет типизированное значение события по единому
// реестру метрик (пакет eventreg). Ветвления по типу события здесь нет и
// быть не должно: форма value, диапазоны и словари описаны в одном месте, к
// которому обращаются создание (POST /events), редактирование
// (PATCH /events/{id}) и импорт (POST /import/local-data).
//
// Возвращает пустую строку, если value валиден для eventType, иначе —
// сообщение об ошибке для ответа 400.
func validateEventValue(eventType string, value json.RawMessage) string {
	return eventreg.ValidateValue(eventType, value)
}

// isValidWeight проверяет диапазон веса питомца (POST /pet и PUT /pet/{id},
// поле weight), переиспользуя границы value.amount типа события "weight" из
// eventreg — единственного источника истины для диапазона 0.001–400 кг
// (см. «Вес питомца — Backend»).
func isValidWeight(weight float64) bool {
	spec, ok := eventreg.Spec("weight")
	if !ok {
		return false
	}
	field, ok := spec.Field("amount")
	if !ok {
		return false
	}
	return weight >= field.Min && weight <= field.Max
}

func validateNotesLength(notes *string) bool {
	return notes == nil || len(*notes) <= maxEventFieldLen
}

func parseEventDate(date string) (time.Time, error) {
	return time.Parse(time.RFC3339, date)
}

// isValidUUIDv4 проверяет, что значение заголовка Idempotency-Key — валидный
// UUID версии 4 (см. страницу "Добавление события — Backend").
func isValidUUIDv4(value string) bool {
	id, err := uuid.Parse(value)
	if err != nil {
		return false
	}
	return id.Version() == 4
}
