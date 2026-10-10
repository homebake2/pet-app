package database

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
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
//
// BucketStarts — моменты начала всех интервалов периода по возрастанию
// (начала локальных суток/недель/месяцев в часовом поясе клиента). Границы
// интервалов вычисляет вызывающий код в Go и передаёт сюда готовыми
// моментами времени, а не именем пояса для AT TIME ZONE: так пояс
// интерпретируется одной и той же базой tzdata, которой он был
// провалидирован (time.LoadLocation), — как и у GET /activities/calendar
// (см. CountEventsByUserIDGroupedByDay).
type StatsQuery struct {
	PetID        uuid.UUID
	Type         string
	SplitField   string
	BucketStarts []time.Time
	From         time.Time // включительно
	To           time.Time // исключительно
	Aggregates   []StatsAggregate
}

// StatsRow — одна группа результата агрегации: интервал × значение
// разделяющего поля. BucketIndex — индекс интервала в
// StatsQuery.BucketStarts (с нуля). Values параллелен StatsQuery.Aggregates.
type StatsRow struct {
	BucketIndex int
	SplitValue  sql.NullString
	Count       int
	Values      []sql.NullFloat64
}

// AggregateEvents сворачивает неудалённые события, привязанные к питомцу
// (через event_pet), одного типа в интервалы средствами БД (GROUP BY по
// номеру интервала: width_bucket по массиву моментов начала интервалов),
// опираясь на индексы event_pet_pet_id_idx и event_type_date_idx. Измерительные
// типы привязаны к одному питомцу, поэтому ряды разных питомцев не смешиваются. Все события периода в память сервиса не
// выбираются: наружу отдаются только готовые группы.
func AggregateEvents(q StatsQuery) ([]StatsRow, error) {
	if len(q.BucketStarts) == 0 {
		return nil, fmt.Errorf("AggregateEvents: пустой список интервалов")
	}
	bucketStarts := make([]string, len(q.BucketStarts))
	for i, start := range q.BucketStarts {
		bucketStarts[i] = start.UTC().Format(time.RFC3339)
	}

	splitExpr := "NULL::text"
	if q.SplitField != "" {
		splitExpr = fmt.Sprintf("e.value->>%s", quoteLiteral(q.SplitField))
	}

	selectParts := []string{
		// width_bucket возвращает номер интервала с единицы: i, если
		// BucketStarts[i-1] <= date_time < BucketStarts[i].
		"width_bucket(e.date_time, $5::timestamptz[]) AS bucket_number",
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
	FROM event e
	JOIN event_pet ep ON ep.event_id = e.id
	WHERE ep.pet_id = $1
	  AND e.type = $2
	  AND e.deleted_at IS NULL
	  AND e.date_time >= $3
	  AND e.date_time < $4
	GROUP BY 1, 2
	ORDER BY 1, 2
	`, strings.Join(selectParts, ",\n\t       "))

	rows, err := DB.Query(query, q.PetID, q.Type, q.From.UTC(), q.To.UTC(), pq.Array(bucketStarts))
	if err != nil {
		log.Println("AggregateEvents error:", err)
		return nil, err
	}
	defer rows.Close()

	var result []StatsRow
	for rows.Next() {
		row := StatsRow{Values: make([]sql.NullFloat64, len(q.Aggregates))}
		dest := make([]any, 0, 3+len(q.Aggregates))
		var bucketNumber int
		dest = append(dest, &bucketNumber, &row.SplitValue, &row.Count)
		for i := range row.Values {
			dest = append(dest, &row.Values[i])
		}
		if err := rows.Scan(dest...); err != nil {
			log.Println("AggregateEvents scan error:", err)
			return nil, err
		}
		// Номер 0 (событие раньше первого интервала) фильтром date_time >= From
		// исключён: From не раньше начала первого интервала.
		if bucketNumber < 1 || bucketNumber > len(q.BucketStarts) {
			continue
		}
		row.BucketIndex = bucketNumber - 1
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
	field := fmt.Sprintf("NULLIF(e.value->>%s, '')::float8", quoteLiteral(agg.Field))

	switch agg.Aggregation {
	case "avg":
		return fmt.Sprintf("avg(%s)", field), nil
	case "sum":
		return fmt.Sprintf("sum(COALESCE(%s, 0))", field), nil
	case "last":
		return fmt.Sprintf("(array_agg(%s ORDER BY e.date_time DESC, e.id DESC))[1]", field), nil
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
