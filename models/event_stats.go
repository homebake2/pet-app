package models

// EventStatsBucket — одна точка серии графика: интервал агрегации.
// Value=nil означает, что событий в интервале не было (пустые интервалы
// обязательны и не пропускаются, см. "Графики динамики — Backend").
type EventStatsBucket struct {
	BucketStart string   `json:"bucket_start"`
	Value       *float64 `json:"value"`
	Count       int      `json:"count"`
}

// EventStatsSeries — одна линия/набор столбцов графика.
type EventStatsSeries struct {
	Type        string             `json:"type"`
	Metric      string             `json:"metric"`
	Category    string             `json:"category,omitempty"`
	Unit        string             `json:"unit,omitempty"`
	ValueKind   string             `json:"value_kind"`
	Aggregation string             `json:"aggregation"`
	Buckets     []EventStatsBucket `json:"buckets"`
}

// EventStatsResponse — тело ответа GET /events/stats.
type EventStatsResponse struct {
	PetName string             `json:"pet_name"`
	Series  []EventStatsSeries `json:"series"`
}
