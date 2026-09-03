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

	// Границы длины строкового поля (FieldString), в символах.
	MinLen int
	MaxLen int

	// Допустимые значения enum-поля (FieldEnum).
	Enum []string

	// RequiredWith — имя поля, вместе с которым это поле передаётся:
	// одно без другого является ошибкой валидации (medication.dose_amount и
	// medication.dose_unit).
	RequiredWith string
}

// Metric описывает одну метрику типа события для графиков.
// Field — поле внутри value, по которому считается метрика; пустое значение
// означает метрику «количество событий» (medication, категориальные типы).
type Metric struct {
	Key         string
	Field       string
	Unit        string
	Aggregation Aggregation
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
)

// Единицы измерения метрик. Единица типа фиксирована конвенцией, кроме
// feeding (задаётся value.unit) и medication (доза не агрегируется).
const (
	unitKg      = "kg"
	unitCelsius = "°C"
	unitMl      = "ml"
	unitMinutes = "min"
	unitMeters  = "m"
)

// excretionSpec собирает одинаковую по форме запись реестра для типов
// urine/defecation/vomit/diarrhea — они отличаются только значением type.
func excretionSpec(eventType string) TypeSpec {
	return TypeSpec{
		Type:      eventType,
		ValueKind: KindCategory,
		Fields: []Field{
			{Name: "status", Type: FieldEnum, Required: true, Enum: excretionStatuses},
		},
		Metrics:    []Metric{{Key: "count", Aggregation: AggCount}},
		SplitField: "status",
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
		SplitField: "kind",
	},
	{
		Type:      "feeding",
		ValueKind: KindQuantity,
		Fields: []Field{
			{Name: "amount", Type: FieldNumber, Required: true, Min: 0.01, Max: 5000},
			{Name: "unit", Type: FieldEnum, Required: true, Enum: feedingUnits},
			{Name: "food", Type: FieldEnum, Required: true, Enum: feedingFoods},
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
	},
	{
		Type:      "activity",
		ValueKind: KindQuantity,
		Fields: []Field{
			{Name: "duration_min", Type: FieldNumber, Required: true, Min: 1, Max: 1440},
			{Name: "kind", Type: FieldEnum, Required: true, Enum: activityKinds},
			{Name: "distance_m", Type: FieldNumber, Min: 0, Max: 100000},
		},
		Metrics: []Metric{
			{Key: "duration_min_sum", Field: "duration_min", Unit: unitMinutes, Aggregation: AggSum},
			{Key: "distance_m_sum", Field: "distance_m", Unit: unitMeters, Aggregation: AggSum},
		},
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
	},
	{
		Type:      "hygiene",
		ValueKind: KindCategory,
		Fields: []Field{
			{Name: "procedure", Type: FieldEnum, Required: true, Enum: hygieneProcedures},
		},
		Metrics:    []Metric{{Key: "count", Aggregation: AggCount}},
		SplitField: "procedure",
	},
	{
		Type:      "mood",
		ValueKind: KindCategory,
		Fields: []Field{
			{Name: "state", Type: FieldEnum, Required: true, Enum: moodStates},
		},
		Metrics:    []Metric{{Key: "count", Aggregation: AggCount}},
		SplitField: "state",
	},
	excretionSpec("urine"),
	excretionSpec("defecation"),
	excretionSpec("vomit"),
	excretionSpec("diarrhea"),
	{
		Type:      "other",
		ValueKind: KindLabel,
		Fields: []Field{
			{Name: "label", Type: FieldString, Required: true, MinLen: 1, MaxLen: 50},
		},
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
