package handlers

import (
	"net/http"
	"time"
	// Встраивает базу часовых поясов IANA в бинарник: time.LoadLocation не
	// должен зависеть от наличия /usr/share/zoneinfo в окружении деплоя.
	_ "time/tzdata"

	"myauthservice/openapi"
)

// timeZoneErrorMessage — сообщение 400 для отсутствующего или некорректного
// часового пояса.
const timeZoneErrorMessage = "Обязательный параметр tz отсутствует или некорректен (ожидается имя IANA, например Europe/Moscow)"

// loadTimeZone разбирает имя часового пояса IANA (например,
// "Europe/Moscow"). Пустое имя, неизвестное имя и значение "Local" (пояс
// процесса сервера, а не клиента — для клиента бессмысленный) — ошибка:
// значения по умолчанию у пояса нет.
func loadTimeZone(name string) (*time.Location, bool) {
	if name == "" || name == "Local" {
		return nil, false
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}
	return loc, true
}

// parseTimeZoneParam разбирает обязательный query-параметр tz — имя
// часового пояса IANA, в котором клиент видит календарь. Календарные даты
// from/to/date эндпоинтов GET /activities, GET /activities/calendar и
// GET /activities/day трактуются как локальные даты этого пояса, а
// календарный день события определяется по его date_time, переведённому в
// этот пояс (см. "Просмотр календаря — Backend"). Тот же параметр принимают
// GET /events/stats, создание/редактирование прививок, лекарств и настроек
// напоминаний (и GET списка лекарств — для next_dose): календарные даты и
// время суток запроса там — местное время этого пояса. Отсутствующий
// параметр — ошибка валидации, а не молчаливая подстановка UTC. При ошибке
// сама пишет 400 и возвращает ok=false.
func parseTimeZoneParam(w http.ResponseWriter, r *http.Request) (loc *time.Location, ok bool) {
	loc, ok = loadTimeZone(r.URL.Query().Get("tz"))
	if !ok {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, timeZoneErrorMessage)
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
