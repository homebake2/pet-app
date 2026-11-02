package database

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Сроки хранения данных (см. «Общие требования: Сроки хранения данных»).
// Изменение срока — изменение требований и текста политики конфиденциальности.
const (
	// SoftDeletedRetention — сколько мягко удалённые записи лежат в БД до
	// физического удаления.
	SoftDeletedRetention = 90 * 24 * time.Hour
	// UnconfirmedFileRetention — сколько живёт загрузка файла, которую клиент
	// не подтвердил.
	UnconfirmedFileRetention = 24 * time.Hour
	// SuggestionRetentionMonths — срок хранения предложений с сайта.
	SuggestionRetentionMonths = 24
	// RegistrationAttemptRetentionDays — сколько прошедших суток (помимо
	// текущих) хранятся счётчики регистраций по IP.
	RegistrationAttemptRetentionDays = 1

	// retentionBatchSize ограничивает число записей одного вида, удаляемых за
	// один проход, чтобы транзакция не держала таблицы долго.
	retentionBatchSize = 500
)

// softDeletedTables — таблицы с deleted_at, кроме pet: питомец удаляется
// отдельно, потому что вместе с ним нужно убрать осиротевшие события.
var softDeletedTables = []string{
	"event",
	"vaccination",
	"disease",
	"vet_visit",
	"allergy",
	"medication",
}

// fileOwnerTables сопоставляет owner_type файла таблице его владельца. Файл
// владельца, строки которого уже нет, никому не виден и подлежит удалению.
// Типы, которых здесь нет, очисткой не затрагиваются.
var fileOwnerTables = map[string]string{
	"pet_photo":               "pet",
	eventFileOwnerType:        "event",
	ReminderPlanFileOwnerType: "reminder_plan",
	ReminderFileOwnerType:     "reminder",
	"vaccination_file":        "vaccination",
	"disease_file":            "disease",
	"vet_visit_file":          "vet_visit",
	"allergy_file":            "allergy",
	"medication_file":         "medication",
}

// PurgeSoftDeleted физически удаляет одну порцию записей, мягко удалённых
// раньше, чем olderThan назад: питомцев (вместе со связями, записями
// Ветпаспорта и событиями, у которых не осталось питомцев), а также события и
// записи пяти сущностей Ветпаспорта. Файлы удалённых записей убирает
// SweepOrphanFiles. Возвращает число удалённых записей; 0 означает, что
// удалять больше нечего.
func PurgeSoftDeleted(olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	var total int64
	err := RunInTx(func(tx *sql.Tx) error {
		pets, err := purgeSoftDeletedPetsWith(tx, cutoff)
		if err != nil {
			return err
		}
		total += pets
		for _, table := range softDeletedTables {
			res, err := tx.Exec(fmt.Sprintf(`
				DELETE FROM %[1]s WHERE id IN (
					SELECT id FROM %[1]s WHERE deleted_at < $1 ORDER BY deleted_at LIMIT $2
				)`, table), cutoff, retentionBatchSize)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			total += n
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// purgeSoftDeletedPetsWith удаляет порцию мягко удалённых питомцев. Связи
// event_pet, записи Ветпаспорта и доступы уходят каскадом; события, у которых
// после этого не осталось ни одного питомца, удаляются вручную — по ним
// больше нельзя ничего прочитать. Событие, привязанное и к другому питомцу,
// остаётся у него.
func purgeSoftDeletedPetsWith(tx *sql.Tx, cutoff time.Time) (int64, error) {
	rows, err := tx.Query(`
		SELECT id FROM pet WHERE deleted_at < $1 ORDER BY deleted_at LIMIT $2
	`, cutoff, retentionBatchSize)
	if err != nil {
		return 0, err
	}
	petIDs, err := scanUUIDs(rows)
	if err != nil {
		return 0, err
	}
	if len(petIDs) == 0 {
		return 0, nil
	}

	rows, err = tx.Query(`
		SELECT DISTINCT event_id FROM event_pet WHERE pet_id = ANY($1)
	`, pq.Array(petIDs))
	if err != nil {
		return 0, err
	}
	eventIDs, err := scanUUIDs(rows)
	if err != nil {
		return 0, err
	}

	res, err := tx.Exec(`DELETE FROM pet WHERE id = ANY($1)`, pq.Array(petIDs))
	if err != nil {
		return 0, err
	}
	purged, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	if len(eventIDs) > 0 {
		if _, err := tx.Exec(`
			DELETE FROM event e
			WHERE e.id = ANY($1)
			  AND NOT EXISTS (SELECT 1 FROM event_pet ep WHERE ep.event_id = e.id)
		`, pq.Array(eventIDs)); err != nil {
			return 0, err
		}
	}
	return purged, nil
}

func scanUUIDs(rows *sql.Rows) ([]uuid.UUID, error) {
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

// SweepOrphanFiles удаляет одну порцию строк file без владельца (его строки
// в БД уже нет: питомца, события, записи Ветпаспорта или напоминания
// удалили, в том числе вместе с аккаунтом) и неподтверждённых загрузок старше
// unconfirmedOlderThan. Возвращает число удалённых строк и ключи объектов S3,
// на которые не осталось ссылок: вызывающий код удаляет их из хранилища после
// фиксации транзакции. 0 удалённых строк означает, что чистить больше нечего.
func SweepOrphanFiles(unconfirmedOlderThan time.Duration) (deleted int64, orphanObjectKeys []string, err error) {
	ownerTypes := make([]string, 0, len(fileOwnerTables))
	for ownerType := range fileOwnerTables {
		ownerTypes = append(ownerTypes, ownerType)
	}
	sort.Strings(ownerTypes)

	// Имена таблиц и owner_type берутся из fileOwnerTables, а не из
	// пользовательского ввода.
	conditions := make([]string, 0, len(ownerTypes))
	for _, ownerType := range ownerTypes {
		conditions = append(conditions, fmt.Sprintf(
			`(f.owner_type = '%s' AND NOT EXISTS (SELECT 1 FROM %s o WHERE o.id = f.owner_id))`,
			ownerType, fileOwnerTables[ownerType],
		))
	}
	query := fmt.Sprintf(`
		DELETE FROM file WHERE id IN (
			SELECT f.id FROM file f
			WHERE (f.confirmed_at IS NULL AND f.created_at < $1)
			   OR %s
			LIMIT $2
		)
		RETURNING object_key
	`, strings.Join(conditions, "\n\t\t\t   OR "))

	cutoff := time.Now().UTC().Add(-unconfirmedOlderThan)
	err = RunInTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(query, cutoff, retentionBatchSize)
		if err != nil {
			return err
		}
		var keys []string
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, key)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		deleted = int64(len(keys))
		orphanObjectKeys, err = unreferencedObjectKeysWith(tx, keys)
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	return deleted, orphanObjectKeys, nil
}

// DeleteStaleRegistrationAttempts удаляет счётчики регистраций по IP старше
// keepDays прошедших суток UTC (текущие сутки не считаются). IP-адрес —
// персональные данные, поэтому таблица не должна его накапливать.
func DeleteStaleRegistrationAttempts(keepDays int) (int64, error) {
	res, err := DB.Exec(`
		DELETE FROM registration_rate_limit
		WHERE day < (now() AT TIME ZONE 'UTC')::date - $1::int
	`, keepDays)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteStaleSuggestions удаляет предложения с сайта старше months месяцев.
func DeleteStaleSuggestions(months int) (int64, error) {
	res, err := DB.Exec(`
		DELETE FROM suggestion WHERE created_at < now() - make_interval(months => $1)
	`, months)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
