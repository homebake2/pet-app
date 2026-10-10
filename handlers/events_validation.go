package handlers

import (
	"encoding/json"
	"fmt"
	"myauthservice/eventreg"
	"myauthservice/models"
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

// isValidEnclosureVolumeL проверяет диапазон профильного поля
// enclosure_volume_l (см. «Профильные поля питомца по видам»).
func isValidEnclosureVolumeL(volume float64) bool {
	return volume >= models.PetEnclosureVolumeLMin && volume <= models.PetEnclosureVolumeLMax
}

// isValidGroupSize проверяет диапазон профильного поля group_size (см.
// «Профильные поля питомца по видам»).
func isValidGroupSize(groupSize int) bool {
	return groupSize >= models.PetGroupSizeMin && groupSize <= models.PetGroupSizeMax
}

// petSpeciesOrDefault возвращает pet.species питомца для проверки
// применимости типа события; если species не входит в закрытый справочник
// видов (см. models.IsKnownSpeciesValue) — используется дефолт "OTHER",
// применимый ко всем типам, то же соглашение, что раньше действовало для
// pet.icon.
func petSpeciesOrDefault(species string) string {
	if models.IsKnownSpeciesValue(species) {
		return species
	}
	return "OTHER"
}

// isTypeApplicableToPet проверяет применимость типа события eventType к
// виду питомца (см. «Модель значения события и реестр метрик», раздел
// «Применимость типа события к виду питомца»). Общая функция для POST
// /events и PATCH /events/{id} — правило одно и то же для обоих эндпоинтов.
func isTypeApplicableToPet(eventType string, petSpecies string) bool {
	return eventreg.IsApplicableToSpecies(eventType, petSpeciesOrDefault(petSpecies))
}

// nestedApplicabilityField возвращает имя вложенного enum-поля value, чья
// применимость к виду питомца проверяется для eventType отдельно от
// применимости самого типа (второй уровень применимости — см. «Применимость
// значений вложенных enum к виду питомца»). Пустая строка — для eventType
// такой проверки нет. Поле "kind" у type=temperature (EventTemperatureKindEnum)
// сюда намеренно не включено — это отдельный словарь без ограничений по
// видам, в отличие от "kind" у type=activity (EventActivityKindEnum).
func nestedApplicabilityField(eventType string) string {
	switch eventType {
	case "hygiene":
		return "procedure"
	case "feeding":
		return "food"
	case "activity":
		return "kind"
	default:
		return ""
	}
}

// isNestedValueApplicableToPet проверяет применимость вложенного enum-поля
// value (hygiene.procedure, feeding.food, activity.kind) к виду питомца —
// второй уровень применимости после применимости самого типа события (см.
// «Применимость значений вложенных enum к виду питомца»). Общая функция для
// POST /events и PATCH /events/{id} — правило одно и то же для обоих
// эндпоинтов. Вызывается только после успешной validateEventValue, поэтому
// форма value уже гарантированно корректна: ошибка разбора здесь означает,
// что проверяемого поля нет в value, и функция просто ничего не проверяет.
// Возвращает пустую строку, если сочетание допустимо, иначе — сообщение об
// ошибке для ответа 400.
func isNestedValueApplicableToPet(eventType string, value json.RawMessage, petSpecies string) string {
	fieldName := nestedApplicabilityField(eventType)
	if fieldName == "" {
		return ""
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		return ""
	}
	raw, present := fields[fieldName]
	if !present {
		return ""
	}
	var fieldValue string
	if err := json.Unmarshal(raw, &fieldValue); err != nil {
		return ""
	}

	species := petSpeciesOrDefault(petSpecies)
	if eventreg.IsFieldValueApplicableToSpecies(eventType, fieldName, fieldValue, species) {
		return ""
	}
	return fmt.Sprintf("Значение value.%s=%s недопустимо для вида питомца", fieldName, fieldValue)
}

func parseEventDate(date string) (time.Time, error) {
	return time.Parse(time.RFC3339, date)
}

// factDateTolerance — допуск на расхождение часов клиента и сервера для
// даты факта: дата события не может быть позднее now() больше чем на это
// значение.
const factDateTolerance = 5 * time.Minute

// validateFactDate проверяет, что дата события (факта — того, что уже
// произошло) не позднее текущего момента (UTC, на момент обработки запроса)
// с допуском factDateTolerance. Одно и то же правило действует для POST
// /events, PATCH /events/{id}, импорта и фактов, которые создают другие
// флоу (отметка напоминания «выполнено», замена напоминания, вакцинация).
// Возвращает пустую строку, если дата допустима, иначе — сообщение об
// ошибке для ответа 400.
func validateFactDate(date time.Time) string {
	if date.After(time.Now().UTC().Add(factDateTolerance)) {
		return "Дата события не может быть в будущем: запланированные события создаются как напоминания"
	}
	return ""
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
