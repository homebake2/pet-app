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
	valid := []string{"weight", "temperature", "feeding", "water", "activity", "sleep", "medication", "hygiene", "mood", "urine", "defecation", "vomit", "diarrhea", "other"}
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
