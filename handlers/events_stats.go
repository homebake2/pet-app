package handlers

import (
	"database/sql"
	"myauthservice/database"
	"myauthservice/eventreg"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"strings"
	"time"
)

// GetEventStatsHandler обрабатывает GET /events/stats — агрегированные ряды
// событий питомца для графиков динамики (см. "Графики динамики — Backend").
// Свёртка строится по реестру метрик (пакет eventreg) и выполняется в БД.
func GetEventStatsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	petID, ok := parseRequiredPetIDParam(w, r)
	if !ok {
		return
	}

	fromDate, toDate, ok := parseEventDateRange(w, r)
	if !ok {
		return
	}

	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Отсутствует обязательный параметр bucket")
		return
	}
	if !isValidStatsBucket(bucket) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение bucket (ожидается day, week или month)")
		return
	}

	loc, ok := parseTimeZoneParam(w, r)
	if !ok {
		return
	}

	types, msg := parseStatsTypes(r.URL.Query().Get("types"))
	if msg != "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	// Питомец должен существовать, принадлежать инициатору и не быть мягко
	// удалён — иначе 404, как и на остальных эндпоинтах чтения событий.
	pet, ok := resolveOwnedPet(w, petID, userID)
	if !ok {
		return
	}

	// from/to — локальные календарные даты часового пояса клиента (tz, по
	// умолчанию UTC), интервалы — локальные сутки/недели/месяцы этого пояса:
	// та же трактовка, что и у GET /activities. Полуоткрытый интервал
	// [начало суток from, начало суток, следующих за to); интервалы, частично
	// выходящие за период, событий вне периода не захватывают.
	bucketDates := statsBucketStarts(bucket, fromDate, toDate)
	bucketInstants := make([]time.Time, len(bucketDates))
	for i, date := range bucketDates {
		bucketInstants[i] = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	}
	rangeStart, rangeEnd := localDaysBounds(fromDate, toDate, loc)

	series := make([]models.EventStatsSeries, 0, len(types))
	for _, eventType := range types {
		spec, found := eventreg.Spec(eventType)
		if !found {
			continue
		}

		aggregates := make([]database.StatsAggregate, 0, len(spec.Metrics))
		for _, metric := range spec.Metrics {
			aggregates = append(aggregates, database.StatsAggregate{
				Field:       metric.Field,
				Aggregation: string(metric.Aggregation),
			})
		}

		rows, err := database.AggregateEvents(database.StatsQuery{
			PetID:        petID,
			Type:         spec.Type,
			SplitField:   spec.SplitField,
			BucketStarts: bucketInstants,
			From:         rangeStart,
			To:           rangeEnd,
			Aggregates:   aggregates,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка агрегации событий")
			return
		}

		series = append(series, buildSeries(spec, rows, bucketDates)...)
	}

	writeJSON(w, http.StatusOK, models.EventStatsResponse{
		PetName: pet.Name,
		Series:  series,
	})
}

func isValidStatsBucket(bucket string) bool {
	switch bucket {
	case "day", "week", "month":
		return true
	default:
		return false
	}
}

// parseStatsTypes разбирает query-параметр types. Пустое значение означает
// множество по умолчанию — все агрегируемые типы реестра. Неизвестный тип и
// тип с value_kind=label (other) дают сообщение об ошибке для ответа 400:
// молчаливое исключение неагрегируемого типа недопустимо.
func parseStatsTypes(raw string) ([]string, string) {
	if strings.TrimSpace(raw) == "" {
		return eventreg.AggregatableTypes(), ""
	}

	seen := make(map[string]bool)
	types := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		eventType := strings.TrimSpace(part)
		if eventType == "" {
			return nil, "Параметр types содержит пустое значение"
		}

		spec, found := eventreg.Spec(eventType)
		if !found {
			return nil, "Неизвестное значение типа события в параметре types: " + eventType
		}
		if !spec.Aggregatable() {
			return nil, "По типу события " + eventType + " график не строится: значение не агрегируется"
		}
		if seen[eventType] {
			continue
		}
		seen[eventType] = true
		types = append(types, eventType)
	}

	return types, ""
}

// statsBucketStarts перечисляет календарные даты начала всех интервалов,
// покрывающих период from..to. Интервал, частично выходящий за границы
// периода (первая и последняя неделя/месяц), включается целиком. Даты здесь —
// «чистые» календарные (полночь UTC используется только как носитель
// года/месяца/дня): моментом времени в поясе клиента они становятся в
// GetEventStatsHandler.
func statsBucketStarts(bucket string, fromDate, toDate time.Time) []time.Time {
	current := truncateToBucket(bucket, fromDate.UTC())
	last := truncateToBucket(bucket, toDate.UTC())

	var starts []time.Time
	for !current.After(last) {
		starts = append(starts, current)
		switch bucket {
		case "week":
			current = current.AddDate(0, 0, 7)
		case "month":
			current = current.AddDate(0, 1, 0)
		default:
			current = current.AddDate(0, 0, 1)
		}
	}

	return starts
}

// truncateToBucket приводит календарную дату к началу её интервала:
// сами сутки, понедельник календарной недели или первое число месяца.
func truncateToBucket(bucket string, date time.Time) time.Time {
	utc := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	switch bucket {
	case "week":
		weekday := int(utc.Weekday())
		if weekday == 0 {
			// time.Sunday == 0, а неделя начинается с понедельника.
			weekday = 7
		}
		return utc.AddDate(0, 0, -(weekday - 1))
	case "month":
		return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return utc
	}
}

// buildSeries раскладывает группы агрегации по сериям реестра и достраивает
// пустые интервалы: каждый интервал периода обязан присутствовать в ответе,
// а серия без единого события всё равно возвращается. bucketStarts —
// календарные даты начала интервалов; row.BucketIndex — индекс в этом срезе.
func buildSeries(spec eventreg.TypeSpec, rows []database.StatsRow, bucketStarts []time.Time) []models.EventStatsSeries {
	type groupKey struct {
		bucketIndex int
		splitValue  string
	}
	grouped := make(map[groupKey]database.StatsRow, len(rows))
	for _, row := range rows {
		key := groupKey{
			bucketIndex: row.BucketIndex,
			splitValue:  splitValueOf(row.SplitValue),
		}
		grouped[key] = row
	}

	metricIndex := make(map[string]int, len(spec.Metrics))
	for i, metric := range spec.Metrics {
		metricIndex[metric.Key] = i
	}

	specSeries := eventreg.SeriesFor(spec.Type)
	result := make([]models.EventStatsSeries, 0, len(specSeries))
	for _, item := range specSeries {
		buckets := make([]models.EventStatsBucket, 0, len(bucketStarts))
		for bucketIndex, start := range bucketStarts {
			bucket := models.EventStatsBucket{BucketStart: start.Format("2006-01-02")}

			row, found := grouped[groupKey{bucketIndex: bucketIndex, splitValue: item.SplitValue}]
			if found {
				bucket.Count = row.Count
				if index, ok := metricIndex[item.Metric]; ok && index < len(row.Values) && row.Values[index].Valid {
					value := row.Values[index].Float64
					bucket.Value = &value
				}
			}

			buckets = append(buckets, bucket)
		}

		result = append(result, models.EventStatsSeries{
			Type:        item.Type,
			Metric:      item.Metric,
			Category:    item.Category,
			Unit:        item.Unit,
			ValueKind:   string(item.ValueKind),
			Aggregation: string(item.Aggregation),
			Buckets:     buckets,
		})
	}

	return result
}

func splitValueOf(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
