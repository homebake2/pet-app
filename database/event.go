package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"myauthservice/models"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// EventFull — событие вместе с его видимыми (не мягко удалёнными)
// питомцами. Запись одна на всех питомцев; у записи без видимых питомцев
// список Pets пуст.
type EventFull struct {
	Event models.EventDB
	Pets  []LinkedPet
}

// InsertEvent — InsertEventWith в собственной транзакции: строка event и
// связи event_pet создаются атомарно.
func InsertEvent(userID string, petIDs []uuid.UUID, fields models.EventFields, idempotencyKey string) (uuid.UUID, error) {
	var eventID uuid.UUID
	err := RunInTx(func(tx *sql.Tx) error {
		id, err := InsertEventWith(tx, userID, petIDs, fields, idempotencyKey)
		eventID = id
		return err
	})
	return eventID, err
}

// InsertEventWith вставляет одно событие владельца userID и по одной связи
// event_pet на каждого питомца из petIDs (в порядке petIDs). Выполняется на
// произвольном dbExecutor: внутри транзакции событие создаётся вместе с
// другими записями (отметка напоминания «выполнено», замена напоминания,
// вакцинация, перенос локальных данных). idempotencyKey — пустая строка
// означает «заголовок Idempotency-Key не передан» (NULL в БД).
func InsertEventWith(exec dbExecutor, userID string, petIDs []uuid.UUID, fields models.EventFields, idempotencyKey string) (uuid.UUID, error) {
	query := `
        INSERT INTO event (
            user_id, date_time, type, notes, value, idempotency_key
        ) VALUES (
            $1, $2, $3, $4, $5, $6
        ) RETURNING id
    `

	dateTime, err := time.Parse(time.RFC3339, fields.Date)
	if err != nil {
		return uuid.Nil, err
	}

	var notes sql.NullString
	if fields.Notes != nil {
		notes = sql.NullString{String: *fields.Notes, Valid: true}
	}

	var key sql.NullString
	if idempotencyKey != "" {
		key = sql.NullString{String: idempotencyKey, Valid: true}
	}

	var eventID uuid.UUID
	// value передаётся строкой: столбец event.value имеет тип jsonb, а
	// []byte драйвер закодировал бы как bytea.
	err = exec.QueryRow(query, userID, dateTime, fields.Type, notes, string(fields.Value), key).Scan(&eventID)
	if err != nil {
		log.Println("InsertEvent error:", err)
		return uuid.Nil, err
	}

	if err := eventPetLinks.insert(exec, eventID, petIDs); err != nil {
		log.Println("InsertEvent pets error:", err)
		return uuid.Nil, err
	}

	return eventID, nil
}

// EventInsert — событие для пакетной вставки InsertEventsWith: поля события и
// питомцы, к которым оно привязывается (в порядке привязки).
type EventInsert struct {
	PetIDs []uuid.UUID
	Fields models.EventFields
}

// InsertEventsWith вставляет события владельца userID без ключа
// идемпотентности и их связи event_pet двумя запросами независимо от числа
// событий (вставка по одному стоила бы двух обращений к базе на каждое,
// а перенос локальных данных присылает сотни событий). Возвращает id событий
// в порядке events; id выдаются на стороне сервиса.
func InsertEventsWith(exec dbExecutor, userID string, events []EventInsert) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, len(events))
	if len(events) == 0 {
		return ids, nil
	}

	idStrs := make([]string, len(events))
	dates := make([]string, len(events))
	types := make([]string, len(events))
	notes := make([]sql.NullString, len(events))
	values := make([]string, len(events))
	var linkEventIDs, linkPetIDs []string
	var linkPositions []int64

	for i, event := range events {
		dateTime, err := time.Parse(time.RFC3339, event.Fields.Date)
		if err != nil {
			return nil, err
		}
		ids[i] = uuid.New()
		idStrs[i] = ids[i].String()
		dates[i] = dateTime.UTC().Format(time.RFC3339Nano)
		types[i] = event.Fields.Type
		if event.Fields.Notes != nil {
			notes[i] = sql.NullString{String: *event.Fields.Notes, Valid: true}
		}
		// value передаётся строкой и приводится к jsonb в запросе — см.
		// InsertEventWith.
		values[i] = string(event.Fields.Value)
		for position, petID := range event.PetIDs {
			linkEventIDs = append(linkEventIDs, idStrs[i])
			linkPetIDs = append(linkPetIDs, petID.String())
			linkPositions = append(linkPositions, int64(position+1))
		}
	}

	if _, err := exec.Exec(`
		INSERT INTO event (id, user_id, date_time, type, notes, value)
		SELECT t.id, $1::uuid, t.date_time, t.type, t.notes, t.value::jsonb
		FROM unnest($2::uuid[], $3::timestamptz[], $4::text[], $5::text[], $6::text[])
		     AS t(id, date_time, type, notes, value)
	`, userID, pq.Array(idStrs), pq.Array(dates), pq.Array(types), pq.Array(notes), pq.Array(values)); err != nil {
		log.Println("InsertEvents error:", err)
		return nil, err
	}

	if len(linkEventIDs) > 0 {
		if _, err := exec.Exec(`
			INSERT INTO event_pet (event_id, pet_id, position)
			SELECT t.event_id, t.pet_id, t.position
			FROM unnest($1::uuid[], $2::uuid[], $3::int[]) AS t(event_id, pet_id, position)
		`, pq.Array(linkEventIDs), pq.Array(linkPetIDs), pq.Array(linkPositions)); err != nil {
			log.Println("InsertEvents pets error:", err)
			return nil, err
		}
	}

	return ids, nil
}

const eventColumns = `e.id, e.user_id, e.date_time, e.type, e.notes, e.value`

func scanEventRows(rows *sql.Rows) ([]models.EventDB, error) {
	defer rows.Close()
	var events []models.EventDB
	for rows.Next() {
		var eventDB models.EventDB
		if err := rows.Scan(&eventDB.ID, &eventDB.UserID, &eventDB.Date, &eventDB.Type, &eventDB.Notes, &eventDB.Value); err != nil {
			return nil, err
		}
		events = append(events, eventDB)
	}
	return events, rows.Err()
}

// withEventPets дополняет события их видимыми питомцами одним запросом.
func withEventPets(exec dbExecutor, events []models.EventDB) ([]EventFull, error) {
	ids := make([]uuid.UUID, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	pets, err := eventPetLinks.visiblePets(exec, ids)
	if err != nil {
		return nil, err
	}
	full := make([]EventFull, len(events))
	for i, e := range events {
		full[i] = EventFull{Event: e, Pets: pets[e.ID]}
	}
	return full, nil
}

// getEventFullOne читает единственное событие запросом query (колонки —
// eventColumns) и дополняет его видимыми питомцами. sql.ErrNoRows — строки
// нет.
func getEventFullOne(exec dbExecutor, query string, args ...any) (*EventFull, error) {
	var eventDB models.EventDB
	err := exec.QueryRow(query, args...).Scan(&eventDB.ID, &eventDB.UserID, &eventDB.Date, &eventDB.Type, &eventDB.Notes, &eventDB.Value)
	if err != nil {
		return nil, err
	}
	full, err := withEventPets(exec, []models.EventDB{eventDB})
	if err != nil {
		return nil, err
	}
	return &full[0], nil
}

// GetEventByUserIDAndIdempotencyKey ищет событие пользователя по ранее
// использованному Idempotency-Key (см. страницу "Добавление события —
// Backend"). Мягко удалённое событие не освобождает ключ. Возвращает
// sql.ErrNoRows, если такого события нет.
func GetEventByUserIDAndIdempotencyKey(userID string, idempotencyKey string) (*EventFull, error) {
	return getEventFullOne(DB, `
	SELECT `+eventColumns+`
	FROM event e
	WHERE e.user_id = $1 AND e.idempotency_key = $2
	`, userID, idempotencyKey)
}

// GetEventFullByID возвращает неудалённое событие по id вместе с видимыми
// питомцами независимо от владельца (для чтения только что созданной записи).
func GetEventFullByID(eventID uuid.UUID) (*EventFull, error) {
	return getEventFullOne(DB, `
	SELECT `+eventColumns+`
	FROM event e
	WHERE e.id = $1 AND e.deleted_at IS NULL
	`, eventID)
}

// GetEventForUserWith возвращает неудалённое событие, видимое userID, вместе
// с его видимыми питомцами. Событие видно, если у него есть хотя бы один
// видимый питомец и пользователю доступен каждый из них (владение или
// выданный доступ любой роли); автор события (event.user_id) доступ не
// определяет. allowNoVisiblePets=true дополнительно допускает событие без
// видимых питомцев (удаление очищает историю мягко удалённого питомца) — его
// получает автор события и владелец любого из привязанных питомцев. lock=true
// блокирует строку события до конца транзакции. sql.ErrNoRows — события нет,
// оно удалено либо недоступно.
func GetEventForUserWith(exec dbExecutor, eventID uuid.UUID, userID string, lock bool, allowNoVisiblePets bool) (*EventFull, error) {
	access := eventPetLinks.readableClause("e", "id", "$2")
	if allowNoVisiblePets {
		access = `(` + access + ` OR (NOT ` + eventPetLinks.visibleExistsClause("e", "id") + ` AND (
			e.user_id = $2 OR EXISTS (
				SELECT 1 FROM event_pet hidden_link JOIN pet hidden_pet ON hidden_pet.id = hidden_link.pet_id
				WHERE hidden_link.event_id = e.id AND hidden_pet.user_id = $2
			)
		)))`
	}
	query := `
	SELECT ` + eventColumns + `
	FROM event e
	WHERE e.id = $1 AND e.deleted_at IS NULL AND ` + access
	if lock {
		query += ` FOR UPDATE OF e`
	}
	return getEventFullOne(exec, query, eventID, userID)
}

// GetEventForWriteWith — GetEventForUserWith для операций записи: кроме
// видимости требует роль owner или full у каждого видимого питомца события,
// иначе ErrForbidden (событие без видимых питомцев проверки ролей не
// требует).
func GetEventForWriteWith(exec dbExecutor, eventID uuid.UUID, userID string, lock bool, allowNoVisiblePets bool) (*EventFull, error) {
	eventFull, err := GetEventForUserWith(exec, eventID, userID, lock, allowNoVisiblePets)
	if err != nil {
		return nil, err
	}
	if err := EnsureWritableWith(exec, userID, eventFull.Pets); err != nil {
		return nil, err
	}
	return eventFull, nil
}

// UpdateEvent - функция обновления события
func UpdateEvent(eventID uuid.UUID, dateTime *time.Time, eventType *string, notes *string, value *json.RawMessage) error {
	return UpdateEventWith(DB, eventID, dateTime, eventType, notes, value)
}

// UpdateEventWith — то же, что UpdateEvent, но выполняется на произвольном
// dbExecutor (в том числе внутри транзакции).
func UpdateEventWith(exec dbExecutor, eventID uuid.UUID, dateTime *time.Time, eventType *string, notes *string, value *json.RawMessage) error {
	setParts := []string{}
	args := []any{}
	argID := 1

	add := func(field string, value any) {
		setParts = append(setParts, field+" = $"+strconv.Itoa(argID))
		args = append(args, value)
		argID++
	}

	if dateTime != nil {
		add("date_time", *dateTime)
	}
	if eventType != nil {
		add("type", *eventType)
	}
	if notes != nil {
		if *notes == "" {
			add("notes", sql.NullString{Valid: false})
		} else {
			add("notes", sql.NullString{String: *notes, Valid: true})
		}
	}
	if value != nil {
		// value заменяется целиком (слияние вложенных полей не поддерживается).
		add("value", string(*value))
	}

	if len(setParts) == 0 {
		return nil // нечего обновлять
	}

	query := fmt.Sprintf(`
		UPDATE event
		SET %s
		WHERE id = $%d
	`, strings.Join(setParts, ", "), argID)
	args = append(args, eventID)

	_, err := exec.Exec(query, args...)
	if err != nil {
		log.Println("UpdateEvent error:", err)
		return err
	}

	return nil
}

// GetEventByIDForUpdateWith — получить неудалённое событие по id (без
// проверки владельца и питомцев) на произвольном dbExecutor, в том числе
// внутри транзакции: используется для события, на которое ссылается
// вакцинация.
func GetEventByIDForUpdateWith(exec dbExecutor, eventID uuid.UUID) (*models.EventDB, error) {
	query := `
	SELECT ` + eventColumns + `
	FROM event e
	WHERE e.id = $1 AND e.deleted_at IS NULL
	`

	var eventDB models.EventDB
	err := exec.QueryRow(query, eventID).Scan(
		&eventDB.ID,
		&eventDB.UserID,
		&eventDB.Date,
		&eventDB.Type,
		&eventDB.Notes,
		&eventDB.Value,
	)
	if err != nil {
		return nil, err
	}

	return &eventDB, nil
}

// DeleteEvent - функция мягкого удаления события (устанавливает deleted_at,
// строка физически не удаляется), аналогично database.DeletePet.
func DeleteEvent(eventID uuid.UUID) error {
	return DeleteEventWith(DB, eventID)
}

// DeleteEventWith — DeleteEvent на произвольном dbExecutor.
func DeleteEventWith(exec dbExecutor, eventID uuid.UUID) error {
	query := `UPDATE event SET deleted_at = $1 WHERE id = $2 AND deleted_at IS NULL`
	now := time.Now().UTC()
	result, err := exec.Exec(query, now, eventID)
	if err != nil {
		log.Println("DeleteEvent error:", err)
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// GetEventsByPetIDAndDateRange - получить все неудалённые события, привязанные
// к питомцу (через event_pet, в том числе общие с другими питомцами), чей
// date_time попадает в полуоткрытый интервал моментов времени [start, end).
// Границы уже вычислены вызывающим кодом из календарных дат в часовом поясе
// клиента (см. handlers.localDaysBounds и страницу "Просмотр календаря —
// Backend"). В каждом событии отдаются все его видимые питомцы.
func GetEventsByPetIDAndDateRange(petID uuid.UUID, userID string, start, end time.Time) ([]EventFull, error) {
	query := `
	SELECT ` + eventColumns + `
	FROM event e
	JOIN event_pet ep ON ep.event_id = e.id
	WHERE ep.pet_id = $1
	AND e.deleted_at IS NULL
	AND e.date_time >= $3
	AND e.date_time < $4
	AND ` + eventPetLinks.readableClause("e", "id", "$2") + `
	ORDER BY e.date_time, e.id
	`
	rows, err := DB.Query(query, petID, userID, start.UTC(), end.UTC())
	if err != nil {
		log.Println("GetEventsByPetIDAndDateRange error:", err)
		return nil, err
	}
	events, err := scanEventRows(rows)
	if err != nil {
		log.Println("GetEventsByPetIDAndDateRange scan error:", err)
		return nil, err
	}
	return withEventPets(DB, events)
}

// CheckEventFileOwnership проверяет владение событием по правилу,
// зарегистрированному для owner_type = "event_file" в реестре типов
// владельцев generic-механизма файлов сущностей (см. handlers/files.go,
// «Файлы события — Backend»): событие не мягко удалено, имеет хотя бы одного
// не мягко удалённого питомца и доступно userID по всем видимым питомцам — то
// же правило, что и при создании/редактировании события (в отличие от
// удаления самого события, где мягко удалённые питомцы операцию не
// блокируют). Загрузка, подтверждение и удаление файла — операции записи:
// нужна роль owner или full у каждого видимого питомца события, иначе
// AccessReadOnly.
func CheckEventFileOwnership(eventID uuid.UUID, userID string) (AccessCheck, error) {
	eventFull, err := GetEventForUserWith(DB, eventID, userID, false, false)
	if err == sql.ErrNoRows {
		return AccessNone, nil
	}
	if err != nil {
		return AccessNone, err
	}
	return accessCheckForPets(DB, userID, eventFull.Pets)
}

// CountEventsByUserIDGroupedByDay возвращает количество неудалённых событий
// (фактов), видимых userID (есть видимый питомец и доступны все видимые
// питомцы), и чей date_time попадает в полуоткрытый интервал [start, end), сгруппированное
// по календарному дню события в часовом поясе loc (YYYY-MM-DD) — см.
// «Просмотр календаря — Backend», GET /activities/calendar. Событие с
// несколькими питомцами считается один раз. Группировка выполняется в Go, а
// не через AT TIME ZONE в SQL: так часовой пояс интерпретируется одной и той
// же базой tzdata, которой он был провалидирован (time.LoadLocation). Дни без
// событий отсутствуют в результирующей map — вызывающий код достраивает
// диапазон нулями.
func CountEventsByUserIDGroupedByDay(userID string, start, end time.Time, loc *time.Location) (map[string]int, error) {
	query := `
	SELECT e.date_time
	FROM event e
	WHERE e.deleted_at IS NULL
	AND e.date_time >= $2
	AND e.date_time < $3
	AND ` + eventPetLinks.userScopeClause("e", "id", "$1") + `
	AND ` + eventPetLinks.readableClause("e", "id", "$1")
	rows, err := DB.Query(query, userID, start.UTC(), end.UTC())
	if err != nil {
		log.Println("CountEventsByUserIDGroupedByDay error:", err)
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var dateTime time.Time
		if err := rows.Scan(&dateTime); err != nil {
			return nil, err
		}
		result[dateTime.In(loc).Format("2006-01-02")]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// GetEventsByUserIDInRange возвращает все неудалённые события, видимые
// userID (есть видимый питомец и доступны все видимые питомцы), и чей date_time
// попадает в полуоткрытый интервал [start, end) — для GET /activities/day это
// границы одних локальных суток клиента (см. handlers.localDaysBounds), —
// отсортированные по date_time, затем по id по возрастанию — см.
// «Просмотр календаря — Backend», GET /activities/day. Событие с несколькими
// питомцами присутствует один раз, со всеми видимыми питомцами.
func GetEventsByUserIDInRange(userID string, start, end time.Time) ([]EventFull, error) {
	query := `
	SELECT ` + eventColumns + `
	FROM event e
	WHERE e.deleted_at IS NULL
	AND e.date_time >= $2
	AND e.date_time < $3
	AND ` + eventPetLinks.userScopeClause("e", "id", "$1") + `
	AND ` + eventPetLinks.readableClause("e", "id", "$1") + `
	ORDER BY e.date_time ASC, e.id ASC
	`
	rows, err := DB.Query(query, userID, start.UTC(), end.UTC())
	if err != nil {
		log.Println("GetEventsByUserIDInRange error:", err)
		return nil, err
	}
	events, err := scanEventRows(rows)
	if err != nil {
		return nil, err
	}
	return withEventPets(DB, events)
}

// escapeLikePattern экранирует спецсимволы LIKE/ILIKE (%, _ и сам escape-
// символ \) в пользовательском вводе, чтобы его можно было безопасно
// подставить в шаблон 'ESCAPE '\” — иначе значения search вроде "50%"
// или "a_b" трактовались бы как wildcard-маски, а не как буквальная подстрока.
func escapeLikePattern(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}

// GetEventsByPetID - получить события, привязанные к питомцу (через
// event_pet), отсортированные по date_time по убыванию (сначала последние), с
// пагинацией limit/offset и фильтрами PetEventsFilter (см. "/pet/{id}/events"
// в open-api/spec.json): при непустом Search в выборку попадают только
// события, у которых notes, либо (для типов с собственным свободнотекстовым
// подполем) value->>'label' (тип other) или value->>'name' (типы medication и
// vaccination) содержат search (регистронезависимо); Types, From и To
// ограничивают тип и момент события. Незаданный фильтр не применяется. В
// каждом событии отдаются все его видимые питомцы.
func GetEventsByPetID(petID uuid.UUID, userID string, limit, offset int, filter PetEventsFilter) ([]EventFull, error) {
	where, filterArgs := petEventsWhere(filter, 5)
	query := `
	SELECT ` + eventColumns + `
	FROM event e
	JOIN event_pet ep ON ep.event_id = e.id
	WHERE ep.pet_id = $1
	AND e.deleted_at IS NULL
	AND ` + eventPetLinks.readableClause("e", "id", "$2") + `
	` + where + `
	ORDER BY e.date_time DESC, e.id DESC
	LIMIT $3 OFFSET $4
	`
	args := append([]any{petID, userID, limit, offset}, filterArgs...)

	rows, err := DB.Query(query, args...)
	if err != nil {
		log.Println("GetEventsByPetID error:", err)
		return nil, err
	}
	events, err := scanEventRows(rows)
	if err != nil {
		return nil, err
	}
	return withEventPets(DB, events)
}

// CountEventsByPetID возвращает общее количество неудалённых событий,
// привязанных к питомцу, подходящих под тот же search-фильтр, что и
// GetEventsByPetID, без учёта limit/offset — используется для поля total в
// ответе GET /pet/{id}/events.
func CountEventsByPetID(petID uuid.UUID, userID string, filter PetEventsFilter) (int, error) {
	where, filterArgs := petEventsWhere(filter, 3)
	query := `
	SELECT COUNT(*)
	FROM event e
	JOIN event_pet ep ON ep.event_id = e.id
	WHERE ep.pet_id = $1
	AND e.deleted_at IS NULL
	AND ` + eventPetLinks.readableClause("e", "id", "$2") + `
	` + where

	var total int
	if err := DB.QueryRow(query, append([]any{petID, userID}, filterArgs...)...).Scan(&total); err != nil {
		log.Println("CountEventsByPetID error:", err)
		return 0, err
	}

	return total, nil
}

// PetEventsFilter — необязательные фильтры GET /pet/{id}/events; все
// заданные условия объединяются по «И». Нулевое значение — «без фильтра».
type PetEventsFilter struct {
	// Search — подстрока (регистронезависимо) в notes и свободнотекстовых
	// подполях value (label, name).
	Search string
	// Types — типы события; событие подходит, если его тип входит в список.
	Types []string
	// From — включительная нижняя граница date_time (начало первого дня
	// периода в часовом поясе клиента); nil — без нижней границы.
	From *time.Time
	// To — исключительная верхняя граница date_time (начало суток после
	// последнего дня периода в часовом поясе клиента); nil — без верхней
	// границы.
	To *time.Time
}

// PetEventsAvailableFilters — агрегаты по всем не удалённым событиям питомца
// без учёта фильтров (available_filters в ответе GET /pet/{id}/events).
type PetEventsAvailableFilters struct {
	Total        int
	Types        []PetEventTypeCount
	DistinctDays int
}

// PetEventTypeCount — число событий питомца одного типа.
type PetEventTypeCount struct {
	Type  string
	Count int
}

// petEventsWhere возвращает условия фильтра (каждое с ведущим AND) и их
// аргументы; первый плейсхолдер условий получает номер firstIdx. Порядок
// условий и аргументов фиксирован: search, types, from, to.
func petEventsWhere(f PetEventsFilter, firstIdx int) (string, []any) {
	var clause strings.Builder
	var args []any
	next := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(firstIdx+len(args)-1)
	}

	if f.Search != "" {
		placeholder := next("%" + escapeLikePattern(f.Search) + "%")
		clause.WriteString(`AND (
		e.notes ILIKE ` + placeholder + ` ESCAPE '\' OR
		e.value ->> 'label' ILIKE ` + placeholder + ` ESCAPE '\' OR
		e.value ->> 'name' ILIKE ` + placeholder + ` ESCAPE '\'
	)
	`)
	}
	if len(f.Types) > 0 {
		clause.WriteString("AND e.type = ANY(" + next(pq.Array(f.Types)) + ")\n\t")
	}
	if f.From != nil {
		clause.WriteString("AND e.date_time >= " + next(f.From.UTC()) + "\n\t")
	}
	if f.To != nil {
		clause.WriteString("AND e.date_time < " + next(f.To.UTC()) + "\n\t")
	}
	return clause.String(), args
}

// GetPetEventsAvailableFilters считает агрегаты available_filters по всем не
// удалённым событиям питомца без учёта search/types/from/to: общее число,
// число событий по типам и число различных календарных дней в часовом поясе
// tzName (имя IANA, понятное и Go, и Postgres). Питомец должен быть уже
// проверен вызывающим как видимый; считаются только события, видимые userID.
func GetPetEventsAvailableFilters(petID uuid.UUID, userID string, tzName string) (*PetEventsAvailableFilters, error) {
	result := &PetEventsAvailableFilters{Types: []PetEventTypeCount{}}

	err := DB.QueryRow(`
	SELECT COUNT(*), COUNT(DISTINCT (e.date_time AT TIME ZONE $2)::date)
	FROM event e
	JOIN event_pet ep ON ep.event_id = e.id
	WHERE ep.pet_id = $1
	AND e.deleted_at IS NULL
	AND `+eventPetLinks.readableClause("e", "id", "$3")+`
	`, petID, tzName, userID).Scan(&result.Total, &result.DistinctDays)
	if err != nil {
		log.Println("GetPetEventsAvailableFilters totals error:", err)
		return nil, err
	}

	rows, err := DB.Query(`
	SELECT e.type, COUNT(*)
	FROM event e
	JOIN event_pet ep ON ep.event_id = e.id
	WHERE ep.pet_id = $1
	AND e.deleted_at IS NULL
	AND `+eventPetLinks.readableClause("e", "id", "$2")+`
	GROUP BY e.type
	ORDER BY COUNT(*) DESC, e.type ASC
	`, petID, userID)
	if err != nil {
		log.Println("GetPetEventsAvailableFilters types error:", err)
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var item PetEventTypeCount
		if err := rows.Scan(&item.Type, &item.Count); err != nil {
			return nil, err
		}
		result.Types = append(result.Types, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
