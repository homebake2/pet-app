package eventreg

// QuickActionCandidate — тип события, пригодный для кнопки быстрого действия
// виджета, и значение value, с которым событие создаётся одним нажатием.
type QuickActionCandidate struct {
	Type  string
	Value map[string]string
}

// quickActionDefaults — значения по умолчанию для полей value типов, у
// которых каждое обязательное поле имеет «нейтральное» значение (см.
// «Модель значения события и реестр метрик», раздел «Значения по умолчанию
// для полей value»). Тип без обязательного поля с умолчанием (измерения,
// свободный текст, water_quality) в таблицу не входит: создать для него
// осмысленное событие без ввода пользователя нельзя. Появление умолчаний у
// нового типа делает его кандидатом автоматически — достаточно добавить
// строку сюда; соответствие значений реестру проверяет тест.
var quickActionDefaults = map[string]map[string]string{
	"urine":      {"status": "normal"},
	"defecation": {"status": "normal"},
	"vomit":      {"status": "normal"},
	"diarrhea":   {"status": "normal"},
	"mood":       {"state": "calm"},
	"molting":    {"status": "started"},
	"heat_cycle": {"phase": "started"},
}

// QuickActionCandidates возвращает кандидатов на кнопки быстрого действия
// для вида питомца species — применимые к виду типы с полным набором
// умолчаний — в порядке справочника типов события.
func QuickActionCandidates(species string) []QuickActionCandidate {
	var out []QuickActionCandidate
	for _, eventType := range Types() {
		value, ok := quickActionDefaults[eventType]
		if !ok || !IsApplicableToSpecies(eventType, species) {
			continue
		}
		copied := make(map[string]string, len(value))
		for k, v := range value {
			copied[k] = v
		}
		out = append(out, QuickActionCandidate{Type: eventType, Value: copied})
	}
	return out
}
