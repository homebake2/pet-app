package database

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// StatsAggregate — одна агрегируемая величина запроса агрегации: поле внутри
// value и способ свёртки. Field пустой означает метрику «количество событий».
type StatsAggregate struct {
	Field       string
	Aggregation string
}

// StatsQuery описывает агрегацию событий одного типа для GET /events/stats.
// Значения Type, SplitField, Bucket и Aggregates приходят из реестра метрик
// (пакет eventreg) и в SQL-выражения попадают только оттуда.
type StatsQuery struct {
	PetID      uuid.UUID
	Type       string
	SplitField string
	Bucket     string
	From       time.Time // включительно
	To         time.Time // исключительно
	Aggregates []StatsAggregate
}

// StatsRow — одна группа результата агрегации: интервал × значение
// разделяющего поля. Values параллелен StatsQuery.Aggregates.
type StatsRow struct {
	BucketStart time.Time
	SplitValue  sql.NullString
	Count       int
	Values      []sql.NullFloat64
}

var allowedBuckets = map[string]bool{
	"day":   true,
	"week":  true,
	"month": true,
}

// AggregateEvents сворачивает неудалённые события питомца одного типа в
// интервалы средствами БД (GROUP BY по date_trunc), опираясь на индекс
// event_pet_type_date_idx. Все события периода в память сервиса не
// выбираются: наружу отдаются только готовые группы.
func AggregateEvents(q StatsQuery) ([]StatsRow, error) {
	if !allowedBuckets[q.Bucket] {
		return nil, fmt.Errorf("AggregateEvents: недопустимый bucket %q", q.Bucket)
	}

	splitExpr := "NULL::text"
	if q.SplitField != "" {
		splitExpr = fmt.Sprintf("value->>%s", quoteLiteral(q.SplitField))
	}

	selectParts := []string{
		fmt.Sprintf("date_trunc('%s', date_time AT TIME ZONE 'UTC') AS bucket_start", q.Bucket),
		splitExpr + " AS split_value",
		"count(*) AS event_count",
	}
	for _, agg := range q.Aggregates {
		expr, err := aggregateExpr(agg)
		if err != nil {
			return nil, err
		}
		selectParts = append(selectParts, expr)
	}

	query := fmt.Sprintf(`
	SELECT %s
	FROM event
	WHERE pet_id = $1
	  AND type = $2
	  AND deleted_at IS NULL
	  AND date_time >= $3
	  AND date_time < $4
	GROUP BY 1, 2
	ORDER BY 1, 2
	`, strings.Join(selectParts, ",\n\t       "))

	rows, err := DB.Query(query, q.PetID, q.Type, q.From.UTC(), q.To.UTC())
	if err != nil {
		log.Println("AggregateEvents error:", err)
		return nil, err
	}
	defer rows.Close()

	var result []StatsRow
	for rows.Next() {
		row := StatsRow{Values: make([]sql.NullFloat64, len(q.Aggregates))}
		dest := make([]any, 0, 3+len(q.Aggregates))
		dest = append(dest, &row.BucketStart, &row.SplitValue, &row.Count)
		for i := range row.Values {
			dest = append(dest, &row.Values[i])
		}
		if err := rows.Scan(dest...); err != nil {
			log.Println("AggregateEvents scan error:", err)
			return nil, err
		}
		result = append(result, row)
	}

	if err := rows.Err(); err != nil {
		log.Println("AggregateEvents rows error:", err)
		return nil, err
	}

	return result, nil
}

// aggregateExpr строит SQL-выражение свёртки одной метрики. Поле value
// приводится к числу; отсутствующее поле даёт NULL и в avg/last не участвует,
// а в sum считается нулём (событие есть, метрика не заполнена).
func aggregateExpr(agg StatsAggregate) (string, error) {
	if agg.Aggregation == "count" {
		return "count(*)::float8", nil
	}

	if agg.Field == "" {
		return "", fmt.Errorf("aggregateExpr: агрегация %q требует поле value", agg.Aggregation)
	}
	field := fmt.Sprintf("NULLIF(value->>%s, '')::float8", quoteLiteral(agg.Field))

	switch agg.Aggregation {
	case "avg":
		return fmt.Sprintf("avg(%s)", field), nil
	case "sum":
		return fmt.Sprintf("sum(COALESCE(%s, 0))", field), nil
	case "last":
		return fmt.Sprintf("(array_agg(%s ORDER BY date_time DESC, id DESC))[1]", field), nil
	default:
		return "", fmt.Errorf("aggregateExpr: неизвестная агрегация %q", agg.Aggregation)
	}
}

// quoteLiteral оформляет имя поля value как строковый литерал SQL. Имена
// приходят из реестра метрик, но экранирование оставлено явным, чтобы
// построение запроса не зависело от этого допущения.
func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
