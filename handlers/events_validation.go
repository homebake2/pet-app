package handlers

import (
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const (
	minEventWeight = 0.01
	maxEventWeight = 400
	minOtherLen    = 1
	maxOtherLen    = 50
)

var allowedStatusValues = map[string]bool{
	"normal":   true,
	"abnormal": true,
}

func isStatusEventType(eventType string) bool {
	switch eventType {
	case "urine", "defecation", "vomit", "diarrhea":
		return true
	default:
		return false
	}
}

// validateEventValue проверяет value по per-type схеме (см. страницу
// "Добавление события — Backend", таблица форматов value). Возвращает
// пустую строку, если value валиден для eventType, иначе — сообщение об
// ошибке для ответа 400.
func validateEventValue(eventType, value string) string {
	switch {
	case eventType == "weight":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f < minEventWeight || f > maxEventWeight {
			return fmt.Sprintf("Поле value для type=weight должно быть числом в диапазоне %.2f–%.0f", minEventWeight, float64(maxEventWeight))
		}
		return ""
	case isStatusEventType(eventType):
		if !allowedStatusValues[value] {
			return `Поле value для type=` + eventType + ` должно быть "normal" или "abnormal"`
		}
		return ""
	case eventType == "other":
		if len(value) < minOtherLen || len(value) > maxOtherLen {
			return "Поле value для type=other должно быть от 1 до 50 символов"
		}
		return ""
	default:
		// Недопустимый type отклоняется отдельной проверкой (IsValidEventType)
		// до вызова validateEventValue.
		return "Некорректное значение type"
	}
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
