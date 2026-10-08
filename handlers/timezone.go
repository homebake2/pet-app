package handlers

import (
	"net/http"
	"time"
	// Встраивает базу часовых поясов IANA в бинарник: time.LoadLocation не
	// должен зависеть от наличия /usr/share/zoneinfo в окружении деплоя.
	_ "time/tzdata"

	"myauthservice/openapi"
)

// parseTimeZoneParam разбирает необязательный query-параметр tz — имя
// часового пояса IANA (например, "Europe/Moscow"), в котором клиент видит
// календарь. Календарные даты from/to/date эндпоинтов GET /activities,
// GET /activities/calendar и GET /activities/day трактуются как локальные
// даты этого пояса, а календарный день события определяется по его
// date_time, переведённому в этот пояс (см. "Просмотр календаря — Backend").
// Тот же параметр принимают создание/редактирование прививок и курсов
// лекарств (и GET списка курсов — для next_dose): календарные даты и время
// суток запроса (event_time, times) там — местное время этого пояса, см.
// combineDateAndTime.
// Отсутствующий параметр означает UTC — прежнее поведение для клиентов,
// которые tz ещё не передают. При ошибке сама пишет 400 и возвращает
// ok=false.
func parseTimeZoneParam(w http.ResponseWriter, r *http.Request) (loc *time.Location, ok bool) {
	name := r.URL.Query().Get("tz")
	if name == "" {
		return time.UTC, true
	}

	// "Local" — пояс процесса сервера, а не клиента: time.LoadLocation его
	// принимает, но для клиента он бессмыслен.
	if name == "Local" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный часовой пояс tz (ожидается имя IANA, например Europe/Moscow)")
		return nil, false
	}

	loc, err := time.LoadLocation(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный часовой пояс tz (ожидается имя IANA, например Europe/Moscow)")
		return nil, false
	}

	return loc, true
}

// localDaysBounds переводит диапазон календарных дат [fromDate, toDate]
// (обе включительно, как их разбирает parseEventDateRange/
// parseSingleDateParam) в полуоткрытый интервал моментов времени
// [начало суток fromDate в loc, начало суток, следующих за toDate, в loc).
// Сутки берутся по локальному времени loc, поэтому в день перехода на
// летнее/зимнее время интервал корректно длится 23/25 часов.
func localDaysBounds(fromDate, toDate time.Time, loc *time.Location) (start, end time.Time) {
	start = time.Date(fromDate.Year(), fromDate.Month(), fromDate.Day(), 0, 0, 0, 0, loc)
	end = time.Date(toDate.Year(), toDate.Month(), toDate.Day()+1, 0, 0, 0, 0, loc)
	return start, end
}

// localDateKey — календарный день момента t в поясе loc (YYYY-MM-DD): ключ,
// по которому события группируются в ответах GET /activities и
// GET /activities/calendar.
func localDateKey(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01-02")
}
