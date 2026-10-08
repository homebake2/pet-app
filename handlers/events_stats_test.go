package handlers

import (
	"database/sql"
	"encoding/json"
	"myauthservice/models"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func statsPath(query string) string {
	return "/events/stats?pet_id=" + testPetID + "&" + query
}

// expectOwnedPet мокает проверку владения питомцем (существует, не удалён,
// принадлежит пользователю) — общий шаг всех успешных сценариев агрегации.
func expectOwnedPet(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	expectNoWeightEvent(mock)
}

func TestGetEventStatsHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodPost, statsPath("from=2024-01-01&to=2024-01-02&bucket=day"), nil, false))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestGetEventStatsHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-01&to=2024-01-02&bucket=day"), nil, false))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetEventStatsHandler_BadRequest(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"нет pet_id", "/events/stats?from=2024-01-01&to=2024-01-02&bucket=day"},
		{"pet_id не uuid", "/events/stats?pet_id=nope&from=2024-01-01&to=2024-01-02&bucket=day"},
		{"нет from", statsPath("to=2024-01-02&bucket=day")},
		{"нет to", statsPath("from=2024-01-01&bucket=day")},
		{"некорректная дата", statsPath("from=2024-13-01&to=2024-01-02&bucket=day")},
		{"from позже to", statsPath("from=2024-02-01&to=2024-01-01&bucket=day")},
		{"диапазон больше 366 дней", statsPath("from=2024-01-01&to=2025-06-01&bucket=day")},
		{"нет bucket", statsPath("from=2024-01-01&to=2024-01-02")},
		{"неизвестный bucket", statsPath("from=2024-01-01&to=2024-01-02&bucket=year")},
		{"неизвестный тип", statsPath("from=2024-01-01&to=2024-01-02&bucket=day&types=weight,food")},
		{"пустое значение в types", statsPath("from=2024-01-01&to=2024-01-02&bucket=day&types=weight,")},
		{"неагрегируемый тип other", statsPath("from=2024-01-01&to=2024-01-02&bucket=day&types=other")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			GetEventStatsHandler(w, eventRequest(t, http.MethodGet, c.path, nil, false))
			assert.Equal(t, http.StatusBadRequest, w.Code, c.name)
		})
	}
}

func TestGetEventStatsHandler_PetNotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-01&to=2024-01-02&bucket=day&types=weight"), nil, true))

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventStatsHandler_AggregationError(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	mock.ExpectQuery(`SELECT width_bucket`).WillReturnError(assertError)

	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-01&to=2024-01-02&bucket=day&types=weight"), nil, true))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// weight — value_kind=measure: две серии-метрики (среднее и последнее),
// интервал без событий присутствует с value=null и count=0.
func TestGetEventStatsHandler_MeasureSeriesWithEmptyBuckets(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	mock.ExpectQuery(`SELECT width_bucket`).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_number", "split_value", "event_count", "amount_avg", "amount_last"}).
			AddRow(1, nil, 2, 4.5, 5.0))

	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-01&to=2024-01-03&bucket=day&types=weight"), nil, true))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.EventStatsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Rex", resp.PetName)
	require.Len(t, resp.Series, 2)

	avg := resp.Series[0]
	assert.Equal(t, "weight", avg.Type)
	assert.Equal(t, "amount_avg", avg.Metric)
	assert.Equal(t, "measure", avg.ValueKind)
	assert.Equal(t, "avg", avg.Aggregation)
	assert.Equal(t, "kg", avg.Unit)
	assert.Empty(t, avg.Category)
	require.Len(t, avg.Buckets, 3)
	assert.Equal(t, "2024-01-01", avg.Buckets[0].BucketStart)
	require.NotNil(t, avg.Buckets[0].Value)
	assert.Equal(t, 4.5, *avg.Buckets[0].Value)
	assert.Equal(t, 2, avg.Buckets[0].Count)
	assert.Nil(t, avg.Buckets[1].Value)
	assert.Equal(t, 0, avg.Buckets[1].Count)
	assert.Equal(t, "2024-01-03", avg.Buckets[2].BucketStart)

	last := resp.Series[1]
	assert.Equal(t, "amount_last", last.Metric)
	assert.Equal(t, "last", last.Aggregation)
	require.NotNil(t, last.Buckets[0].Value)
	assert.Equal(t, 5.0, *last.Buckets[0].Value)

	require.NoError(t, mock.ExpectationsWereMet())
}

// temperature — раздельные серии по виду замера: тело и среда не сводятся в
// одно значение даже при одной единице измерения.
func TestGetEventStatsHandler_TemperatureSeriesSplitByKind(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	mock.ExpectQuery(`SELECT width_bucket`).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_number", "split_value", "event_count", "amount_avg", "amount_last"}).
			AddRow(1, "body", 1, 38.5, 38.5).
			AddRow(1, "environment", 2, 26.0, 27.0))

	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-01&to=2024-01-01&bucket=day&types=temperature"), nil, true))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.EventStatsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Series, 4)

	byKey := map[string]models.EventStatsSeries{}
	for _, series := range resp.Series {
		assert.Equal(t, "°C", series.Unit)
		byKey[series.Category+"/"+series.Metric] = series
	}

	body := byKey["body/amount_avg"]
	require.Len(t, body.Buckets, 1)
	require.NotNil(t, body.Buckets[0].Value)
	assert.Equal(t, 38.5, *body.Buckets[0].Value)
	assert.Equal(t, 1, body.Buckets[0].Count)

	environment := byKey["environment/amount_last"]
	require.NotNil(t, environment.Buckets[0].Value)
	assert.Equal(t, 27.0, *environment.Buckets[0].Value)
	assert.Equal(t, 2, environment.Buckets[0].Count)

	require.NoError(t, mock.ExpectationsWereMet())
}

// feeding — по серии на каждую единицу измерения; серия без событий всё равно
// возвращается («нет данных» отличимо от «такой метрики нет»).
func TestGetEventStatsHandler_FeedingSeriesPerUnit(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	mock.ExpectQuery(`SELECT width_bucket`).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_number", "split_value", "event_count", "amount_sum"}).
			AddRow(1, "g", 3, 210.0))

	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-01&to=2024-01-01&bucket=day&types=feeding"), nil, true))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.EventStatsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Series, 4)

	byUnit := map[string]models.EventStatsSeries{}
	for _, series := range resp.Series {
		assert.Equal(t, "quantity", series.ValueKind)
		assert.Equal(t, "sum", series.Aggregation)
		byUnit[series.Unit] = series
	}

	grams := byUnit["g"]
	require.NotNil(t, grams.Buckets[0].Value)
	assert.Equal(t, 210.0, *grams.Buckets[0].Value)

	pieces := byUnit["piece"]
	require.Len(t, pieces.Buckets, 1)
	assert.Nil(t, pieces.Buckets[0].Value)
	assert.Equal(t, 0, pieces.Buckets[0].Count)

	require.NoError(t, mock.ExpectationsWereMet())
}

// Категориальный тип: по серии на каждое значение словаря, значение
// интервала — количество событий этой категории.
func TestGetEventStatsHandler_CategorySeries(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	mock.ExpectQuery(`SELECT width_bucket`).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_number", "split_value", "event_count", "count"}).
			AddRow(1, "abnormal", 2, 2.0))

	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-01&to=2024-01-01&bucket=day&types=urine"), nil, true))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.EventStatsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Series, 2)

	for _, series := range resp.Series {
		assert.Equal(t, "category", series.ValueKind)
		assert.Equal(t, "count", series.Aggregation)
		assert.Empty(t, series.Unit)
		assert.NotEmpty(t, series.Category)
		if series.Category == "abnormal" {
			require.NotNil(t, series.Buckets[0].Value)
			assert.Equal(t, 2.0, *series.Buckets[0].Value)
		} else {
			assert.Nil(t, series.Buckets[0].Value)
		}
	}

	require.NoError(t, mock.ExpectationsWereMet())
}

// bucket=week: интервал, частично выходящий за период, включается целиком, а
// bucket_start — понедельник календарной недели.
func TestGetEventStatsHandler_WeekBucketsStartOnMonday(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	mock.ExpectQuery(`SELECT width_bucket`).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_number", "split_value", "event_count", "amount_sum"}))

	// 2024-01-04 — четверг, 2024-01-09 — вторник следующей недели.
	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-04&to=2024-01-09&bucket=week&types=water"), nil, true))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.EventStatsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Series, 1)
	require.Len(t, resp.Series[0].Buckets, 2)
	assert.Equal(t, "2024-01-01", resp.Series[0].Buckets[0].BucketStart)
	assert.Equal(t, "2024-01-08", resp.Series[0].Buckets[1].BucketStart)
	assert.Equal(t, "ml", resp.Series[0].Unit)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStatsBucketStarts(t *testing.T) {
	from := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)

	months := statsBucketStarts("month", from, to)
	require.Len(t, months, 3)
	assert.Equal(t, "2024-01-01", months[0].Format("2006-01-02"))
	assert.Equal(t, "2024-03-01", months[2].Format("2006-01-02"))

	days := statsBucketStarts("day", from, from)
	require.Len(t, days, 1)

	// Воскресенье относится к неделе, начавшейся в предыдущий понедельник.
	sunday := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, "2024-01-01", truncateToBucket("week", sunday).Format("2006-01-02"))
}

// expectStatsQuery мокает запрос агрегации и проверяет переданные в него
// границы периода и моменты начала интервалов (RFC3339, UTC).
func expectStatsQuery(mock sqlmock.Sqlmock, eventType, from, to string, bucketStarts []string) *sqlmock.ExpectedQuery {
	return mock.ExpectQuery(`SELECT width_bucket\(date_time, \$5::timestamptz\[\]\)`).
		WithArgs(uuid.MustParse(testPetID), eventType, mustRFC3339(from), mustRFC3339(to), pq.Array(bucketStarts))
}

func statsBucketStartsOf(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.EventStatsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Series)
	starts := make([]string, 0, len(resp.Series[0].Buckets))
	for _, bucket := range resp.Series[0].Buckets {
		starts = append(starts, bucket.BucketStart)
	}
	return starts
}

// Без tz — прежнее поведение: сутки по UTC.
func TestGetEventStatsHandler_DefaultTimeZoneIsUTC(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	expectStatsQuery(mock, "water", "2024-01-01T00:00:00Z", "2024-01-03T00:00:00Z",
		[]string{"2024-01-01T00:00:00Z", "2024-01-02T00:00:00Z"}).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_number", "split_value", "event_count", "amount_sum"}))

	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-01&to=2024-01-02&bucket=day&types=water"), nil, true))

	assert.Equal(t, []string{"2024-01-01", "2024-01-02"}, statsBucketStartsOf(t, w))
	require.NoError(t, mock.ExpectationsWereMet())
}

// UTC+3: локальные сутки начинаются в 21:00 UTC предыдущего дня; событие
// 2024-01-01T22:30Z относится ко 2 января по Москве (интервал №2).
func TestGetEventStatsHandler_DayBucketsInPositiveOffsetZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	expectStatsQuery(mock, "water", "2023-12-31T21:00:00Z", "2024-01-02T21:00:00Z",
		[]string{"2023-12-31T21:00:00Z", "2024-01-01T21:00:00Z"}).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_number", "split_value", "event_count", "amount_sum"}).
			AddRow(2, nil, 1, 250.0))

	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-01&to=2024-01-02&bucket=day&types=water&tz=Europe/Moscow"), nil, true))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.EventStatsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Series, 1)
	buckets := resp.Series[0].Buckets
	require.Len(t, buckets, 2)
	assert.Equal(t, "2024-01-01", buckets[0].BucketStart)
	assert.Equal(t, 0, buckets[0].Count)
	assert.Equal(t, "2024-01-02", buckets[1].BucketStart)
	assert.Equal(t, 1, buckets[1].Count)
	require.NotNil(t, buckets[1].Value)
	assert.Equal(t, 250.0, *buckets[1].Value)
	require.NoError(t, mock.ExpectationsWereMet())
}

// UTC-5 (America/Bogota, без перехода на летнее время): локальные сутки
// начинаются в 05:00 UTC.
func TestGetEventStatsHandler_DayBucketsInNegativeOffsetZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	expectStatsQuery(mock, "water", "2024-01-01T05:00:00Z", "2024-01-03T05:00:00Z",
		[]string{"2024-01-01T05:00:00Z", "2024-01-02T05:00:00Z"}).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_number", "split_value", "event_count", "amount_sum"}))

	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-01&to=2024-01-02&bucket=day&types=water&tz=America/Bogota"), nil, true))

	assert.Equal(t, []string{"2024-01-01", "2024-01-02"}, statsBucketStartsOf(t, w))
	require.NoError(t, mock.ExpectationsWereMet())
}

// Переход на летнее время (America/New_York, 2024-03-10): сутки 10 марта
// длятся 23 часа — граница до перехода в 05:00Z, после — в 04:00Z.
func TestGetEventStatsHandler_DayBucketsAcrossDST(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	expectStatsQuery(mock, "water", "2024-03-09T05:00:00Z", "2024-03-12T04:00:00Z",
		[]string{"2024-03-09T05:00:00Z", "2024-03-10T05:00:00Z", "2024-03-11T04:00:00Z"}).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_number", "split_value", "event_count", "amount_sum"}))

	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-03-09&to=2024-03-11&bucket=day&types=water&tz=America/New_York"), nil, true))

	assert.Equal(t, []string{"2024-03-09", "2024-03-10", "2024-03-11"}, statsBucketStartsOf(t, w))
	require.NoError(t, mock.ExpectationsWereMet())
}

// bucket=week в UTC+3: неделя начинается в локальную полночь понедельника
// (воскресенье 21:00Z); первая неделя, частично выходящая за период,
// включается целиком, но события раньше from в неё не попадают.
func TestGetEventStatsHandler_WeekBucketsInTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	// 2024-01-04 — четверг, 2024-01-09 — вторник следующей недели.
	expectStatsQuery(mock, "water", "2024-01-03T21:00:00Z", "2024-01-09T21:00:00Z",
		[]string{"2023-12-31T21:00:00Z", "2024-01-07T21:00:00Z"}).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_number", "split_value", "event_count", "amount_sum"}))

	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-04&to=2024-01-09&bucket=week&types=water&tz=Europe/Moscow"), nil, true))

	assert.Equal(t, []string{"2024-01-01", "2024-01-08"}, statsBucketStartsOf(t, w))
	require.NoError(t, mock.ExpectationsWereMet())
}

// bucket=month в UTC-5: месяц начинается в локальную полночь 1-го числа
// (05:00Z), а не в полночь UTC.
func TestGetEventStatsHandler_MonthBucketsInTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	expectStatsQuery(mock, "water", "2024-01-15T05:00:00Z", "2024-03-03T05:00:00Z",
		[]string{"2024-01-01T05:00:00Z", "2024-02-01T05:00:00Z", "2024-03-01T05:00:00Z"}).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_number", "split_value", "event_count", "amount_sum"}))

	w := httptest.NewRecorder()
	GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-15&to=2024-03-02&bucket=month&types=water&tz=America/Bogota"), nil, true))

	assert.Equal(t, []string{"2024-01-01", "2024-02-01", "2024-03-01"}, statsBucketStartsOf(t, w))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventStatsHandler_InvalidTimeZone(t *testing.T) {
	for _, tz := range []string{"Mars/Phobos", "Local"} {
		w := httptest.NewRecorder()
		GetEventStatsHandler(w, eventRequest(t, http.MethodGet, statsPath("from=2024-01-01&to=2024-01-02&bucket=day&types=water&tz="+tz), nil, false))
		assert.Equal(t, http.StatusBadRequest, w.Code, tz)
	}
}
