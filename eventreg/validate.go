package eventreg

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ValidateValue проверяет типизированное значение события по реестру:
// объект value должен содержать ровно те поля, которые описаны формой для
// eventType, обязательные поля обязаны присутствовать, числовые — попадать в
// диапазон, enum-поля — входить в словарь.
//
// Единый валидатор для POST /events, PATCH /events/{id} и
// POST /import/local-data: ослабленного правила для импорта не существует.
// Возвращает пустую строку, если значение валидно, иначе — сообщение для
// ответа 400.
func ValidateValue(eventType string, raw json.RawMessage) string {
	spec, ok := Spec(eventType)
	if !ok {
		return "Некорректное значение type"
	}

	if len(raw) == 0 {
		return "Поле value обязательно"
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Sprintf("Поле value для type=%s должно быть объектом", eventType)
	}
	if fields == nil {
		return "Поле value обязательно"
	}

	for name := range fields {
		if _, allowed := spec.Field(name); !allowed {
			return fmt.Sprintf("Поле value.%s недопустимо для type=%s; допустимые поля: %s", name, eventType, strings.Join(fieldNames(spec), ", "))
		}
	}

	for _, field := range spec.Fields {
		value, present := fields[field.Name]
		if present && isJSONNull(value) {
			present = false
			delete(fields, field.Name)
		}

		if !present {
			if field.Required {
				return fmt.Sprintf("Поле value.%s обязательно для type=%s", field.Name, eventType)
			}
			continue
		}

		if msg := validateField(eventType, field, value); msg != "" {
			return msg
		}
	}

	// Парные поля (medication.dose_amount / dose_unit) передаются только вместе.
	for _, field := range spec.Fields {
		if field.RequiredWith == "" {
			continue
		}
		if _, present := fields[field.Name]; !present {
			continue
		}
		if _, pairPresent := fields[field.RequiredWith]; !pairPresent {
			return fmt.Sprintf("Поля value.%s и value.%s для type=%s передаются только вместе", field.Name, field.RequiredWith, eventType)
		}
	}

	return ""
}

func validateField(eventType string, field Field, raw json.RawMessage) string {
	switch field.Type {
	case FieldNumber:
		var number float64
		if err := json.Unmarshal(raw, &number); err != nil {
			return fmt.Sprintf("Поле value.%s для type=%s должно быть числом", field.Name, eventType)
		}
		if number < field.Min || number > field.Max {
			return fmt.Sprintf("Поле value.%s для type=%s должно быть в диапазоне %s–%s", field.Name, eventType, formatBound(field.Min), formatBound(field.Max))
		}
		return ""
	case FieldString:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return fmt.Sprintf("Поле value.%s для type=%s должно быть строкой", field.Name, eventType)
		}
		length := utf8.RuneCountInString(strings.TrimSpace(text))
		if length < field.MinLen || length > field.MaxLen {
			return fmt.Sprintf("Поле value.%s для type=%s должно быть от %d до %d символов", field.Name, eventType, field.MinLen, field.MaxLen)
		}
		return ""
	case FieldEnum:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return fmt.Sprintf("Поле value.%s для type=%s должно быть строкой", field.Name, eventType)
		}
		for _, allowed := range field.Enum {
			if text == allowed {
				return ""
			}
		}
		return fmt.Sprintf("Поле value.%s для type=%s должно быть одним из: %s", field.Name, eventType, strings.Join(field.Enum, ", "))
	default:
		return fmt.Sprintf("Поле value.%s для type=%s не поддерживается", field.Name, eventType)
	}
}

func fieldNames(spec TypeSpec) []string {
	names := make([]string, 0, len(spec.Fields))
	for _, f := range spec.Fields {
		names = append(names, f.Name)
	}
	return names
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func formatBound(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}
