// Package eventreg — единый реестр типов событий и метрик.
//
// Это единственное место в коде, описывающее для каждого типа события форму
// его типизированного значения (`value`), диапазоны и словари вложенных
// enum, а также метрики и способ их свёртки для графиков динамики.
// К реестру обращаются создание события (POST /events), редактирование
// (PATCH /events/{id}), импорт локальных данных (POST /import/local-data) и
// агрегация (GET /events/stats) — вторых списков правил по типам в
// обработчиках быть не должно.
//
// Источник истины требований: страницы «Модель значения события и реестр
// метрик» и «Справочник значений (словари enum)».
package eventreg

// ValueKind — характер значения события (см. реестр метрик).
type ValueKind string

const (
	KindMeasure  ValueKind = "measure"
	KindQuantity ValueKind = "quantity"
	KindCategory ValueKind = "category"
	KindLabel    ValueKind = "label"
)

// Aggregation — способ свёртки значений метрики внутри интервала графика.
type Aggregation string

const (
	AggAvg   Aggregation = "avg"
	AggLast  Aggregation = "last"
	AggSum   Aggregation = "sum"
	AggCount Aggregation = "count"
)

// FieldType — тип поля внутри объекта value.
type FieldType int

const (
	FieldNumber FieldType = iota
	FieldString
	FieldEnum
)

// Field описывает одно поле объекта value: тип, обязательность и границы.
// Поле, отсутствующее в списке Fields своего типа события, недопустимо.
type Field struct {
	Name     string
	Type     FieldType
	Required bool

	// Границы числового поля (FieldNumber), включительно.
	Min float64
	Max float64

	// Integer=true требует, чтобы числовое поле (FieldNumber) было целым —
	// egg_laying.count — количество отложенных яиц, штучная величина, что
	// закреплено в open-api/spec.json как type: integer.
	Integer bool

	// Границы длины строкового поля (FieldString), в символах.
	MinLen int
	MaxLen int

	// Допустимые значения enum-поля (FieldEnum).
	Enum []string

	// SpeciesByValue — применимость каждого значения enum-поля (FieldEnum) к
	// виду питомца pet.species — второй уровень применимости, ниже
	// TypeSpec.ApplicableSpecies (см. «Применимость значений вложенных enum к
	// виду питомца»). Ключ — значение enum, значение — список применимых
	// видов из закрытого справочника. Значение enum, отсутствующее в карте
	// (например, "other"), применимо любому виду без ограничения. nil
	// означает, что поле не имеет ограничений второго уровня — все его
	// значения применимы любому виду (temperature.kind: словарь
	// EventTemperatureKindEnum видовых ограничений не имеет).
	SpeciesByValue map[string][]string

	// RequiredWith — имя поля, вместе с которым это поле передаётся:
	// одно без другого является ошибкой валидации (medication.dose_amount и
	// medication.dose_unit).
	RequiredWith string
}

// Metric описывает одну метрику типа события для графиков.
// Field — поле внутри value, по которому считается метрика; пустое значение
// означает метрику «количество событий» (medication, категориальные типы).
//
// ValueKind — характер именно этой метрики; заполняется только когда он
// отличается от TypeSpec.ValueKind (water_quality: temperature_c/ph/
// ammonia_ppm — measure, changed_volume_ml — quantity, см. правило «ValueKind
// может быть задан на уровне отдельной метрики» реестра метрик). Пустое
// значение означает «как у типа целиком».
type Metric struct {
	Key         string
	Field       string
	Unit        string
	Aggregation Aggregation
	ValueKind   ValueKind
}

// TypeSpec — запись реестра для одного типа события.
type TypeSpec struct {
	Type      string
	ValueKind ValueKind
	Fields    []Field
	Metrics   []Metric

	// SplitField — поле value, по которому ряды графика разделяются на
	// независимые серии: temperature.kind (несопоставимые наблюдения в одной
	// шкале), feeding.unit (несуммируемые единицы), а для категориальных
	// типов — само поле категории (procedure/state/status).
	SplitField string

	// SplitAsUnit=true означает, что значение SplitField является единицей
	// измерения серии (feeding.unit), а не её категорией.
	SplitAsUnit bool

	// AtLeastOneOf — имена опциональных полей value, из которых хотя бы одно
	// обязано быть передано (water_quality: пустое value без единого
	// показателя не несёт факта). Пусто, если у типа такого правила нет.
	AtLeastOneOf []string

	// ApplicableSpecies — множество значений pet.species (из закрытого
	// справочника видов), для которых этот тип события применим (см.
	// «Применимость типа события к виду питомца»). nil означает «применим ко
	// всем видам справочника» (отметка «все» в таблице применимости).
	// Значение OTHER (species вне справочника или неопределён) применимо к
	// любому типу независимо от содержимого этого списка — проверяется
	// отдельно в IsApplicableToSpecies.
	ApplicableSpecies []string
}

// Field возвращает описание поля value по имени.
func (s TypeSpec) Field(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// SplitValues возвращает допустимые значения поля, разделяющего серии.
// Для типа без разделения — nil.
func (s TypeSpec) SplitValues() []string {
	if s.SplitField == "" {
		return nil
	}
	f, ok := s.Field(s.SplitField)
	if !ok {
		return nil
	}
	return f.Enum
}

// Aggregatable сообщает, строятся ли по типу ряды графика. Тип с
// value_kind=label (other) не агрегируется.
func (s TypeSpec) Aggregatable() bool {
	return s.ValueKind != KindLabel
}

// MetricValueKind возвращает value_kind конкретной метрики: собственный,
// если он задан в реестре отдельно от типа (water_quality), иначе — value_kind
// типа целиком.
func (s TypeSpec) MetricValueKind(m Metric) ValueKind {
	if m.ValueKind != "" {
		return m.ValueKind
	}
	return s.ValueKind
}

// allSpecies — полный справочник видов питомца (см. «Питомцы — Вид
// (species)»), 31 значение. Используется для построения применимости «все,
// кроме …» без переписывания списка целиком под каждое исключение.
var allSpecies = []string{
	"DOG", "CAT", "HAMSTER", "GUINEA_PIG", "RABBIT", "PARROT", "CANARY", "FISH",
	"TURTLE", "RAT", "MOUSE", "FERRET", "HEDGEHOG", "CHINCHILLA", "MINI_PIG",
	"MINI_GOAT", "CHICKEN", "DUCK", "PIGEON", "IGUANA", "GECKO", "BEARDED_AGAMA",
	"SNAKE", "PYTHON", "FROG", "AXOLOTL", "TARANTULA", "HERMIT_CRAB", "ANT_FARM",
	"SNAIL", "OTHER",
}

// allSpeciesExcept возвращает применимость вида «все, кроме …» из таблицы
// применимости — полный справочник видов минус перечисленные исключения.
func allSpeciesExcept(excluded ...string) []string {
	skip := make(map[string]bool, len(excluded))
	for _, species := range excluded {
		skip[species] = true
	}
	out := make([]string, 0, len(allSpecies))
	for _, species := range allSpecies {
		if !skip[species] {
			out = append(out, species)
		}
	}
	return out
}

// IsApplicableToSpecies сообщает, применим ли тип события eventType к виду
// питомца species (см. «Применимость типа события к виду питомца»). Тип
// без записи в реестре не применим ни к одному виду. OTHER (species вне
// справочника или неопределён) применим ко всем типам без исключения.
func IsApplicableToSpecies(eventType, species string) bool {
	spec, ok := Spec(eventType)
	if !ok {
		return false
	}
	if species == "OTHER" {
		return true
	}
	if spec.ApplicableSpecies == nil {
		return true
	}
	for _, applicable := range spec.ApplicableSpecies {
		if applicable == species {
			return true
		}
	}
	return false
}

// IsFieldValueApplicableToSpecies сообщает, применимо ли значение value
// вложенного enum-поля fieldName (hygiene.procedure, feeding.food,
// activity.kind) объекта value типа eventType к виду питомца species — второй
// уровень применимости, ниже применимости самого типа события (см.
// «Применимость значений вложенных enum к виду питомца»). OTHER (species вне
// справочника или неопределён) применимо любому значению. Тип без записи в
// реестре, поле без карты SpeciesByValue или значение вне карты (например,
// "other") — применимо любому виду без ограничения.
func IsFieldValueApplicableToSpecies(eventType, fieldName, value, species string) bool {
	spec, ok := Spec(eventType)
	if !ok {
		return true
	}
	field, ok := spec.Field(fieldName)
	if !ok || field.SpeciesByValue == nil {
		return true
	}
	applicable, hasRule := field.SpeciesByValue[value]
	if !hasRule {
		return true
	}
	if species == "OTHER" {
		return true
	}
	for _, s := range applicable {
		if s == species {
			return true
		}
	}
	return false
}

// Словари вложенных enum значения события (см. «Справочник значений»).
var (
	temperatureKinds   = []string{"body", "environment"}
	feedingUnits       = []string{"g", "ml", "portion", "piece"}
	feedingFoods       = []string{"dry", "wet", "raw", "homemade", "live_prey", "frozen_prey", "insects", "hay", "grain", "greens", "treat", "other"}
	activityKinds      = []string{"walk", "free_range", "play", "training", "swim", "other"}
	medicationDoseUnit = []string{"mcg", "mg", "g", "ml", "drop", "tablet", "capsule"}
	hygieneProcedures  = []string{"bath", "brushing", "teeth", "nails", "beak", "ears", "shedding", "antiparasitic", "enclosure", "water_change", "other"}
	moodStates         = []string{"calm", "playful", "lethargic", "anxious", "aggressive", "hiding"}
	excretionStatuses  = []string{"normal", "abnormal"}
	moltingStatuses    = []string{"started", "stuck", "completed"}
	eggLayingStatuses  = []string{"normal", "abnormal"}
	heatCyclePhases    = []string{"started", "ended"}
)

// Единицы измерения метрик. Единица типа фиксирована конвенцией, кроме
// feeding (задаётся value.unit) и medication (доза не агрегируется).
const (
	unitKg      = "kg"
	unitCelsius = "°C"
	unitMl      = "ml"
	unitMinutes = "min"
	unitMeters  = "m"
	unitPh      = "pH"
	unitPpm     = "ppm"
	unitPieces  = "pcs"
)

// Применимость к видам питомца (pet.species) для типов, где применимость
// задана как узкий явный список видов, а не «все, кроме …» (см.
// «Применимость типа события к виду питомца»).
var (
	urineSpecies = []string{
		"DOG", "CAT", "HAMSTER", "GUINEA_PIG", "RABBIT", "RAT", "MOUSE", "FERRET",
		"HEDGEHOG", "CHINCHILLA", "MINI_PIG", "MINI_GOAT", "OTHER",
	}
	vomitSpecies = []string{
		"DOG", "CAT", "FERRET", "HEDGEHOG", "MINI_PIG", "PARROT", "CANARY",
		"CHICKEN", "DUCK", "PIGEON", "TURTLE", "IGUANA", "GECKO", "BEARDED_AGAMA",
		"SNAKE", "PYTHON", "FROG", "OTHER",
	}
	moltingSpecies = []string{
		"TURTLE", "IGUANA", "GECKO", "BEARDED_AGAMA", "SNAKE", "PYTHON", "FROG",
		"AXOLOTL", "TARANTULA", "HERMIT_CRAB", "CHINCHILLA", "FERRET", "PARROT",
		"CANARY", "CHICKEN", "DUCK", "PIGEON", "OTHER",
	}
	eggLayingSpecies = []string{
		"PARROT", "CANARY", "CHICKEN", "DUCK", "PIGEON", "TURTLE", "IGUANA",
		"GECKO", "BEARDED_AGAMA", "SNAKE", "PYTHON", "FROG", "OTHER",
	}
	waterQualitySpecies = []string{"FISH", "AXOLOTL", "FROG", "TURTLE", "OTHER"}
	heatCycleSpecies    = []string{"DOG", "CAT", "RABBIT", "MINI_GOAT", "OTHER"}

	// coldBloodedExclusion — виды, для которых нерелевантны замер температуры
	// тела, сон в человеческом смысле и наблюдаемое настроение по общему
	// словарю (см. таблицу применимости: temperature, sleep, mood).
	coldBloodedExclusion = []string{
		"FISH", "TURTLE", "IGUANA", "GECKO", "BEARDED_AGAMA", "SNAKE", "PYTHON",
		"FROG", "AXOLOTL", "TARANTULA", "HERMIT_CRAB", "ANT_FARM", "SNAIL",
	}
	waterExclusion      = []string{"FISH", "AXOLOTL", "FROG", "TARANTULA", "ANT_FARM"}
	activityExclusion   = []string{"ANT_FARM", "SNAIL"}
	defecationExclusion = []string{"FISH", "AXOLOTL", "ANT_FARM", "SNAIL"}
	diarrheaExclusion   = []string{"FISH", "AXOLOTL", "FROG", "ANT_FARM", "SNAIL"}
)

// Группы видов питомца для применимости значений вложенных enum-полей value
// (hygiene.procedure, feeding.food, activity.kind) к виду питомца — второй
// уровень применимости, ниже TypeSpec.ApplicableSpecies (см. «Применимость
// значений вложенных enum к виду питомца»). Единственное место, где эти
// группы описаны — карты применимости ниже собираются из них через
// speciesUnion, вместо повторения списков видов под каждое значение enum.
var (
	mammalSpecies = []string{
		"DOG", "CAT", "HAMSTER", "GUINEA_PIG", "RABBIT", "RAT", "MOUSE", "FERRET",
		"HEDGEHOG", "CHINCHILLA", "MINI_PIG", "MINI_GOAT",
	}
	cagedMammalSpecies = []string{
		"HAMSTER", "GUINEA_PIG", "RABBIT", "RAT", "MOUSE", "FERRET", "HEDGEHOG", "CHINCHILLA",
	}
	nonCagedMammalSpecies   = []string{"DOG", "CAT", "MINI_PIG", "MINI_GOAT"}
	birdSpecies             = []string{"PARROT", "CANARY", "CHICKEN", "DUCK", "PIGEON"}
	terrariumReptileSpecies = []string{"IGUANA", "GECKO", "BEARDED_AGAMA", "SNAKE", "PYTHON"}
	aquaticSpecies          = []string{"FISH", "AXOLOTL", "FROG", "TURTLE"}
	invertebrateSpecies     = []string{"TARANTULA", "HERMIT_CRAB", "ANT_FARM", "SNAIL"}
)

// speciesUnion объединяет несколько групп видов (и/или отдельных видов) в
// один список применимости, без переписывания состава групп под каждое
// значение enum.
func speciesUnion(groups ...[]string) []string {
	out := make([]string, 0)
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// hygieneProcedureSpecies — применимость значений value.procedure (type=
// hygiene) к виду питомца. "other" отсутствует в карте — применим любому
// виду. "shedding" — тот же состав видов, что и применимость типа события
// molting (moltingSpecies), переиспользуется как единственный источник
// истины для этого набора видов.
var hygieneProcedureSpecies = map[string][]string{
	"bath":          speciesUnion(mammalSpecies, birdSpecies, terrariumReptileSpecies, []string{"TURTLE"}),
	"brushing":      mammalSpecies,
	"teeth":         mammalSpecies,
	"nails":         speciesUnion(mammalSpecies, []string{"TURTLE"}),
	"beak":          birdSpecies,
	"ears":          mammalSpecies,
	"shedding":      moltingSpecies,
	"antiparasitic": speciesUnion(mammalSpecies, birdSpecies, terrariumReptileSpecies, aquaticSpecies, invertebrateSpecies),
	"enclosure":     speciesUnion(cagedMammalSpecies, birdSpecies, terrariumReptileSpecies, aquaticSpecies, invertebrateSpecies),
	"water_change":  aquaticSpecies,
}

// feedingFoodSpecies — применимость значений value.food (type=feeding) к
// виду питомца. "other" отсутствует в карте — применим любому виду.
var feedingFoodSpecies = map[string][]string{
	"dry":         speciesUnion(mammalSpecies, birdSpecies, aquaticSpecies, []string{"HERMIT_CRAB"}),
	"wet":         speciesUnion(mammalSpecies, birdSpecies),
	"raw":         mammalSpecies,
	"homemade":    mammalSpecies,
	"live_prey":   speciesUnion(terrariumReptileSpecies, aquaticSpecies, []string{"TARANTULA"}),
	"frozen_prey": speciesUnion(terrariumReptileSpecies, aquaticSpecies),
	"insects":     speciesUnion([]string{"HEDGEHOG"}, birdSpecies, terrariumReptileSpecies, aquaticSpecies, []string{"TARANTULA", "ANT_FARM"}),
	"hay":         mammalSpecies,
	"grain":       speciesUnion(mammalSpecies, birdSpecies),
	"greens":      speciesUnion(mammalSpecies, birdSpecies, terrariumReptileSpecies, []string{"TURTLE", "HERMIT_CRAB", "SNAIL"}),
	"treat":       speciesUnion(mammalSpecies, birdSpecies, []string{"HERMIT_CRAB"}),
}

// activityKindSpecies — применимость значений value.kind при type=activity к
// виду питомца. Не путать с value.kind при type=temperature
// (EventTemperatureKindEnum) — тот словарь применимости по видам не имеет
// (Field.SpeciesByValue у temperature.kind не задан).
var activityKindSpecies = map[string][]string{
	"walk":       nonCagedMammalSpecies,
	"free_range": speciesUnion(cagedMammalSpecies, birdSpecies, terrariumReptileSpecies, []string{"TURTLE"}),
	"play":       speciesUnion(mammalSpecies, birdSpecies, terrariumReptileSpecies, invertebrateSpecies),
	"training":   speciesUnion(mammalSpecies, birdSpecies),
	"swim":       []string{"DOG", "DUCK"},
}

// excretionSpec собирает одинаковую по форме запись реестра для типов
// urine/defecation/vomit/diarrhea — они отличаются только значением type и
// применимостью к видам питомца.
func excretionSpec(eventType string, applicableSpecies []string) TypeSpec {
	return TypeSpec{
		Type:      eventType,
		ValueKind: KindCategory,
		Fields: []Field{
			{Name: "status", Type: FieldEnum, Required: true, Enum: excretionStatuses},
		},
		Metrics:         []Metric{{Key: "count", Aggregation: AggCount}},
		SplitField:      "status",
		ApplicableSpecies: applicableSpecies,
	}
}

// specs — реестр в порядке справочника типов события. Порядок значим: он
// определяет порядок серий в ответе GET /events/stats и множество типов по
// умолчанию.
var specs = []TypeSpec{
	{
		Type:      "weight",
		ValueKind: KindMeasure,
		Fields: []Field{
			{Name: "amount", Type: FieldNumber, Required: true, Min: 0.001, Max: 400},
		},
		Metrics: []Metric{
			{Key: "amount_avg", Field: "amount", Unit: unitKg, Aggregation: AggAvg},
			{Key: "amount_last", Field: "amount", Unit: unitKg, Aggregation: AggLast},
		},
		// weight — применим ко всем видам справочника (ApplicableSpecies: nil).
	},
	{
		Type:      "temperature",
		ValueKind: KindMeasure,
		Fields: []Field{
			{Name: "amount", Type: FieldNumber, Required: true, Min: 0, Max: 50},
			{Name: "kind", Type: FieldEnum, Required: true, Enum: temperatureKinds},
		},
		Metrics: []Metric{
			{Key: "amount_avg", Field: "amount", Unit: unitCelsius, Aggregation: AggAvg},
			{Key: "amount_last", Field: "amount", Unit: unitCelsius, Aggregation: AggLast},
		},
		SplitField:      "kind",
		ApplicableSpecies: allSpeciesExcept(coldBloodedExclusion...),
	},
	{
		Type:      "feeding",
		ValueKind: KindQuantity,
		Fields: []Field{
			{Name: "amount", Type: FieldNumber, Required: true, Min: 0.01, Max: 5000},
			{Name: "unit", Type: FieldEnum, Required: true, Enum: feedingUnits},
			{Name: "food", Type: FieldEnum, Required: true, Enum: feedingFoods, SpeciesByValue: feedingFoodSpecies},
		},
		Metrics: []Metric{
			{Key: "amount_sum", Field: "amount", Aggregation: AggSum},
		},
		SplitField:  "unit",
		SplitAsUnit: true,
	},
	{
		Type:      "water",
		ValueKind: KindQuantity,
		Fields: []Field{
			{Name: "amount", Type: FieldNumber, Required: true, Min: 0.1, Max: 5000},
		},
		Metrics: []Metric{
			{Key: "amount_sum", Field: "amount", Unit: unitMl, Aggregation: AggSum},
		},
		ApplicableSpecies: allSpeciesExcept(waterExclusion...),
	},
	{
		Type:      "activity",
		ValueKind: KindQuantity,
		Fields: []Field{
			{Name: "duration_min", Type: FieldNumber, Required: true, Min: 1, Max: 1440},
			{Name: "kind", Type: FieldEnum, Required: true, Enum: activityKinds, SpeciesByValue: activityKindSpecies},
			{Name: "distance_m", Type: FieldNumber, Min: 0, Max: 100000},
		},
		Metrics: []Metric{
			{Key: "duration_min_sum", Field: "duration_min", Unit: unitMinutes, Aggregation: AggSum},
			{Key: "distance_m_sum", Field: "distance_m", Unit: unitMeters, Aggregation: AggSum},
		},
		ApplicableSpecies: allSpeciesExcept(activityExclusion...),
	},
	{
		Type:      "sleep",
		ValueKind: KindQuantity,
		Fields: []Field{
			{Name: "duration_min", Type: FieldNumber, Required: true, Min: 1, Max: 1440},
		},
		Metrics: []Metric{
			{Key: "duration_min_sum", Field: "duration_min", Unit: unitMinutes, Aggregation: AggSum},
		},
		ApplicableSpecies: allSpeciesExcept(coldBloodedExclusion...),
	},
	{
		Type:      "medication",
		ValueKind: KindQuantity,
		Fields: []Field{
			{Name: "name", Type: FieldString, Required: true, MinLen: 1, MaxLen: 100},
			{Name: "dose_amount", Type: FieldNumber, Min: 0.001, Max: 10000, RequiredWith: "dose_unit"},
			{Name: "dose_unit", Type: FieldEnum, Enum: medicationDoseUnit, RequiredWith: "dose_amount"},
		},
		// Доза относится к конкретному препарату и его единице, поэтому
		// medication сворачивается в количество приёмов, а не в сумму доз.
		Metrics: []Metric{{Key: "count", Aggregation: AggCount}},
		// medication — применим ко всем видам справочника (ApplicableSpecies: nil).
	},
	{
		Type:      "hygiene",
		ValueKind: KindCategory,
		Fields: []Field{
			{Name: "procedure", Type: FieldEnum, Required: true, Enum: hygieneProcedures, SpeciesByValue: hygieneProcedureSpecies},
		},
		Metrics:    []Metric{{Key: "count", Aggregation: AggCount}},
		SplitField: "procedure",
		// hygiene — применим ко всем видам справочника (ApplicableSpecies: nil).
	},
	{
		Type:      "mood",
		ValueKind: KindCategory,
		Fields: []Field{
			{Name: "state", Type: FieldEnum, Required: true, Enum: moodStates},
		},
		Metrics:         []Metric{{Key: "count", Aggregation: AggCount}},
		SplitField:      "state",
		ApplicableSpecies: allSpeciesExcept(coldBloodedExclusion...),
	},
	excretionSpec("urine", urineSpecies),
	excretionSpec("defecation", allSpeciesExcept(defecationExclusion...)),
	excretionSpec("vomit", vomitSpecies),
	excretionSpec("diarrhea", allSpeciesExcept(diarrheaExclusion...)),
	{
		Type:      "other",
		ValueKind: KindLabel,
		Fields: []Field{
			{Name: "label", Type: FieldString, Required: true, MinLen: 1, MaxLen: 50},
		},
		// other — применим ко всем видам справочника (ApplicableSpecies: nil).
	},
	{
		Type:      "molting",
		ValueKind: KindCategory,
		Fields: []Field{
			{Name: "status", Type: FieldEnum, Required: true, Enum: moltingStatuses},
		},
		Metrics:         []Metric{{Key: "count", Aggregation: AggCount}},
		SplitField:      "status",
		ApplicableSpecies: moltingSpecies,
	},
	{
		Type:      "egg_laying",
		ValueKind: KindQuantity,
		Fields: []Field{
			{Name: "count", Type: FieldNumber, Required: true, Min: 1, Max: 200, Integer: true},
			// status — категориальный факт о кладке, но не зарегистрирован как
			// метрика (см. реестр метрик, правило «необязательное поле формы
			// value, не входящее в реестр метрик, не агрегируется»).
			{Name: "status", Type: FieldEnum, Enum: eggLayingStatuses},
		},
		Metrics: []Metric{
			{Key: "count_sum", Field: "count", Unit: unitPieces, Aggregation: AggSum},
		},
		ApplicableSpecies: eggLayingSpecies,
	},
	{
		Type: "water_quality",
		// ValueKind типа целиком: смешанный, у большинства метрик — measure;
		// changed_volume_ml переопределяет ValueKind на уровне метрики (см.
		// Metric.ValueKind).
		ValueKind: KindMeasure,
		Fields: []Field{
			{Name: "temperature_c", Type: FieldNumber, Min: 0, Max: 40},
			{Name: "ph", Type: FieldNumber, Min: 0, Max: 14},
			{Name: "ammonia_ppm", Type: FieldNumber, Min: 0, Max: 10},
			{Name: "changed_volume_ml", Type: FieldNumber, Min: 0, Max: 200000},
		},
		Metrics: []Metric{
			{Key: "temperature_c_avg", Field: "temperature_c", Unit: unitCelsius, Aggregation: AggAvg},
			{Key: "temperature_c_last", Field: "temperature_c", Unit: unitCelsius, Aggregation: AggLast},
			{Key: "ph_avg", Field: "ph", Unit: unitPh, Aggregation: AggAvg},
			{Key: "ph_last", Field: "ph", Unit: unitPh, Aggregation: AggLast},
			{Key: "ammonia_ppm_avg", Field: "ammonia_ppm", Unit: unitPpm, Aggregation: AggAvg},
			{Key: "ammonia_ppm_last", Field: "ammonia_ppm", Unit: unitPpm, Aggregation: AggLast},
			{Key: "changed_volume_ml_sum", Field: "changed_volume_ml", Unit: unitMl, Aggregation: AggSum, ValueKind: KindQuantity},
		},
		// Хотя бы одно из четырёх полей обязано быть передано — пустое value
		// без единого показателя не несёт факта.
		AtLeastOneOf:    []string{"temperature_c", "ph", "ammonia_ppm", "changed_volume_ml"},
		ApplicableSpecies: waterQualitySpecies,
	},
	{
		Type:      "heat_cycle",
		ValueKind: KindCategory,
		Fields: []Field{
			{Name: "phase", Type: FieldEnum, Required: true, Enum: heatCyclePhases},
		},
		Metrics:         []Metric{{Key: "count", Aggregation: AggCount}},
		SplitField:      "phase",
		ApplicableSpecies: heatCycleSpecies,
	},
}

var specByType = func() map[string]TypeSpec {
	m := make(map[string]TypeSpec, len(specs))
	for _, s := range specs {
		m[s.Type] = s
	}
	return m
}()

// Spec возвращает запись реестра для типа события.
func Spec(eventType string) (TypeSpec, bool) {
	s, ok := specByType[eventType]
	return s, ok
}

// IsValidType сообщает, входит ли значение в справочник типов события.
func IsValidType(eventType string) bool {
	_, ok := specByType[eventType]
	return ok
}

// Types возвращает все типы события в порядке справочника.
func Types() []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Type)
	}
	return out
}

// AggregatableTypes возвращает типы, по которым строятся графики (все, кроме
// value_kind=label) — множество types по умолчанию для GET /events/stats.
func AggregatableTypes() []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		if s.Aggregatable() {
			out = append(out, s.Type)
		}
	}
	return out
}
