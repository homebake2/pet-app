// Передача питомца другому пользователю: таблица pet_transfer хранит историю
// передач (запись создаётся со статусом pending, затем переходит в accepted,
// declined, cancelled или expired). Активной считается передача pending с
// expires_at > now(); pending с истёкшим сроком везде трактуется как expired,
// а строка переводится в expired при первом обращении к ней (ленивое
// истечение) либо фоновой очисткой (ExpireStalePetTransfers).
//
// Порядок блокировок во всех транзакциях один: сначала строка передачи, затем
// строка питомца — это исключает взаимную блокировку принятия и удаления
// питомца.
package database

import (
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Статусы передачи питомца.
const (
	PetTransferStatusPending   = "pending"
	PetTransferStatusAccepted  = "accepted"
	PetTransferStatusDeclined  = "declined"
	PetTransferStatusCancelled = "cancelled"
	PetTransferStatusExpired   = "expired"
)

const (
	// PetTransferTTL — срок действия кода передачи с момента создания.
	PetTransferTTL = 7 * 24 * time.Hour
	// PetTransferCodeFailureLimit — наибольшее число неудачных обращений к
	// эндпоинтам с кодом за PetTransferCodeFailureWindow на пользователя.
	PetTransferCodeFailureLimit  = 10
	PetTransferCodeFailureWindow = time.Hour

	petTransferPendingPetIndex  = "pet_transfer_pending_pet_idx"
	petTransferPendingCodeIndex = "pet_transfer_pending_code_idx"
)

var (
	// ErrTransferAlreadyPending — у питомца уже есть активная передача.
	ErrTransferAlreadyPending = errors.New("pet transfer already pending")
	// ErrTransferCodeCollision — сгенерированный код совпал с кодом другой
	// активной передачи; вызывающий код генерирует новый код и повторяет.
	ErrTransferCodeCollision = errors.New("pet transfer code collision")
	// ErrTransferOwnPet — код использует отправитель передачи.
	ErrTransferOwnPet = errors.New("pet transfer used by its sender")
)

// PetTransfer — активная передача питомца.
type PetTransfer struct {
	ID        uuid.UUID
	Code      string
	ExpiresAt time.Time
}

// PendingPetTransfer — найденная по коду активная передача, питомец которой
// существует, не удалён и принадлежит отправителю.
type PendingPetTransfer struct {
	ID         uuid.UUID
	PetID      uuid.UUID
	FromUserID string
	ExpiresAt  time.Time
}

// expirePetTransfersForPetWith переводит истёкшие pending-передачи питомца в
// expired.
func expirePetTransfersForPetWith(exec dbExecutor, petID uuid.UUID) error {
	_, err := exec.Exec(`
		UPDATE pet_transfer SET status = 'expired', resolved_at = now()
		WHERE pet_id = $1 AND status = 'pending' AND expires_at <= now()
	`, petID)
	return err
}

// ExpireStalePetTransfers переводит все истёкшие pending-передачи в expired и
// возвращает их число (фоновая очистка).
func ExpireStalePetTransfers() (int64, error) {
	result, err := DB.Exec(`
		UPDATE pet_transfer SET status = 'expired', resolved_at = now()
		WHERE status = 'pending' AND expires_at <= now()
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// CreatePetTransfer создаёт передачу питомца petID с кодом code для владельца
// userID. sql.ErrNoRows — питомца нет, он чужой или мягко удалён;
// ErrTransferAlreadyPending — у питомца уже есть активная передача;
// ErrTransferCodeCollision — код занят другой активной передачей.
func CreatePetTransfer(petID uuid.UUID, userID string, code string) (*PetTransfer, error) {
	var transfer PetTransfer
	err := RunInTx(func(tx *sql.Tx) error {
		if err := expirePetTransfersForPetWith(tx, petID); err != nil {
			return err
		}

		var name, species string
		err := tx.QueryRow(`
			SELECT name, species FROM pet
			WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
			FOR UPDATE
		`, petID, userID).Scan(&name, &species)
		if err != nil {
			return err
		}

		var pending bool
		if err := tx.QueryRow(`
			SELECT EXISTS (SELECT 1 FROM pet_transfer WHERE pet_id = $1 AND status = 'pending')
		`, petID).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return ErrTransferAlreadyPending
		}

		err = tx.QueryRow(`
			INSERT INTO pet_transfer (pet_id, pet_name, pet_species, from_user_id, code, status, expires_at)
			VALUES ($1, $2, $3, $4, $5, 'pending', now() + make_interval(secs => $6))
			RETURNING id, code, expires_at
		`, petID, name, species, userID, code, PetTransferTTL.Seconds()).Scan(&transfer.ID, &transfer.Code, &transfer.ExpiresAt)
		if err != nil {
			var pqErr *pq.Error
			if errors.As(err, &pqErr) && pqErr.Code == "23505" {
				switch pqErr.Constraint {
				case petTransferPendingPetIndex:
					return ErrTransferAlreadyPending
				case petTransferPendingCodeIndex:
					return ErrTransferCodeCollision
				}
			}
			return err
		}
		return nil
	})
	if err != nil {
		if err != sql.ErrNoRows && !errors.Is(err, ErrTransferAlreadyPending) && !errors.Is(err, ErrTransferCodeCollision) {
			log.Println("CreatePetTransfer error:", err)
		}
		return nil, err
	}
	return &transfer, nil
}

// GetActivePetTransfer возвращает активную передачу питомца владельца userID.
// sql.ErrNoRows — питомца нет, он чужой или мягко удалён, либо активной
// передачи нет.
func GetActivePetTransfer(petID uuid.UUID, userID string) (*PetTransfer, error) {
	if err := expirePetTransfersForPetWith(DB, petID); err != nil {
		return nil, err
	}
	var transfer PetTransfer
	err := DB.QueryRow(`
		SELECT t.id, t.code, t.expires_at
		FROM pet_transfer t
		JOIN pet p ON p.id = t.pet_id
		WHERE t.pet_id = $1 AND p.user_id = $2 AND p.deleted_at IS NULL
		AND t.status = 'pending' AND t.expires_at > now()
	`, petID, userID).Scan(&transfer.ID, &transfer.Code, &transfer.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &transfer, nil
}

// CancelActivePetTransfer переводит активную передачу питомца владельца
// userID в cancelled. sql.ErrNoRows — питомца нет, он чужой или мягко удалён,
// либо активной передачи нет.
func CancelActivePetTransfer(petID uuid.UUID, userID string) error {
	if err := expirePetTransfersForPetWith(DB, petID); err != nil {
		return err
	}
	result, err := DB.Exec(`
		UPDATE pet_transfer t SET status = 'cancelled', resolved_at = now()
		WHERE t.pet_id = $1 AND t.status = 'pending' AND t.expires_at > now()
		AND EXISTS (SELECT 1 FROM pet p WHERE p.id = t.pet_id AND p.user_id = $2 AND p.deleted_at IS NULL)
	`, petID, userID)
	if err != nil {
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

// cancelPendingTransfersForPetWith переводит активную передачу питомца в
// cancelled (мягкое удаление питомца).
func cancelPendingTransfersForPetWith(exec dbExecutor, petID uuid.UUID) error {
	_, err := exec.Exec(`
		UPDATE pet_transfer SET status = 'cancelled', resolved_at = now()
		WHERE pet_id = $1 AND status = 'pending'
	`, petID)
	return err
}

// cancelPendingTransfersFromUserWith переводит активные передачи всех
// питомцев пользователя в cancelled (удаление аккаунта).
func cancelPendingTransfersFromUserWith(exec dbExecutor, userID string) error {
	_, err := exec.Exec(`
		UPDATE pet_transfer SET status = 'cancelled', resolved_at = now()
		WHERE from_user_id = $1 AND status = 'pending'
	`, userID)
	return err
}

// findPendingPetTransferByCodeWith ищет активную передачу по нормализованному
// коду; питомец должен существовать, не быть удалённым и принадлежать
// отправителю. Истёкшая передача с этим кодом переводится в expired.
// lock=true блокирует строки передачи и питомца до конца транзакции.
// sql.ErrNoRows — передача не найдена или недоступна.
func findPendingPetTransferByCodeWith(exec dbExecutor, code string, lock bool) (*PendingPetTransfer, error) {
	if _, err := exec.Exec(`
		UPDATE pet_transfer SET status = 'expired', resolved_at = now()
		WHERE code = $1 AND status = 'pending' AND expires_at <= now()
	`, code); err != nil {
		return nil, err
	}
	query := `
		SELECT t.id, t.pet_id, t.from_user_id, t.expires_at
		FROM pet_transfer t
		JOIN pet p ON p.id = t.pet_id AND p.user_id = t.from_user_id AND p.deleted_at IS NULL
		WHERE t.code = $1 AND t.status = 'pending' AND t.expires_at > now()`
	if lock {
		query += ` FOR UPDATE OF t, p`
	}
	var transfer PendingPetTransfer
	if err := exec.QueryRow(query, code).Scan(&transfer.ID, &transfer.PetID, &transfer.FromUserID, &transfer.ExpiresAt); err != nil {
		return nil, err
	}
	return &transfer, nil
}

// FindPendingPetTransferByCode ищет активную передачу по коду без блокировок
// (предпросмотр). sql.ErrNoRows — передача не найдена или недоступна.
func FindPendingPetTransferByCode(code string) (*PendingPetTransfer, error) {
	return findPendingPetTransferByCodeWith(DB, code, false)
}

// DeclinePetTransfer переводит активную передачу по коду в declined от имени
// userID. sql.ErrNoRows — передача не найдена или недоступна;
// ErrTransferOwnPet — код использует отправитель.
func DeclinePetTransfer(code string, userID string) error {
	return RunInTx(func(tx *sql.Tx) error {
		transfer, err := findPendingPetTransferByCodeWith(tx, code, true)
		if err != nil {
			return err
		}
		if transfer.FromUserID == userID {
			return ErrTransferOwnPet
		}
		_, err = tx.Exec(`
			UPDATE pet_transfer SET status = 'declined', to_user_id = $2, resolved_at = now() WHERE id = $1
		`, transfer.ID, userID)
		return err
	})
}

// GetPetTransferSenderName возвращает имя отправителя для получателя: first_name
// профиля, а при отсутствии профиля — логин.
func GetPetTransferSenderName(userID string) (string, error) {
	var name string
	err := DB.QueryRow(`
		SELECT COALESCE(NULLIF(trim(pr.first_name), ''), u.login)
		FROM users u
		LEFT JOIN profile pr ON pr.user_id = u.id
		WHERE u.id = $1
	`, userID).Scan(&name)
	return name, err
}

// CountPetTransferCodeFailures возвращает число неудачных обращений
// пользователя с кодом передачи за последний час.
func CountPetTransferCodeFailures(userID string) (int, error) {
	var count int
	err := DB.QueryRow(`
		SELECT COUNT(*) FROM pet_transfer_code_failure
		WHERE user_id = $1 AND created_at > now() - make_interval(secs => $2)
	`, userID, PetTransferCodeFailureWindow.Seconds()).Scan(&count)
	return count, err
}

// RecordPetTransferCodeFailure учитывает неудачное обращение с кодом передачи
// и заодно удаляет устаревшие записи пользователя.
func RecordPetTransferCodeFailure(userID string) error {
	_, err := DB.Exec(`
		WITH purge AS (
			DELETE FROM pet_transfer_code_failure
			WHERE user_id = $1 AND created_at <= now() - make_interval(secs => $2)
		)
		INSERT INTO pet_transfer_code_failure (user_id) VALUES ($1)
	`, userID, PetTransferCodeFailureWindow.Seconds())
	return err
}

// petTransferFileOwnerTables — owner_type файлов записей Ветпаспорта и
// таблица их сущностей (у каждой есть pet_id).
var petTransferFileOwnerTables = []struct{ ownerType, table string }{
	{"vaccination_file", "vaccination"},
	{"disease_file", "disease"},
	{"vet_visit_file", "vet_visit"},
	{"allergy_file", "allergy"},
	{"medication_file", "medication"},
}

// AcceptPetTransfer принимает передачу по коду от имени toUserID в одной
// транзакции: питомец и его события переходят к получателю (событие с другими
// видимыми питомцами копируется), связи питомца с настройками напоминаний
// удаляются, у файлов переносимых сущностей меняется владелец. Возвращает id
// питомца и ключи объектов S3, на которые не осталось ссылок (их удаляет
// вызывающий код после фиксации). sql.ErrNoRows — передача не найдена или
// недоступна; ErrTransferOwnPet — код использует отправитель.
func AcceptPetTransfer(code string, toUserID string) (petID uuid.UUID, orphanObjectKeys []string, err error) {
	err = RunInTx(func(tx *sql.Tx) error {
		transfer, err := findPendingPetTransferByCodeWith(tx, code, true)
		if err != nil {
			return err
		}
		if transfer.FromUserID == toUserID {
			return ErrTransferOwnPet
		}
		petID = transfer.PetID

		if _, err := tx.Exec(`UPDATE pet SET user_id = $1 WHERE id = $2`, toUserID, petID); err != nil {
			return err
		}
		if err := transferPetEventsWith(tx, petID, toUserID); err != nil {
			return err
		}
		orphanObjectKeys, err = UnlinkPetFromReminderPlansWith(tx, petID)
		if err != nil {
			return err
		}
		if err := transferPetFilesWith(tx, petID, toUserID); err != nil {
			return err
		}
		_, err = tx.Exec(`
			UPDATE pet_transfer SET status = 'accepted', to_user_id = $2, resolved_at = now() WHERE id = $1
		`, transfer.ID, toUserID)
		return err
	})
	if err != nil {
		if err != sql.ErrNoRows && !errors.Is(err, ErrTransferOwnPet) {
			log.Println("AcceptPetTransfer error:", err)
		}
		return uuid.Nil, nil, err
	}
	return petID, orphanObjectKeys, nil
}

// transferPetEventsWith переносит события питомца к получателю: событие без
// других видимых питомцев переходит целиком, событие с другими видимыми
// питомцами остаётся у отправителя, а получатель получает копию с одной
// связью — с передаваемым питомцем и ссылочными строками file.
func transferPetEventsWith(exec dbExecutor, petID uuid.UUID, toUserID string) error {
	rows, err := exec.Query(`
		SELECT e.id, e.deleted_at IS NOT NULL,
		       (SELECT COUNT(*) FROM event_pet other
		        JOIN pet other_pet ON other_pet.id = other.pet_id
		        WHERE other.event_id = e.id AND other.pet_id <> $1 AND other_pet.deleted_at IS NULL)
		FROM event e
		JOIN event_pet ep ON ep.event_id = e.id
		WHERE ep.pet_id = $1
		ORDER BY e.id
		FOR UPDATE OF e
	`, petID)
	if err != nil {
		return err
	}
	type linkedEvent struct {
		id           uuid.UUID
		deleted      bool
		visibleOther int
	}
	var events []linkedEvent
	for rows.Next() {
		var ev linkedEvent
		if err := rows.Scan(&ev.id, &ev.deleted, &ev.visibleOther); err != nil {
			rows.Close()
			return err
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	// События делятся на три группы, и каждая обрабатывается одним запросом
	// на всю группу: число обращений к базе не зависит от числа событий.
	var movedIDs, detachedIDs, copiedIDs []uuid.UUID
	for _, ev := range events {
		switch {
		case ev.visibleOther == 0:
			movedIDs = append(movedIDs, ev.id)
		case ev.deleted:
			detachedIDs = append(detachedIDs, ev.id)
		default:
			copiedIDs = append(copiedIDs, ev.id)
		}
	}

	if len(movedIDs) > 0 {
		if err := moveEventsWith(exec, movedIDs, petID, toUserID); err != nil {
			return err
		}
	}
	if len(detachedIDs) > 0 {
		// Удалённое событие, общее с другими питомцами отправителя, не
		// копируется: достаточно отвязать передаваемого питомца.
		if _, err := exec.Exec(`DELETE FROM event_pet WHERE event_id = ANY($1) AND pet_id = $2`, pq.Array(detachedIDs), petID); err != nil {
			return err
		}
	}
	if len(copiedIDs) > 0 {
		return copyEventsForPetWith(exec, copiedIDs, petID, toUserID)
	}
	return nil
}

// moveEventsWith передаёт получателю события eventIDs целиком: меняет
// владельца записи и её файлов и удаляет связи с остальными (скрытыми)
// питомцами. Ключ идемпотентности уникален в пределах пользователя, поэтому
// перенесённое событие его теряет — иначе возможен конфликт с ключами
// получателя. Все три изменения выполняются одним запросом.
func moveEventsWith(exec dbExecutor, eventIDs []uuid.UUID, petID uuid.UUID, toUserID string) error {
	_, err := exec.Exec(`
		WITH moved_events AS (
			UPDATE event SET user_id = $1, idempotency_key = NULL WHERE id = ANY($2)
		), dropped_links AS (
			DELETE FROM event_pet WHERE event_id = ANY($2) AND pet_id <> $3
		)
		UPDATE file SET user_id = $1 WHERE owner_type = $4 AND owner_id = ANY($2)
	`, toUserID, pq.Array(eventIDs), petID, eventFileOwnerType)
	return err
}

// copyEventsForPetWith создаёт для получателя копии событий eventIDs, у
// каждой одна связь — с питомцем petID — и ссылочные строки file; отвязывает
// питомца от исходных событий и переключает на копии ссылку на событие
// введения у вакцинаций питомца. Идентификаторы копий выдаются заранее, чтобы
// всё выполнялось одним запросом: проверки внешних ключей срабатывают после
// него, поэтому порядок частей внутри запроса не важен.
func copyEventsForPetWith(exec dbExecutor, eventIDs []uuid.UUID, petID uuid.UUID, toUserID string) error {
	copyIDs := make([]uuid.UUID, len(eventIDs))
	for i := range copyIDs {
		copyIDs[i] = uuid.New()
	}
	_, err := exec.Exec(`
		WITH copies AS (
			SELECT m.source_id, m.copy_id
			FROM unnest($1::uuid[], $2::uuid[]) AS m(source_id, copy_id)
		), copied_events AS (
			INSERT INTO event (id, user_id, date_time, type, notes, value)
			SELECT c.copy_id, $3, e.date_time, e.type, e.notes, e.value
			FROM copies c JOIN event e ON e.id = c.source_id
		), copied_links AS (
			INSERT INTO event_pet (event_id, pet_id, position)
			SELECT c.copy_id, $4, 1 FROM copies c
		), copied_files AS (
			INSERT INTO file (id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at)
			SELECT gen_random_uuid(), $5, c.copy_id, $3, f.object_key, f.content_type, f.filename,
			       ROW_NUMBER() OVER (PARTITION BY c.copy_id ORDER BY f.position) - 1, now()
			FROM copies c
			JOIN file f ON f.owner_type = $5 AND f.owner_id = c.source_id AND f.confirmed_at IS NOT NULL
		), detached_links AS (
			DELETE FROM event_pet WHERE event_id = ANY($1) AND pet_id = $4
		)
		UPDATE vaccination v SET administered_event_id = c.copy_id
		FROM copies c
		WHERE v.pet_id = $4 AND v.administered_event_id = c.source_id
	`, pq.Array(eventIDs), pq.Array(copyIDs), toUserID, petID, eventFileOwnerType)
	return err
}

// transferPetFilesWith передаёт получателю файлы фотографии питомца и записей
// Ветпаспорта: меняется только file.user_id, объекты S3 и ключи не меняются.
func transferPetFilesWith(exec dbExecutor, petID uuid.UUID, toUserID string) error {
	// Владельцы файлов собираются подзапросом из всех сущностей питомца, а
	// владелец меняется одним запросом; имена таблиц и owner_type берутся
	// только из petTransferFileOwnerTables.
	owners := `SELECT 'pet_photo'::text AS owner_type, $2::uuid AS owner_id`
	for _, entry := range petTransferFileOwnerTables {
		owners += ` UNION ALL SELECT '` + entry.ownerType + `', id FROM ` + entry.table + ` WHERE pet_id = $2`
	}
	_, err := exec.Exec(`
		UPDATE file f SET user_id = $1
		FROM (`+owners+`) o
		WHERE f.owner_type = o.owner_type AND f.owner_id = o.owner_id
	`, toUserID, petID)
	return err
}
