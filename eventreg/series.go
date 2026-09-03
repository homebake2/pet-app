package eventreg

// Series — одна серия графика (линия или набор столбцов), полученная
// разворачиванием типа события по реестру метрик: тип × значение
// разделяющего поля × метрика.
type Series struct {
	Type        string
	Metric      string
	Category    string // значение категории/вида замера; пусто, если серия одна
	Unit        string // единица измерения; пусто, если у метрики её нет
	ValueKind   ValueKind
	Aggregation Aggregation

	// SplitField/SplitValue — поле value и его значение, по которым события
	// отбираются в эту серию; SplitField пуст, если тип не разделяется.
	SplitField string
	SplitValue string

	// Field — поле value, по которому считается метрика; пусто для метрики
	// «количество событий».
	Field string
}

// SeriesFor разворачивает тип события в набор серий графика по реестру.
// Для неагрегируемого типа (value_kind=label) возвращает nil.
func SeriesFor(eventType string) []Series {
	spec, ok := Spec(eventType)
	if !ok || !spec.Aggregatable() {
		return nil
	}

	splitValues := spec.SplitValues()
	if len(splitValues) == 0 {
		splitValues = []string{""}
	}

	series := make([]Series, 0, len(splitValues)*len(spec.Metrics))
	for _, splitValue := range splitValues {
		for _, metric := range spec.Metrics {
			item := Series{
				Type:        spec.Type,
				Metric:      metric.Key,
				Unit:        metric.Unit,
				ValueKind:   spec.ValueKind,
				Aggregation: metric.Aggregation,
				Field:       metric.Field,
			}
			if splitValue != "" {
				item.SplitField = spec.SplitField
				item.SplitValue = splitValue
				if spec.SplitAsUnit {
					// feeding: разделяющее поле само является единицей
					// измерения серии (граммы, миллилитры, порции, штуки).
					item.Unit = splitValue
				} else {
					item.Category = splitValue
				}
			}
			series = append(series, item)
		}
	}

	return series
}
