package eventreg

import (
	"myauthservice/openapi"
	"testing"
)

// Реестр обязан покрывать ровно тот справочник типов события, который описан
// контрактом: тип без записи в реестре существовать не может.
func TestRegistryCoversOpenAPIEventTypes(t *testing.T) {
	specTypes := []openapi.GetEventEnum{
		openapi.GetEventEnumWeight,
		openapi.GetEventEnumTemperature,
		openapi.GetEventEnumFeeding,
		openapi.GetEventEnumWater,
		openapi.GetEventEnumActivity,
		openapi.GetEventEnumSleep,
		openapi.GetEventEnumMedication,
		openapi.GetEventEnumHygiene,
		openapi.GetEventEnumMood,
		openapi.GetEventEnumUrine,
		openapi.GetEventEnumDefecation,
		openapi.GetEventEnumVomit,
		openapi.GetEventEnumDiarrhea,
		openapi.GetEventEnumOther,
		openapi.GetEventEnumMolting,
		openapi.GetEventEnumEggLaying,
		openapi.GetEventEnumWaterQuality,
		openapi.GetEventEnumHeatCycle,
	}

	if len(specTypes) != len(Types()) {
		t.Fatalf("контракт описывает %d типов события, реестр — %d", len(specTypes), len(Types()))
	}

	for _, eventType := range specTypes {
		spec, ok := Spec(string(eventType))
		if !ok {
			t.Errorf("тип %q отсутствует в реестре", eventType)
			continue
		}
		if len(spec.Fields) == 0 {
			t.Errorf("тип %q не описывает форму value", eventType)
		}
		if spec.Aggregatable() && len(spec.Metrics) == 0 {
			t.Errorf("агрегируемый тип %q не описывает метрик", eventType)
		}
	}
}

func TestIsValidType(t *testing.T) {
	valid := []string{"weight", "temperature", "feeding", "water", "activity", "sleep", "medication", "hygiene", "mood", "urine", "defecation", "vomit", "diarrhea", "other", "molting", "egg_laying", "water_quality", "heat_cycle"}
	for _, v := range valid {
		if !IsValidType(v) {
			t.Errorf("ожидался валидный тип события %q", v)
		}
	}

	invalid := []string{"", "WEIGHT", "food", "unknown", "label"}
	for _, v := range invalid {
		if IsValidType(v) {
			t.Errorf("ожидался невалидный тип события %q", v)
		}
	}
}

// other (value_kind=label) не агрегируется и не входит в множество типов по
// умолчанию для GET /events/stats.
func TestAggregatableTypesExcludesLabel(t *testing.T) {
	for _, eventType := range AggregatableTypes() {
		if eventType == "other" {
			t.Fatal("тип other не должен входить в множество агрегируемых типов")
		}
	}
	if len(AggregatableTypes()) != len(Types())-1 {
		t.Fatalf("ожидалось %d агрегируемых типов, получено %d", len(Types())-1, len(AggregatableTypes()))
	}
}

func TestSeriesForSplitsIncomparableValues(t *testing.T) {
	// Температура: раздельные серии по виду замера, две метрики на каждый вид.
	temperature := SeriesFor("temperature")
	if len(temperature) != 4 {
		t.Fatalf("ожидалось 4 серии для temperature, получено %d", len(temperature))
	}
	seen := map[string]bool{}
	for _, series := range temperature {
		if series.Category == "" {
			t.Error("серия temperature обязана указывать вид замера в category")
		}
		if series.Unit != unitCelsius {
			t.Errorf("единица серии temperature = %q, ожидалась %q", series.Unit, unitCelsius)
		}
		seen[series.Category+"/"+series.Metric] = true
	}
	for _, key := range []string{"body/amount_avg", "body/amount_last", "environment/amount_avg", "environment/amount_last"} {
		if !seen[key] {
			t.Errorf("не построена серия %s", key)
		}
	}

	// Кормление: единица — сама разделяющая величина, категории нет.
	feeding := SeriesFor("feeding")
	if len(feeding) != 4 {
		t.Fatalf("ожидалось 4 серии для feeding, получено %d", len(feeding))
	}
	for _, series := range feeding {
		if series.Category != "" {
			t.Errorf("серия feeding не должна иметь category, получено %q", series.Category)
		}
		if series.Unit == "" {
			t.Error("серия feeding обязана указывать единицу измерения")
		}
		if series.Aggregation != AggSum {
			t.Errorf("агрегация feeding = %q, ожидалась sum", series.Aggregation)
		}
	}

	// Активность: две метрики с разными единицами, одна серия на метрику.
	activity := SeriesFor("activity")
	if len(activity) != 2 {
		t.Fatalf("ожидалось 2 серии для activity, получено %d", len(activity))
	}

	// Лекарства: количество приёмов, а не сумма доз.
	medication := SeriesFor("medication")
	if len(medication) != 1 || medication[0].Aggregation != AggCount || medication[0].Field != "" {
		t.Fatalf("medication должен сворачиваться в количество приёмов, получено %+v", medication)
	}

	// Категориальный тип: по серии на каждое значение словаря.
	mood := SeriesFor("mood")
	if len(mood) != 6 {
		t.Fatalf("ожидалось 6 серий для mood, получено %d", len(mood))
	}

	if SeriesFor("other") != nil {
		t.Fatal("по типу other серии не строятся")
	}
}

// water_quality: смешанный value_kind — measure-метрики (среднее и
// последнее) соседствуют с quantity-метрикой (сумма), заданной на уровне
// самой метрики, а не всего типа.
func TestSeriesForWaterQualityMixedValueKind(t *testing.T) {
	series := SeriesFor("water_quality")
	byMetric := map[string]Series{}
	for _, s := range series {
		byMetric[s.Metric] = s
	}

	measureMetrics := []string{"temperature_c_avg", "temperature_c_last", "ph_avg", "ph_last", "ammonia_ppm_avg", "ammonia_ppm_last"}
	for _, key := range measureMetrics {
		s, ok := byMetric[key]
		if !ok {
			t.Fatalf("не построена серия %s", key)
		}
		if s.ValueKind != KindMeasure {
			t.Errorf("value_kind серии %s = %q, ожидался measure", key, s.ValueKind)
		}
	}

	volume, ok := byMetric["changed_volume_ml_sum"]
	if !ok {
		t.Fatal("не построена серия changed_volume_ml_sum")
	}
	if volume.ValueKind != KindQuantity {
		t.Errorf("value_kind серии changed_volume_ml_sum = %q, ожидался quantity", volume.ValueKind)
	}
	if volume.Aggregation != AggSum {
		t.Errorf("агрегация changed_volume_ml_sum = %q, ожидалась sum", volume.Aggregation)
	}
}

// Применимость типа события к виду питомца — таблица «Применимость типа
// события к виду питомца». OTHER применим ко всем типам без исключения.
func TestIsApplicableToSpecies(t *testing.T) {
	cases := []struct {
		eventType string
		species   string
		want      bool
	}{
		{"weight", "SNAIL", true},
		{"heat_cycle", "FISH", false},
		{"heat_cycle", "DOG", true},
		{"heat_cycle", "OTHER", true},
		{"urine", "PARROT", false},
		{"urine", "DOG", true},
		{"molting", "SNAKE", true},
		{"molting", "DOG", false},
		{"egg_laying", "CHICKEN", true},
		{"egg_laying", "DOG", false},
		{"water_quality", "FISH", true},
		{"water_quality", "DOG", false},
		{"temperature", "FISH", false},
		{"temperature", "DOG", true},
		{"unknown_type", "DOG", false},
	}
	for _, c := range cases {
		if got := IsApplicableToSpecies(c.eventType, c.species); got != c.want {
			t.Errorf("IsApplicableToSpecies(%q, %q) = %v, ожидалось %v", c.eventType, c.species, got, c.want)
		}
	}
}

// TestIsFieldValueApplicableToSpecies проверяет второй уровень применимости —
// значений вложенных enum-полей value (hygiene.procedure, feeding.food,
// activity.kind) к виду питомца, отдельно от применимости самого типа
// события (см. «Применимость значений вложенных enum к виду питомца»).
func TestIsFieldValueApplicableToSpecies(t *testing.T) {
	cases := []struct {
		eventType string
		field     string
		value     string
		species   string
		want      bool
	}{
		// hygiene.procedure
		{"hygiene", "procedure", "brushing", "FISH", false},
		{"hygiene", "procedure", "brushing", "DOG", true},
		{"hygiene", "procedure", "water_change", "FISH", true},
		{"hygiene", "procedure", "water_change", "DOG", false},
		{"hygiene", "procedure", "shedding", "SNAKE", true},
		{"hygiene", "procedure", "enclosure", "DOG", false},
		{"hygiene", "procedure", "enclosure", "HAMSTER", true},
		{"hygiene", "procedure", "other", "FISH", true},
		{"hygiene", "procedure", "brushing", "OTHER", true},
		// feeding.food
		{"feeding", "food", "live_prey", "DOG", false},
		{"feeding", "food", "live_prey", "SNAKE", true},
		{"feeding", "food", "raw", "DOG", true},
		{"feeding", "food", "insects", "HEDGEHOG", true},
		{"feeding", "food", "insects", "CAT", false},
		// activity.kind (не путать с temperature.kind — другой словарь)
		{"activity", "kind", "swim", "FISH", false},
		{"activity", "kind", "swim", "DOG", true},
		{"activity", "kind", "walk", "CAT", true},
		{"activity", "kind", "walk", "FISH", false},
		{"temperature", "kind", "body", "FISH", true},
		// поле/тип без правил применимости второго уровня
		{"unknown_type", "procedure", "brushing", "FISH", true},
	}
	for _, c := range cases {
		if got := IsFieldValueApplicableToSpecies(c.eventType, c.field, c.value, c.species); got != c.want {
			t.Errorf("IsFieldValueApplicableToSpecies(%q, %q, %q, %q) = %v, ожидалось %v", c.eventType, c.field, c.value, c.species, got, c.want)
		}
	}
}
