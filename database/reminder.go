// Напоминания: настройки напоминания (reminder_plan) и напоминания по
// моментам расписания (reminder). Функции с суффиксом With принимают
// dbExecutor и поэтому работают и вне транзакции, и внутри неё — составные
// операции (отметка «выполнено», замена, пересоздание расписания,
// удаление питомца) выполняются в одной транзакции через RunInTx.
//
// Настройки и напоминания удаляются жёстко: у них нет истории, которую нужно
// сохранять (исключение из общего правила soft-delete). Строки file их
// владельцев удаляются вместе с ними; функции удаления возвращают ключи
// объектов S3, на которые не осталось ссылок, — вызывающий код удаляет такие
// объекты после фиксации транзакции.
package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"myauthservice/models"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Owner types файлов настроек напоминания и напоминания (реестр владельцев
// generic-механизма файлов сущностей, см. handlers/files.go).
const (
	ReminderPlanFileOwnerType = "reminder_plan_file"
	ReminderFileOwnerType     = "reminder_file"
	// eventFileOwnerType — owner_type файлов события; дублирует константу
	// handlers.eventFileOwnerType, потому что пакет database не может
	// импортировать handlers.
	eventFileOwnerType = "event_file"
)

// RunInTx выполняет fn в транзакции БД: фиксирует при nil-ошибке и
// откатывает иначе.
func RunInTx(fn func(tx *sql.Tx) error) error {
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ReminderPlanFull — настройки напоминания вместе с их видимыми (не мягко
// удалёнными) питомцами и названием источника (лекарства либо вакцинации).
type ReminderPlanFull struct {
	models.ReminderPlanDB
	Pets        []LinkedPet
	SourceTitle sql.NullString
}

// ReminderFull — напоминание вместе с его настройками и видимыми питомцами
// настроек.
type ReminderFull struct {
	Reminder    models.ReminderDB
	Plan        models.ReminderPlanDB
	Pets        []LinkedPet
	SourceTitle sql.NullString
}

const reminderPlanColumns = `p.id, p.user_id, p.source, p.source_id, p.type, p.value, p.notes, p.frequency_type,
	p.weekdays, p.interval_days, p.times, p.start_date, p.end_date, p.tz, p.created_at`

const reminderPlanJoins = `
	LEFT JOIN medication med ON p.source = 'medication' AND med.id = p.source_id
	LEFT JOIN vaccination vac ON p.source = 'vaccination' AND vac.id = p.source_id`

// reminderPlanVisibleClause — условие «у настроек p есть хотя бы один не
// мягко удалённый питомец».
var reminderPlanVisibleClause = reminderPlanPetLinks.visibleExistsClause("p", "id")

func scanReminderPlan(scan func(dest ...any) error, suffix ...any) (models.ReminderPlanDB, error) {
	return scanReminderPlanWithPrefix(scan, nil, suffix)
}

// GetReminderPlanForUserWith находит настройки по id и проверяет, что они
// принадлежат userID (reminder_plan.user_id) и имеют хотя бы одного не мягко
// удалённого питомца. sql.ErrNoRows — настроек нет, они чужие либо у них нет
// видимых питомцев. lock=true блокирует строку настроек до конца транзакции.
func GetReminderPlanForUserWith(exec dbExecutor, planID uuid.UUID, userID string, lock bool) (*ReminderPlanFull, error) {
	query := `
		SELECT ` + reminderPlanColumns + `, COALESCE(med.name, vac.name)
		FROM reminder_plan p` + reminderPlanJoins + `
		WHERE p.id = $1 AND p.user_id = $2 AND ` + reminderPlanVisibleClause
	if lock {
		query += ` FOR UPDATE OF p`
	}
	var full ReminderPlanFull
	plan, err := scanReminderPlan(exec.QueryRow(query, planID, userID).Scan, &full.SourceTitle)
	if err != nil {
		return nil, err
	}
	full.ReminderPlanDB = plan
	pets, err := reminderPlanPetLinks.visiblePets(exec, []uuid.UUID{planID})
	if err != nil {
		return nil, err
	}
	full.Pets = pets[planID]
	return &full, nil
}

// GetReminderPlanOwnerUserID возвращает user_id владельца настроек
// независимо от того, есть ли у них видимые питомцы. sql.ErrNoRows —
// настроек с таким id нет. Используется для различения повтора создания
// (свои настройки с клиентским id) и занятого чужими настройками id.
func GetReminderPlanOwnerUserID(planID uuid.UUID) (string, error) {
	var userID string
	err := DB.QueryRow(`
		SELECT user_id::text FROM reminder_plan WHERE id = $1
	`, planID).Scan(&userID)
	return userID, err
}

// InsertReminderPlanWith вставляет настройки владельца plan.UserID, связи с
// питомцами petIDs (в порядке petIDs) и по одному напоминанию на каждый
// момент. Напоминания получают серверные id.
func InsertReminderPlanWith(exec dbExecutor, plan models.ReminderPlanDB, petIDs []uuid.UUID, moments []models.ReminderMoment) error {
	var weekdays sql.NullString
	if plan.Weekdays != nil {
		b, err := json.Marshal(plan.Weekdays)
		if err != nil {
			return err
		}
		weekdays = sql.NullString{String: string(b), Valid: true}
	}
	_, err := exec.Exec(`
		INSERT INTO reminder_plan (id, user_id, source, source_id, type, value, notes, frequency_type, weekdays, interval_days, times, start_date, end_date, tz)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, plan.ID, plan.UserID, plan.Source, plan.SourceID, plan.Type, string(plan.Value), plan.Notes, plan.FrequencyType,
		weekdays, plan.IntervalDays, pq.Array(plan.Times), plan.StartDate, plan.EndDate, plan.TZ)
	if err != nil {
		return err
	}
	if err := reminderPlanPetLinks.insert(exec, plan.ID, petIDs); err != nil {
		return err
	}
	_, err = InsertRemindersWith(exec, plan.ID, moments)
	return err
}

// SyncReminderPlanPetsWith приводит видимых питомцев настроек к желаемому
// набору desired: отвязывает видимых питомцев, не вошедших в набор, и
// привязывает отсутствующих. Напоминания и файлы настроек не пересоздаются.
func SyncReminderPlanPetsWith(exec dbExecutor, planID uuid.UUID, desired []uuid.UUID) error {
	return reminderPlanPetLinks.syncVisible(exec, planID, desired)
}

// SyncEventPetsWith приводит видимых питомцев события к желаемому набору
// desired: отвязывает видимых питомцев, не вошедших в набор (удаляется только
// связь), и привязывает отсутствующих. Связи с мягко удалёнными питомцами не
// затрагиваются.
func SyncEventPetsWith(exec dbExecutor, eventID uuid.UUID, desired []uuid.UUID) error {
	return eventPetLinks.syncVisible(exec, eventID, desired)
}

// InsertRemindersWith вставляет напоминания настроек planID по моментам и
// возвращает их id в порядке moments.
func InsertRemindersWith(exec dbExecutor, planID uuid.UUID, moments []models.ReminderMoment) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(moments))
	for _, m := range moments {
		var notes sql.NullString
		if m.Notes != nil && *m.Notes != "" {
			notes = sql.NullString{String: *m.Notes, Valid: true}
		}
		id := uuid.New()
		if _, err := exec.Exec(`
			INSERT INTO reminder (id, plan_id, remind_at, notes) VALUES ($1, $2, $3, $4)
		`, id, planID, m.RemindAt.UTC(), notes); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// SetReminderPlanSourceWith записывает источник настроек (source/source_id).
func SetReminderPlanSourceWith(exec dbExecutor, planID uuid.UUID, source string, sourceID uuid.NullUUID) error {
	_, err := exec.Exec(`UPDATE reminder_plan SET source = $1, source_id = $2 WHERE id = $3`, source, sourceID, planID)
	return err
}

// ReminderPlanUpdate описывает изменение настроек: nil/false-поля не
// меняются.
type ReminderPlanUpdate struct {
	Type  *string
	Value *json.RawMessage
	// Notes: nil — не менять; пустая строка — очистить.
	Notes *string
	// Schedule — новое расписание целиком; nil — не менять.
	Schedule *ReminderPlanSchedule
}

// ReminderPlanSchedule — поля расписания настроек и часовой пояс, в котором
// они трактуются.
type ReminderPlanSchedule struct {
	FrequencyType string
	Weekdays      []int
	IntervalDays  sql.NullInt64
	Times         []string
	StartDate     time.Time
	EndDate       sql.NullTime
	TZ            string
}

// UpdateReminderPlanWith применяет изменение настроек.
func UpdateReminderPlanWith(exec dbExecutor, planID uuid.UUID, upd ReminderPlanUpdate) error {
	setParts := []string{}
	args := []any{}
	add := func(field string, value any) {
		args = append(args, value)
		setParts = append(setParts, fmt.Sprintf("%s = $%d", field, len(args)))
	}
	if upd.Type != nil {
		add("type", *upd.Type)
	}
	if upd.Value != nil {
		add("value", string(*upd.Value))
	}
	if upd.Notes != nil {
		if *upd.Notes == "" {
			add("notes", sql.NullString{})
		} else {
			add("notes", sql.NullString{String: *upd.Notes, Valid: true})
		}
	}
	if upd.Schedule != nil {
		s := upd.Schedule
		var weekdays sql.NullString
		if s.Weekdays != nil {
			b, err := json.Marshal(s.Weekdays)
			if err != nil {
				return err
			}
			weekdays = sql.NullString{String: string(b), Valid: true}
		}
		add("frequency_type", s.FrequencyType)
		add("weekdays", weekdays)
		add("interval_days", s.IntervalDays)
		add("times", pq.Array(s.Times))
		add("start_date", s.StartDate)
		add("end_date", s.EndDate)
		add("tz", s.TZ)
	}
	if len(setParts) == 0 {
		return nil
	}
	query := "UPDATE reminder_plan SET "
	for i, part := range setParts {
		if i > 0 {
			query += ", "
		}
		query += part
	}
	args = append(args, planID)
	query += fmt.Sprintf(" WHERE id = $%d", len(args))
	_, err := exec.Exec(query, args...)
	return err
}

// ListOpenRemindersWith возвращает незавершённые напоминания настроек,
// отсортированные по remind_at, затем по id.
func ListOpenRemindersWith(exec dbExecutor, planID uuid.UUID) ([]models.ReminderDB, error) {
	rows, err := exec.Query(`
		SELECT id, plan_id, remind_at, notes, closed_at, close_reason, fact_event_id
		FROM reminder WHERE plan_id = $1 AND closed_at IS NULL
		ORDER BY remind_at, id
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.ReminderDB
	for rows.Next() {
		var r models.ReminderDB
		if err := rows.Scan(&r.ID, &r.PlanID, &r.RemindAt, &r.Notes, &r.ClosedAt, &r.CloseReason, &r.FactEventID); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}

// ListClosedRemindMomentsWith возвращает множество моментов (unix-секунды)
// закрытых напоминаний настроек: пересоздание расписания не создаёт заново
// момент, который уже закрыт, независимо от причины закрытия.
func ListClosedRemindMomentsWith(exec dbExecutor, planID uuid.UUID) (map[int64]bool, error) {
	rows, err := exec.Query(`SELECT remind_at FROM reminder WHERE plan_id = $1 AND closed_at IS NOT NULL`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]bool)
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		result[t.Unix()] = true
	}
	return result, rows.Err()
}

// CountOpenRemindersWith возвращает число незавершённых напоминаний настроек.
func CountOpenRemindersWith(exec dbExecutor, planID uuid.UUID) (int, error) {
	var count int
	err := exec.QueryRow(`SELECT COUNT(*) FROM reminder WHERE plan_id = $1 AND closed_at IS NULL`, planID).Scan(&count)
	return count, err
}

// CountFutureRemindersWithOwnFilesWith возвращает число незавершённых
// напоминаний настроек с remind_at > now, у которых есть собственные
// подтверждённые файлы.
func CountFutureRemindersWithOwnFilesWith(exec dbExecutor, planID uuid.UUID, now time.Time) (int, error) {
	var count int
	err := exec.QueryRow(`
		SELECT COUNT(*) FROM reminder r
		WHERE r.plan_id = $1 AND r.closed_at IS NULL AND r.remind_at > $2
		AND EXISTS (SELECT 1 FROM file f WHERE f.owner_type = $3 AND f.owner_id = r.id AND f.confirmed_at IS NOT NULL)
	`, planID, now.UTC(), ReminderFileOwnerType).Scan(&count)
	return count, err
}

// MaxOwnReminderFilesCountForPlan возвращает наибольшее число собственных
// подтверждённых файлов среди незавершённых напоминаний настроек.
func MaxOwnReminderFilesCountForPlan(planID uuid.UUID) (int, error) {
	var maxCount sql.NullInt64
	err := DB.QueryRow(`
		SELECT MAX(cnt) FROM (
			SELECT COUNT(*) AS cnt FROM file f
			JOIN reminder r ON r.id = f.owner_id
			WHERE f.owner_type = $1 AND f.confirmed_at IS NOT NULL AND r.plan_id = $2 AND r.closed_at IS NULL
			GROUP BY f.owner_id
		) counts
	`, ReminderFileOwnerType, planID).Scan(&maxCount)
	if err != nil {
		return 0, err
	}
	return int(maxCount.Int64), nil
}

// ReminderPlanIDOfReminder возвращает id настроек напоминания.
func ReminderPlanIDOfReminder(reminderID uuid.UUID) (uuid.UUID, error) {
	var planID uuid.UUID
	err := DB.QueryRow(`SELECT plan_id FROM reminder WHERE id = $1`, reminderID).Scan(&planID)
	return planID, err
}

// deleteReminderFilesWith удаляет строки file напоминаний reminderIDs.
func deleteReminderFilesWith(exec dbExecutor, reminderIDs []uuid.UUID) ([]string, error) {
	return DeleteFilesByOwnersWith(exec, ReminderFileOwnerType, reminderIDs)
}

// DeleteFutureOpenRemindersWith жёстко удаляет незавершённые напоминания
// настроек с remind_at > now вместе с их собственными файлами. Прошедшие
// незавершённые и закрытые напоминания не затрагиваются.
func DeleteFutureOpenRemindersWith(exec dbExecutor, planID uuid.UUID, now time.Time) ([]string, error) {
	rows, err := exec.Query(`
		DELETE FROM reminder WHERE plan_id = $1 AND closed_at IS NULL AND remind_at > $2 RETURNING id
	`, planID, now.UTC())
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return deleteReminderFilesWith(exec, ids)
}

// DeleteReminderPlanWith жёстко удаляет настройки со всеми напоминаниями (в
// том числе закрытыми) и файлами настроек и напоминаний. Ссылки лекарств и
// вакцинаций на настройки обнуляются внешним ключом.
func DeleteReminderPlanWith(exec dbExecutor, planID uuid.UUID) ([]string, error) {
	return DeleteReminderPlansWith(exec, []uuid.UUID{planID})
}

// DeleteReminderPlansWith — то же, что DeleteReminderPlanWith, для набора
// настроек.
func DeleteReminderPlansWith(exec dbExecutor, planIDs []uuid.UUID) ([]string, error) {
	if len(planIDs) == 0 {
		return nil, nil
	}
	reminderIDs, err := uuidColumnWith(exec, `SELECT id FROM reminder WHERE plan_id = ANY($1)`, pq.Array(planIDs))
	if err != nil {
		return nil, err
	}
	reminderOrphans, err := deleteReminderFilesWith(exec, reminderIDs)
	if err != nil {
		return nil, err
	}
	planOrphans, err := DeleteFilesByOwnersWith(exec, ReminderPlanFileOwnerType, planIDs)
	if err != nil {
		return nil, err
	}
	if _, err := exec.Exec(`DELETE FROM reminder_plan WHERE id = ANY($1)`, pq.Array(planIDs)); err != nil {
		return nil, err
	}
	return append(reminderOrphans, planOrphans...), nil
}

// UnlinkPetFromReminderPlansWith жёстко удаляет связи питомца с настройками
// напоминаний, а настройки, у которых после этого не осталось питомцев (ни
// видимых, ни скрытых), — вместе с напоминаниями и файлами. Настройки,
// общие с другими питомцами, остаются у них. Возвращает ключи объектов S3, на
// которые не осталось ссылок.
func UnlinkPetFromReminderPlansWith(exec dbExecutor, petID uuid.UUID) ([]string, error) {
	planIDs, err := uuidColumnWith(exec, `DELETE FROM reminder_plan_pet WHERE pet_id = $1 RETURNING plan_id`, petID)
	if err != nil {
		return nil, err
	}
	if len(planIDs) == 0 {
		return nil, nil
	}
	emptyPlanIDs, err := uuidColumnWith(exec, `
		SELECT p.id FROM reminder_plan p
		WHERE p.id = ANY($1)
		AND NOT EXISTS (SELECT 1 FROM reminder_plan_pet rpp WHERE rpp.plan_id = p.id)
	`, pq.Array(planIDs))
	if err != nil {
		return nil, err
	}
	return DeleteReminderPlansWith(exec, emptyPlanIDs)
}

func uuidColumnWith(exec dbExecutor, query string, args ...any) ([]uuid.UUID, error) {
	rows, err := exec.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetReminderForUserWith находит напоминание (в том числе закрытое) по id и
// проверяет, что его настройки принадлежат userID и имеют хотя бы одного не
// мягко удалённого питомца. sql.ErrNoRows — напоминания нет, оно чужое либо у
// его настроек нет видимых питомцев. lock=true блокирует строку напоминания
// до конца транзакции.
func GetReminderForUserWith(exec dbExecutor, reminderID uuid.UUID, userID string, lock bool) (*ReminderFull, error) {
	query := `
		SELECT r.id, r.plan_id, r.remind_at, r.notes, r.closed_at, r.close_reason, r.fact_event_id,
		       ` + reminderPlanColumns + `, COALESCE(med.name, vac.name)
		FROM reminder r
		JOIN reminder_plan p ON p.id = r.plan_id` + reminderPlanJoins + `
		WHERE r.id = $1 AND p.user_id = $2 AND ` + reminderPlanVisibleClause
	if lock {
		query += ` FOR UPDATE OF r`
	}
	var full ReminderFull
	row := exec.QueryRow(query, reminderID, userID)
	rem := &full.Reminder
	plan, err := scanReminderPlanWithPrefix(row.Scan,
		[]any{&rem.ID, &rem.PlanID, &rem.RemindAt, &rem.Notes, &rem.ClosedAt, &rem.CloseReason, &rem.FactEventID},
		[]any{&full.SourceTitle})
	if err != nil {
		return nil, err
	}
	full.Plan = plan
	pets, err := reminderPlanPetLinks.visiblePets(exec, []uuid.UUID{plan.ID})
	if err != nil {
		return nil, err
	}
	full.Pets = pets[plan.ID]
	return &full, nil
}

// scanReminderPlanWithPrefix — scanReminderPlan для запросов, где колонки
// настроек идут не первыми.
func scanReminderPlanWithPrefix(scan func(dest ...any) error, prefix []any, suffix []any) (models.ReminderPlanDB, error) {
	var p models.ReminderPlanDB
	var weekdaysNS sql.NullString
	var value []byte
	dest := append([]any{}, prefix...)
	dest = append(dest, &p.ID, &p.UserID, &p.Source, &p.SourceID, &p.Type, &value, &p.Notes, &p.FrequencyType,
		&weekdaysNS, &p.IntervalDays, pq.Array(&p.Times), &p.StartDate, &p.EndDate, &p.TZ, &p.CreatedAt)
	dest = append(dest, suffix...)
	if err := scan(dest...); err != nil {
		return p, err
	}
	p.Value = json.RawMessage(value)
	if weekdaysNS.Valid {
		if err := json.Unmarshal([]byte(weekdaysNS.String), &p.Weekdays); err != nil {
			return p, err
		}
	}
	return p, nil
}

// CloseReminderWith закрывает незавершённое напоминание (closed_at = now,
// close_reason, при «выполнено» — fact_event_id) и удаляет его собственные
// файлы. Если у настроек не осталось незавершённых напоминаний, удаляет
// настройки вместе со строками закрытых напоминаний и файлами. Возвращает
// ключи объектов S3, на которые не осталось ссылок, и признак удаления
// настроек.
func CloseReminderWith(exec dbExecutor, reminder models.ReminderDB, reason string, factEventID uuid.NullUUID, now time.Time) (orphanKeys []string, planDeleted bool, err error) {
	if _, err = exec.Exec(`
		UPDATE reminder SET closed_at = $1, close_reason = $2, fact_event_id = $3 WHERE id = $4
	`, now.UTC(), reason, factEventID, reminder.ID); err != nil {
		return nil, false, err
	}
	orphanKeys, err = deleteReminderFilesWith(exec, []uuid.UUID{reminder.ID})
	if err != nil {
		return nil, false, err
	}

	open, err := CountOpenRemindersWith(exec, reminder.PlanID)
	if err != nil {
		return nil, false, err
	}
	if open > 0 {
		return orphanKeys, false, nil
	}
	planOrphans, err := DeleteReminderPlanWith(exec, reminder.PlanID)
	if err != nil {
		return nil, false, err
	}
	return append(orphanKeys, planOrphans...), true, nil
}

// ReminderCalendarRow — незавершённое напоминание вместе с данными его
// настроек, нужными календарю.
type ReminderCalendarRow struct {
	ID            uuid.UUID
	PlanID        uuid.UUID
	RemindAt      time.Time
	ReminderNotes sql.NullString
	PlanNotes     sql.NullString
	Type          string
	Value         json.RawMessage
	// Pets — видимые питомцы настроек.
	Pets       []LinkedPet
	PlanSource string
	// PlanSourceTitle — название лекарства либо вакцинации; не задано у
	// настроек manual.
	PlanSourceTitle sql.NullString
	// PlanUnclosedCount — число незавершённых напоминаний настроек, включая
	// само напоминание.
	PlanUnclosedCount int
}

// scanReminderCalendarRows читает строки напоминаний календаря и дополняет
// их видимыми питомцами настроек одним запросом.
func scanReminderCalendarRows(exec dbExecutor, rows *sql.Rows) ([]ReminderCalendarRow, error) {
	defer rows.Close()
	var items []ReminderCalendarRow
	for rows.Next() {
		var it ReminderCalendarRow
		var value []byte
		if err := rows.Scan(&it.ID, &it.PlanID, &it.RemindAt, &it.ReminderNotes, &it.PlanNotes, &it.Type, &value,
			&it.PlanSource, &it.PlanSourceTitle, &it.PlanUnclosedCount); err != nil {
			return nil, err
		}
		it.Value = json.RawMessage(value)
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	planIDs := make([]uuid.UUID, len(items))
	for i, it := range items {
		planIDs[i] = it.PlanID
	}
	pets, err := reminderPlanPetLinks.visiblePets(exec, planIDs)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Pets = pets[items[i].PlanID]
	}
	return items, nil
}

// reminderCalendarSelect — незавершённые напоминания настроек userID, у
// которых есть хотя бы один не мягко удалённый питомец.
var reminderCalendarSelect = `
	SELECT r.id, r.plan_id, r.remind_at, r.notes, p.notes, p.type, p.value,
	       p.source, COALESCE(med.name, vac.name),
	       (SELECT COUNT(*) FROM reminder o WHERE o.plan_id = p.id AND o.closed_at IS NULL)
	FROM reminder r
	JOIN reminder_plan p ON p.id = r.plan_id` + reminderPlanJoins + `
	WHERE p.user_id = $1 AND r.closed_at IS NULL AND ` + reminderPlanVisibleClause

// GetRemindersByUserIDInRange возвращает незавершённые напоминания настроек
// userID с видимыми питомцами, чей remind_at попадает в полуоткрытый
// интервал [start, end), отсортированные по remind_at, затем по id.
// Наступившие незавершённые напоминания выбираются наравне с будущими;
// напоминание общих настроек присутствует один раз.
func GetRemindersByUserIDInRange(userID string, start, end time.Time) ([]ReminderCalendarRow, error) {
	rows, err := DB.Query(reminderCalendarSelect+`
		AND r.remind_at >= $2 AND r.remind_at < $3
		ORDER BY r.remind_at ASC, r.id ASC`, userID, start.UTC(), end.UTC())
	if err != nil {
		log.Println("GetRemindersByUserIDInRange error:", err)
		return nil, err
	}
	return scanReminderCalendarRows(DB, rows)
}

// GetUpcomingRemindersByUserID возвращает до limit ближайших незавершённых
// напоминаний с remind_at >= now по настройкам userID с видимыми питомцами.
func GetUpcomingRemindersByUserID(userID string, now time.Time, limit int) ([]ReminderCalendarRow, error) {
	rows, err := DB.Query(reminderCalendarSelect+`
		AND r.remind_at >= $2
		ORDER BY r.remind_at ASC, r.id ASC
		LIMIT $3`, userID, now.UTC(), limit)
	if err != nil {
		log.Println("GetUpcomingRemindersByUserID error:", err)
		return nil, err
	}
	return scanReminderCalendarRows(DB, rows)
}

// GetUpcomingRemindersByPetID возвращает до limit ближайших незавершённых
// напоминаний настроек, привязанных к питомцу (в том числе общих с другими
// питомцами), с remind_at >= now.
func GetUpcomingRemindersByPetID(userID string, petID uuid.UUID, now time.Time, limit int) ([]ReminderCalendarRow, error) {
	rows, err := DB.Query(reminderCalendarSelect+`
		AND EXISTS (SELECT 1 FROM reminder_plan_pet own_link WHERE own_link.plan_id = p.id AND own_link.pet_id = $4)
		AND r.remind_at >= $2
		ORDER BY r.remind_at ASC, r.id ASC
		LIMIT $3`, userID, now.UTC(), limit, petID)
	if err != nil {
		log.Println("GetUpcomingRemindersByPetID error:", err)
		return nil, err
	}
	return scanReminderCalendarRows(DB, rows)
}

// CountRemindersByUserIDGroupedByDay возвращает число незавершённых
// напоминаний настроек userID с видимыми питомцами, чей remind_at попадает в
// [start, end), сгруппированное по календарному дню в поясе loc
// (YYYY-MM-DD). Напоминание общих настроек считается один раз. Группировка
// выполняется в Go по той же причине, что у CountEventsByUserIDGroupedByDay.
func CountRemindersByUserIDGroupedByDay(userID string, start, end time.Time, loc *time.Location) (map[string]int, error) {
	rows, err := DB.Query(`
		SELECT r.remind_at
		FROM reminder r
		JOIN reminder_plan p ON p.id = r.plan_id
		WHERE p.user_id = $1 AND r.closed_at IS NULL AND `+reminderPlanVisibleClause+`
		AND r.remind_at >= $2 AND r.remind_at < $3
	`, userID, start.UTC(), end.UTC())
	if err != nil {
		log.Println("CountRemindersByUserIDGroupedByDay error:", err)
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		result[t.In(loc).Format("2006-01-02")]++
	}
	return result, rows.Err()
}

// CheckReminderPlanFileOwnership — правило владения для owner_type =
// "reminder_plan_file": настройки существуют, принадлежат userID
// (reminder_plan.user_id) и имеют хотя бы одного не мягко удалённого питомца.
func CheckReminderPlanFileOwnership(planID uuid.UUID, userID string) (bool, error) {
	var count int
	err := DB.QueryRow(`
		SELECT COUNT(1) FROM reminder_plan p
		WHERE p.id = $1 AND p.user_id = $2 AND `+reminderPlanVisibleClause, planID, userID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

// CheckReminderFileOwnership — правило владения для owner_type =
// "reminder_file": напоминание незавершённое, его настройки принадлежат
// userID и имеют хотя бы одного не мягко удалённого питомца.
func CheckReminderFileOwnership(reminderID uuid.UUID, userID string) (bool, error) {
	var count int
	err := DB.QueryRow(`
		SELECT COUNT(1) FROM reminder r
		JOIN reminder_plan p ON p.id = r.plan_id
		WHERE r.id = $1 AND r.closed_at IS NULL AND p.user_id = $2 AND `+reminderPlanVisibleClause, reminderID, userID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

// GetFilesForOwnerWith — GetFilesForOwner на произвольном dbExecutor.
func GetFilesForOwnerWith(exec dbExecutor, ownerType string, ownerID uuid.UUID) ([]models.FileDB, error) {
	rows, err := exec.Query(`
		SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at
		FROM file
		WHERE owner_type = $1 AND owner_id = $2 AND confirmed_at IS NOT NULL
		ORDER BY position ASC
	`, ownerType, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	files := []models.FileDB{}
	for rows.Next() {
		var f models.FileDB
		if err := rows.Scan(&f.ID, &f.OwnerType, &f.OwnerID, &f.UserID, &f.ObjectKey, &f.ContentType, &f.Filename, &f.Position, &f.ConfirmedAt, &f.CreatedAt); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// CopyEventFilesFromReminderWith создаёт для факта ссылочные строки file на
// файлы настроек и собственные файлы напоминания (в этом порядке).
func CopyEventFilesFromReminderWith(exec dbExecutor, userID string, planID, reminderID, eventID uuid.UUID) error {
	_, err := CopyFilesAsReferencesWith(exec, userID, []FileOwnerRef{
		{OwnerType: ReminderPlanFileOwnerType, OwnerID: planID},
		{OwnerType: ReminderFileOwnerType, OwnerID: reminderID},
	}, eventFileOwnerType, eventID)
	return err
}

// CopyPlanFilesFromReminderWith создаёт для новых настроек ссылочные строки
// file на файлы настроек и собственные файлы заменяемого напоминания (в этом
// порядке): при замене напоминания его данные и файлы переходят к новым
// настройкам.
func CopyPlanFilesFromReminderWith(exec dbExecutor, userID string, planID, reminderID, newPlanID uuid.UUID) error {
	_, err := CopyFilesAsReferencesWith(exec, userID, []FileOwnerRef{
		{OwnerType: ReminderPlanFileOwnerType, OwnerID: planID},
		{OwnerType: ReminderFileOwnerType, OwnerID: reminderID},
	}, ReminderPlanFileOwnerType, newPlanID)
	return err
}
