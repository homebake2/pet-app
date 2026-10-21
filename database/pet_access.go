// Доступ к питомцу: владелец (pet.user_id) выдаёт другому пользователю роль
// full (полный доступ) или view (только просмотр). Действующие доступы лежат
// в pet_access, приглашения и их история — в pet_access_invite и
// pet_access_invite_pet, уведомления о потере доступа — в pet_access_notice.
//
// Роль пользователя у питомца: owner (pet.user_id), full или view (строка
// pet_access); без роли доступа нет. Мягко удалённый питомец скрыт для всех
// ролей, кроме проверок «включая удалённых» (GetPetRoleIncludingDeletedWith),
// где владелец по-прежнему видит свои данные.
//
// Приглашение pending с expires_at <= now везде трактуется как expired, а
// строка переводится в expired при первом обращении по коду либо фоновой
// очисткой (ExpireStalePetAccessInvites). Действующие питомцы приглашения —
// не мягко удалённые питомцы из pet_access_invite_pet, которые всё ещё
// принадлежат from_user_id; приглашение без действующих питомцев неактивно и
// переводится в cancelled так же — лениво либо очисткой.
//
// Порядок блокировок: сначала строки питомцев (по возрастанию id), затем
// строка приглашения. Принятие передачи и удаление питомца берут блокировки
// в том же порядке.
package database

import (
	"database/sql"
	"errors"
	"log"
	"myauthservice/models"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Роли пользователя у питомца (значения поля access в ответах API).
const (
	PetRoleOwner = "owner"
	PetRoleFull  = "full"
	PetRoleView  = "view"
)

// Причины потери доступа (pet_access_notice.reason).
const (
	PetAccessLostRevoked        = "revoked"
	PetAccessLostPetDeleted     = "pet_deleted"
	PetAccessLostPetTransferred = "pet_transferred"
)

const (
	// PetAccessInviteTTL — срок действия кода приглашения с момента создания.
	PetAccessInviteTTL = 7 * 24 * time.Hour
	// PetAccessMaxMembers — наибольшее число участников питомца суммарно с
	// его активными приглашениями.
	PetAccessMaxMembers = 10
	// PetAccessMaxInvitePets — наибольшее число питомцев в одном приглашении.
	PetAccessMaxInvitePets = 20

	petAccessInvitePendingCodeIndex = "pet_access_invite_pending_code_idx"
)

var (
	// ErrForbidden — доступ к ресурсу есть, но роли недостаточно для операции.
	ErrForbidden = errors.New("access forbidden")
	// ErrAccessOwnPet — код приглашения использует его создатель.
	ErrAccessOwnPet = errors.New("pet access invite used by its sender")
	// ErrAccessAlreadyGranted — у получателя уже есть доступ ко всем питомцам
	// приглашения.
	ErrAccessAlreadyGranted = errors.New("pet access already granted")
	// ErrAccessLimitReached — у питомца участники и активные приглашения
	// достигли лимита.
	ErrAccessLimitReached = errors.New("pet access members limit reached")
	// ErrAccessCodeCollision — сгенерированный код совпал с кодом другого
	// активного приглашения или активной передачи; вызывающий код
	// генерирует новый код и повторяет.
	ErrAccessCodeCollision = errors.New("pet access invite code collision")
)

// CanWriteRole сообщает, разрешены ли роли операции записи над питомцем и его
// данными: владелец и полный доступ — да, только просмотр — нет.
func CanWriteRole(role string) bool {
	return role == PetRoleOwner || role == PetRoleFull
}

// ---------------------------------------------------------------------------
// Условия SQL: доступность питомцев и записей.
// ---------------------------------------------------------------------------

// petAccessibleClause — условие «пользователь (параметр userParam) — владелец
// питомца petAlias или его участник с любой ролью». Мягкое удаление питомца
// условием не учитывается.
func petAccessibleClause(petAlias, userParam string) string {
	return `(` + petAlias + `.user_id = ` + userParam + ` OR EXISTS (
		SELECT 1 FROM pet_access acc_pa
		WHERE acc_pa.pet_id = ` + petAlias + `.id AND acc_pa.user_id = ` + userParam + `
	))`
}

// petWritableClause — условие «пользователь — владелец питомца petAlias или
// участник с ролью full».
func petWritableClause(petAlias, userParam string) string {
	return `(` + petAlias + `.user_id = ` + userParam + ` OR EXISTS (
		SELECT 1 FROM pet_access acc_pa
		WHERE acc_pa.pet_id = ` + petAlias + `.id AND acc_pa.user_id = ` + userParam + ` AND acc_pa.role = 'full'
	))`
}

// readableClause — условие «запись (событие или настройки напоминания)
// видна пользователю userParam»: у неё есть хотя бы один видимый питомец и
// пользователю доступен каждый видимый питомец записи.
func (l petLinks) readableClause(ownerAlias, ownerIDColumn, userParam string) string {
	return l.visibleExistsClause(ownerAlias, ownerIDColumn) + ` AND NOT EXISTS (
		SELECT 1 FROM ` + l.table + ` acc_link
		JOIN pet acc_pet ON acc_pet.id = acc_link.pet_id
		WHERE acc_link.` + l.ownerColumn + ` = ` + ownerAlias + `.` + ownerIDColumn + `
		AND acc_pet.deleted_at IS NULL
		AND NOT ` + petAccessibleClause("acc_pet", userParam) + `
	)`
}

// ---------------------------------------------------------------------------
// Роль пользователя у питомца.
// ---------------------------------------------------------------------------

// GetPetRoleWith возвращает роль userID у не мягко удалённого питомца petID:
// owner, full или view. sql.ErrNoRows — питомца нет, он удалён либо у
// пользователя нет доступа.
func GetPetRoleWith(exec dbExecutor, petID uuid.UUID, userID string) (string, error) {
	var role string
	err := exec.QueryRow(`
		SELECT CASE WHEN p.user_id = $2 THEN 'owner' ELSE pa.role END
		FROM pet p
		LEFT JOIN pet_access pa ON pa.pet_id = p.id AND pa.user_id = $2
		WHERE p.id = $1 AND p.deleted_at IS NULL AND (p.user_id = $2 OR pa.user_id IS NOT NULL)
	`, petID, userID).Scan(&role)
	return role, err
}

// GetPetRole — GetPetRoleWith на основном подключении.
func GetPetRole(petID uuid.UUID, userID string) (string, error) {
	return GetPetRoleWith(DB, petID, userID)
}

// GetPetRoleIncludingDeleted возвращает роль userID у питомца petID, считая
// владельца и мягко удалённого питомца: владелец сохраняет доступ к данным
// удалённого питомца (очистка истории), участник — нет. sql.ErrNoRows —
// питомца нет либо у пользователя нет доступа.
func GetPetRoleIncludingDeleted(petID uuid.UUID, userID string) (string, error) {
	var role string
	err := DB.QueryRow(`
		SELECT CASE WHEN p.user_id = $2 THEN 'owner' ELSE pa.role END
		FROM pet p
		LEFT JOIN pet_access pa ON pa.pet_id = p.id AND pa.user_id = $2
		WHERE p.id = $1 AND (p.user_id = $2 OR (pa.user_id IS NOT NULL AND p.deleted_at IS NULL))
	`, petID, userID).Scan(&role)
	return role, err
}

// AccessiblePet — питомец, доступный пользователю, вместе с его ролью,
// владельцем и признаком мягкого удаления.
type AccessiblePet struct {
	LinkedPet
	Deleted bool
	Role    string
	OwnerID string
}

// GetAccessiblePetsWith возвращает питомцев petIDs, доступных userID: свои
// (в том числе мягко удалённые, по ним различаются 404 и 400) и чужие,
// выданные ему в доступ (мягко удалённые чужие скрыты). Недоступные и
// несуществующие питомцы в результате отсутствуют.
func GetAccessiblePetsWith(exec dbExecutor, userID string, petIDs []uuid.UUID) (map[uuid.UUID]AccessiblePet, error) {
	rows, err := exec.Query(`
		SELECT p.id, p.name, p.species, p.deleted_at IS NOT NULL, p.user_id::text,
		       CASE WHEN p.user_id = $2 THEN 'owner' ELSE pa.role END
		FROM pet p
		LEFT JOIN pet_access pa ON pa.pet_id = p.id AND pa.user_id = $2
		WHERE p.id = ANY($1) AND (p.user_id = $2 OR (pa.user_id IS NOT NULL AND p.deleted_at IS NULL))
	`, pq.Array(petIDs), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[uuid.UUID]AccessiblePet, len(petIDs))
	for rows.Next() {
		var pet AccessiblePet
		if err := rows.Scan(&pet.ID, &pet.Name, &pet.Species, &pet.Deleted, &pet.OwnerID, &pet.Role); err != nil {
			return nil, err
		}
		result[pet.ID] = pet
	}
	return result, rows.Err()
}

// EnsureWritableWith возвращает ErrForbidden, если хотя бы у одного из
// питомцев у userID нет роли owner или full. Питомцы должны быть уже признаны
// доступными пользователю.
func EnsureWritableWith(exec dbExecutor, userID string, pets []LinkedPet) error {
	if len(pets) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(pets))
	for i, pet := range pets {
		ids[i] = pet.ID
	}
	var notWritable bool
	err := exec.QueryRow(`
		SELECT EXISTS (SELECT 1 FROM pet p WHERE p.id = ANY($1) AND NOT `+petWritableClause("p", "$2")+`)
	`, pq.Array(ids), userID).Scan(&notWritable)
	if err != nil {
		return err
	}
	if notWritable {
		return ErrForbidden
	}
	return nil
}

// AccessCheck — результат проверки права на операцию над записью или
// питомцем для generic-механизма файлов: нет доступа вообще (404), доступ
// есть, но роли недостаточно (403), операция разрешена.
type AccessCheck int

const (
	AccessNone AccessCheck = iota
	AccessReadOnly
	AccessWritable
)

// accessCheckFromRole переводит роль в результат проверки записи.
func accessCheckFromRole(role string) AccessCheck {
	if CanWriteRole(role) {
		return AccessWritable
	}
	return AccessReadOnly
}

// accessCheckForPets переводит набор видимых питомцев записи, доступной
// пользователю на чтение, в результат проверки записи.
func accessCheckForPets(exec dbExecutor, userID string, pets []LinkedPet) (AccessCheck, error) {
	err := EnsureWritableWith(exec, userID, pets)
	if errors.Is(err, ErrForbidden) {
		return AccessReadOnly, nil
	}
	if err != nil {
		return AccessNone, err
	}
	return AccessWritable, nil
}

// ---------------------------------------------------------------------------
// Автор записи.
// ---------------------------------------------------------------------------

// AuthorRef — запись, для которой определяется автор: идентификатор автора и
// видимые питомцы записи.
type AuthorRef struct {
	AuthorID string
	PetIDs   []uuid.UUID
}

// ResolveAuthorsWith возвращает для каждой записи refs поле author ответа:
// имя автора и признак «это сам viewerID». Поле заполняется, только если у
// записи есть видимый питомец, которым пользуется минимум два человека
// (у питомца есть хотя бы один участник); иначе nil.
func ResolveAuthorsWith(exec dbExecutor, viewerID string, refs []AuthorRef) ([]*models.EventAuthor, error) {
	authors := make([]*models.EventAuthor, len(refs))

	petSet := make(map[uuid.UUID]bool)
	var petIDs []uuid.UUID
	for _, ref := range refs {
		for _, id := range ref.PetIDs {
			if !petSet[id] {
				petSet[id] = true
				petIDs = append(petIDs, id)
			}
		}
	}
	if len(petIDs) == 0 {
		return authors, nil
	}

	shared := make(map[uuid.UUID]bool)
	rows, err := exec.Query(`
		SELECT DISTINCT pet_id FROM pet_access WHERE pet_id = ANY($1)
	`, pq.Array(petIDs))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		shared[id] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(shared) == 0 {
		return authors, nil
	}

	needsAuthor := make([]bool, len(refs))
	authorSet := make(map[string]bool)
	var authorIDs []string
	for i, ref := range refs {
		if ref.AuthorID == "" {
			continue
		}
		for _, id := range ref.PetIDs {
			if shared[id] {
				needsAuthor[i] = true
				break
			}
		}
		if needsAuthor[i] && !authorSet[ref.AuthorID] {
			authorSet[ref.AuthorID] = true
			authorIDs = append(authorIDs, ref.AuthorID)
		}
	}
	if len(authorIDs) == 0 {
		return authors, nil
	}

	names := make(map[string]string, len(authorIDs))
	nameRows, err := exec.Query(`
		SELECT u.id::text, COALESCE(NULLIF(trim(pr.first_name), ''), u.login)
		FROM users u
		LEFT JOIN profile pr ON pr.user_id = u.id
		WHERE u.id = ANY($1::uuid[])
	`, pq.Array(authorIDs))
	if err != nil {
		return nil, err
	}
	defer nameRows.Close()
	for nameRows.Next() {
		var id, name string
		if err := nameRows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
	}
	if err := nameRows.Err(); err != nil {
		return nil, err
	}

	for i, ref := range refs {
		if !needsAuthor[i] {
			continue
		}
		name, found := names[ref.AuthorID]
		if !found {
			continue
		}
		authors[i] = &models.EventAuthor{Name: name, IsMe: ref.AuthorID == viewerID}
	}
	return authors, nil
}

// ResolveAuthors — ResolveAuthorsWith на основном подключении.
func ResolveAuthors(viewerID string, refs []AuthorRef) ([]*models.EventAuthor, error) {
	return ResolveAuthorsWith(DB, viewerID, refs)
}

// GetVetPassportAuthorIDs возвращает идентификатор автора записей ids
// сущности ветпаспорта table: пользователь, создавший запись, а если его
// аккаунт удалён — владелец питомца.
func GetVetPassportAuthorIDs(table VetPassportTable, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	result := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := DB.Query(`
		SELECT t.id, COALESCE(t.user_id, p.user_id)::text
		FROM `+string(table)+` t
		JOIN pet p ON p.id = t.pet_id
		WHERE t.id = ANY($1)
	`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var authorID string
		if err := rows.Scan(&id, &authorID); err != nil {
			return nil, err
		}
		result[id] = authorID
	}
	return result, rows.Err()
}

// GetUserDisplayName возвращает имя пользователя для других пользователей:
// first_name профиля, а при отсутствии профиля — логин.
func GetUserDisplayName(userID string) (string, error) {
	var name string
	err := DB.QueryRow(`
		SELECT COALESCE(NULLIF(trim(pr.first_name), ''), u.login)
		FROM users u
		LEFT JOIN profile pr ON pr.user_id = u.id
		WHERE u.id = $1
	`, userID).Scan(&name)
	return name, err
}

// ---------------------------------------------------------------------------
// Приглашения владельца.
// ---------------------------------------------------------------------------

// PetAccessInvite — активное приглашение к доступу.
type PetAccessInvite struct {
	ID        uuid.UUID
	Code      string
	Role      string
	PetIDs    []uuid.UUID
	ExpiresAt time.Time
}

// PetAccessMember — участник питомца.
type PetAccessMember struct {
	UserID    uuid.UUID
	Name      string
	Role      string
	GrantedAt time.Time
}

// petAccessSlotsWith возвращает число занятых мест питомца: участники плюс
// активные приглашения, в которых питомец действующий. Приглашение excludeInvite
// не учитывается (uuid.Nil — учитываются все).
func petAccessSlotsWith(exec dbExecutor, petID uuid.UUID, excludeInvite uuid.UUID) (int, error) {
	var slots int
	err := exec.QueryRow(`
		SELECT (SELECT COUNT(*) FROM pet_access a WHERE a.pet_id = p.id)
		     + (SELECT COUNT(*) FROM pet_access_invite i
		        JOIN pet_access_invite_pet ip ON ip.invite_id = i.id
		        WHERE ip.pet_id = p.id AND i.status = 'pending' AND i.expires_at > now()
		        AND i.from_user_id = p.user_id AND i.id <> $2)
		FROM pet p WHERE p.id = $1 AND p.deleted_at IS NULL
	`, petID, excludeInvite).Scan(&slots)
	return slots, err
}

// CreatePetAccessInvite создаёт приглашение владельца ownerID к доступу role
// на питомцев petIDs. sql.ErrNoRows — какой-либо питомец не найден, чужой или
// мягко удалён; ErrAccessLimitReached — у какого-либо питомца нет свободного
// места; ErrAccessCodeCollision — код занят другим активным приглашением или
// передачей.
func CreatePetAccessInvite(ownerID string, petIDs []uuid.UUID, role, code string) (*PetAccessInvite, error) {
	invite := &PetAccessInvite{Code: code, Role: role, PetIDs: petIDs}
	err := RunInTx(func(tx *sql.Tx) error {
		sorted := append([]uuid.UUID(nil), petIDs...)
		rows, err := tx.Query(`
			SELECT id, name, species FROM pet
			WHERE id = ANY($1) AND user_id = $2 AND deleted_at IS NULL
			ORDER BY id
			FOR UPDATE
		`, pq.Array(sorted), ownerID)
		if err != nil {
			return err
		}
		snapshots := make(map[uuid.UUID]LinkedPet, len(petIDs))
		for rows.Next() {
			var pet LinkedPet
			if err := rows.Scan(&pet.ID, &pet.Name, &pet.Species); err != nil {
				rows.Close()
				return err
			}
			snapshots[pet.ID] = pet
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(snapshots) != len(petIDs) {
			return sql.ErrNoRows
		}

		for _, petID := range petIDs {
			slots, err := petAccessSlotsWith(tx, petID, uuid.Nil)
			if err != nil {
				return err
			}
			if slots >= PetAccessMaxMembers {
				return ErrAccessLimitReached
			}
		}

		var transferCodeTaken bool
		if err := tx.QueryRow(`
			SELECT EXISTS (SELECT 1 FROM pet_transfer WHERE code = $1 AND status = 'pending' AND expires_at > now())
		`, code).Scan(&transferCodeTaken); err != nil {
			return err
		}
		if transferCodeTaken {
			return ErrAccessCodeCollision
		}

		err = tx.QueryRow(`
			INSERT INTO pet_access_invite (from_user_id, role, code, status, expires_at)
			VALUES ($1, $2, $3, 'pending', now() + make_interval(secs => $4))
			RETURNING id, expires_at
		`, ownerID, role, code, PetAccessInviteTTL.Seconds()).Scan(&invite.ID, &invite.ExpiresAt)
		if err != nil {
			var pqErr *pq.Error
			if errors.As(err, &pqErr) && pqErr.Code == "23505" && pqErr.Constraint == petAccessInvitePendingCodeIndex {
				return ErrAccessCodeCollision
			}
			return err
		}

		for _, petID := range petIDs {
			snapshot := snapshots[petID]
			if _, err := tx.Exec(`
				INSERT INTO pet_access_invite_pet (invite_id, pet_id, pet_name, pet_species)
				VALUES ($1, $2, $3, $4)
			`, invite.ID, petID, snapshot.Name, snapshot.Species); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrAccessLimitReached) && !errors.Is(err, ErrAccessCodeCollision) {
			log.Println("CreatePetAccessInvite error:", err)
		}
		return nil, err
	}
	return invite, nil
}

// ListPetAccess возвращает участников питомца и его активные приглашения
// (оба списка — по возрастанию created_at). Питомец должен быть уже признан
// принадлежащим вызывающему владельцу.
func ListPetAccess(petID uuid.UUID) (members []PetAccessMember, invites []PetAccessInvite, err error) {
	members = []PetAccessMember{}
	invites = []PetAccessInvite{}

	rows, err := DB.Query(`
		SELECT a.user_id, COALESCE(NULLIF(trim(pr.first_name), ''), u.login), a.role, a.created_at
		FROM pet_access a
		JOIN users u ON u.id = a.user_id
		LEFT JOIN profile pr ON pr.user_id = a.user_id
		WHERE a.pet_id = $1
		ORDER BY a.created_at, a.user_id
	`, petID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var member PetAccessMember
		if err := rows.Scan(&member.UserID, &member.Name, &member.Role, &member.GrantedAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()

	inviteRows, err := DB.Query(`
		SELECT i.id, i.code, i.role, i.expires_at
		FROM pet_access_invite i
		JOIN pet_access_invite_pet ip ON ip.invite_id = i.id AND ip.pet_id = $1
		JOIN pet p ON p.id = ip.pet_id AND p.deleted_at IS NULL AND p.user_id = i.from_user_id
		WHERE i.status = 'pending' AND i.expires_at > now()
		ORDER BY i.created_at, i.id
	`, petID)
	if err != nil {
		return nil, nil, err
	}
	var inviteIDs []uuid.UUID
	for inviteRows.Next() {
		var invite PetAccessInvite
		if err := inviteRows.Scan(&invite.ID, &invite.Code, &invite.Role, &invite.ExpiresAt); err != nil {
			inviteRows.Close()
			return nil, nil, err
		}
		invites = append(invites, invite)
		inviteIDs = append(inviteIDs, invite.ID)
	}
	if err := inviteRows.Err(); err != nil {
		inviteRows.Close()
		return nil, nil, err
	}
	inviteRows.Close()
	if len(invites) == 0 {
		return members, invites, nil
	}

	petsByInvite, err := effectiveInvitePetsWith(DB, inviteIDs)
	if err != nil {
		return nil, nil, err
	}
	for i := range invites {
		invites[i].PetIDs = petsByInvite[invites[i].ID]
	}
	return members, invites, nil
}

// effectiveInvitePetsWith возвращает действующих питомцев приглашений
// inviteIDs (не мягко удалённые и принадлежащие создателю приглашения) по
// возрастанию id.
func effectiveInvitePetsWith(exec dbExecutor, inviteIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	rows, err := exec.Query(`
		SELECT ip.invite_id, ip.pet_id
		FROM pet_access_invite_pet ip
		JOIN pet_access_invite i ON i.id = ip.invite_id
		JOIN pet p ON p.id = ip.pet_id AND p.deleted_at IS NULL AND p.user_id = i.from_user_id
		WHERE ip.invite_id = ANY($1)
		ORDER BY ip.invite_id, ip.pet_id
	`, pq.Array(inviteIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[uuid.UUID][]uuid.UUID, len(inviteIDs))
	for rows.Next() {
		var inviteID, petID uuid.UUID
		if err := rows.Scan(&inviteID, &petID); err != nil {
			return nil, err
		}
		result[inviteID] = append(result[inviteID], petID)
	}
	return result, rows.Err()
}

// UpdatePetAccessRole меняет роль участника userID у питомца petID.
// sql.ErrNoRows — участника нет.
func UpdatePetAccessRole(petID uuid.UUID, userID, role string) error {
	result, err := DB.Exec(`
		UPDATE pet_access SET role = $3, updated_at = now() WHERE pet_id = $1 AND user_id = $2
	`, petID, userID, role)
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

// RevokePetAccess отзывает доступ участника userID к питомцу petID и создаёт
// ему уведомление о потере доступа с причиной revoked. sql.ErrNoRows —
// участника нет.
func RevokePetAccess(petID uuid.UUID, userID string) error {
	return RunInTx(func(tx *sql.Tx) error {
		result, err := tx.Exec(`DELETE FROM pet_access WHERE pet_id = $1 AND user_id = $2`, petID, userID)
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
		_, err = tx.Exec(`
			INSERT INTO pet_access_notice (user_id, pet_id, pet_name, pet_species, reason)
			SELECT $2, p.id, p.name, p.species, $3 FROM pet p WHERE p.id = $1
			ON CONFLICT (user_id, pet_id) DO UPDATE
			SET pet_name = EXCLUDED.pet_name, pet_species = EXCLUDED.pet_species,
			    reason = EXCLUDED.reason, created_at = now()
		`, petID, userID, PetAccessLostRevoked)
		return err
	})
}

// LeavePetAccess удаляет собственный доступ участника userID к питомцу petID
// без уведомления. sql.ErrNoRows — доступа нет.
func LeavePetAccess(petID uuid.UUID, userID string) error {
	result, err := DB.Exec(`DELETE FROM pet_access WHERE pet_id = $1 AND user_id = $2`, petID, userID)
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

// CancelPetAccessInvite переводит активное приглашение inviteID владельца
// ownerID, в состав которого входит питомец petID, в cancelled. sql.ErrNoRows
// — приглашения нет, оно не активно или не включает питомца.
func CancelPetAccessInvite(petID, inviteID uuid.UUID, ownerID string) error {
	result, err := DB.Exec(`
		UPDATE pet_access_invite i SET status = 'cancelled', resolved_at = now()
		WHERE i.id = $1 AND i.from_user_id = $2 AND i.status = 'pending' AND i.expires_at > now()
		AND EXISTS (
			SELECT 1 FROM pet_access_invite_pet ip
			JOIN pet p ON p.id = ip.pet_id AND p.deleted_at IS NULL AND p.user_id = i.from_user_id
			WHERE ip.invite_id = i.id AND ip.pet_id = $3
		)
	`, inviteID, ownerID, petID)
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

// ---------------------------------------------------------------------------
// Приглашения по коду: предпросмотр, отклонение, принятие.
// ---------------------------------------------------------------------------

// PendingPetAccessInvite — найденное по коду активное приглашение вместе с
// его действующими питомцами.
type PendingPetAccessInvite struct {
	ID         uuid.UUID
	FromUserID string
	Role       string
	ExpiresAt  time.Time
	// PetIDs — действующие питомцы по возрастанию id.
	PetIDs []uuid.UUID
}

// findPendingPetAccessInviteByCodeWith ищет активное приглашение по
// нормализованному коду; истёкшее приглашение с этим кодом переводится в
// expired, приглашение без действующих питомцев — в cancelled. lock=true
// блокирует строку приглашения до конца транзакции. sql.ErrNoRows —
// приглашение не найдено или неактивно.
func findPendingPetAccessInviteByCodeWith(exec dbExecutor, code string, lock bool) (*PendingPetAccessInvite, error) {
	if _, err := exec.Exec(`
		UPDATE pet_access_invite SET status = 'expired', resolved_at = now()
		WHERE code = $1 AND status = 'pending' AND expires_at <= now()
	`, code); err != nil {
		return nil, err
	}
	query := `
		SELECT id, from_user_id::text, role, expires_at FROM pet_access_invite
		WHERE code = $1 AND status = 'pending' AND expires_at > now()`
	if lock {
		query += ` FOR UPDATE`
	}
	var invite PendingPetAccessInvite
	var fromUserID sql.NullString
	if err := exec.QueryRow(query, code).Scan(&invite.ID, &fromUserID, &invite.Role, &invite.ExpiresAt); err != nil {
		return nil, err
	}
	invite.FromUserID = fromUserID.String

	pets, err := effectiveInvitePetsWith(exec, []uuid.UUID{invite.ID})
	if err != nil {
		return nil, err
	}
	invite.PetIDs = pets[invite.ID]
	if len(invite.PetIDs) == 0 {
		if _, err := exec.Exec(`
			UPDATE pet_access_invite SET status = 'cancelled', resolved_at = now() WHERE id = $1
		`, invite.ID); err != nil {
			return nil, err
		}
		return nil, sql.ErrNoRows
	}
	return &invite, nil
}

// grantablePetIDsWith возвращает питомцев petIDs, к которым у userID ещё нет
// доступа.
func grantablePetIDsWith(exec dbExecutor, petIDs []uuid.UUID, userID string) ([]uuid.UUID, error) {
	return uuidColumnWith(exec, `
		SELECT p.id FROM pet p
		WHERE p.id = ANY($1) AND NOT EXISTS (
			SELECT 1 FROM pet_access a WHERE a.pet_id = p.id AND a.user_id = $2
		)
		ORDER BY p.id
	`, pq.Array(petIDs), userID)
}

// PetAccessPreviewPet — данные питомца для предпросмотра приглашения.
type PetAccessPreviewPet struct {
	ID        uuid.UUID
	Name      string
	Species   string
	Breed     sql.NullString
	Gender    sql.NullString
	BirthDate sql.NullTime
}

// PetAccessPreview — результат предпросмотра приглашения: питомцы, доступ к
// которым будет выдан, роль, создатель и срок.
type PetAccessPreview struct {
	Pets       []PetAccessPreviewPet
	Role       string
	FromUserID string
	ExpiresAt  time.Time
}

// checkInviteForUserWith выполняет проверки получателя, общие для
// предпросмотра, отклонения и принятия: приглашение не своё
// (ErrAccessOwnPet) и у пользователя нет доступа ко всем питомцам
// (ErrAccessAlreadyGranted). Возвращает питомцев, доступ к которым будет
// выдан.
func checkInviteForUserWith(exec dbExecutor, invite *PendingPetAccessInvite, userID string) ([]uuid.UUID, error) {
	if invite.FromUserID == userID {
		return nil, ErrAccessOwnPet
	}
	grantable, err := grantablePetIDsWith(exec, invite.PetIDs, userID)
	if err != nil {
		return nil, err
	}
	if len(grantable) == 0 {
		return nil, ErrAccessAlreadyGranted
	}
	return grantable, nil
}

// PreviewPetAccessInvite возвращает данные приглашения по коду для
// получателя userID, ничего не меняя. sql.ErrNoRows — приглашение не найдено
// или неактивно; ErrAccessOwnPet; ErrAccessAlreadyGranted.
func PreviewPetAccessInvite(code, userID string) (*PetAccessPreview, error) {
	invite, err := findPendingPetAccessInviteByCodeWith(DB, code, false)
	if err != nil {
		return nil, err
	}
	grantable, err := checkInviteForUserWith(DB, invite, userID)
	if err != nil {
		return nil, err
	}

	rows, err := DB.Query(`
		SELECT id, name, species, breed, gender, birth_date
		FROM pet WHERE id = ANY($1)
		ORDER BY created_at, id
	`, pq.Array(grantable))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	preview := &PetAccessPreview{Role: invite.Role, FromUserID: invite.FromUserID, ExpiresAt: invite.ExpiresAt}
	for rows.Next() {
		var pet PetAccessPreviewPet
		if err := rows.Scan(&pet.ID, &pet.Name, &pet.Species, &pet.Breed, &pet.Gender, &pet.BirthDate); err != nil {
			return nil, err
		}
		preview.Pets = append(preview.Pets, pet)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return preview, nil
}

// DeclinePetAccessInvite переводит активное приглашение по коду в declined от
// имени userID. Ошибки — как у PreviewPetAccessInvite.
func DeclinePetAccessInvite(code, userID string) error {
	return RunInTx(func(tx *sql.Tx) error {
		invite, err := findPendingPetAccessInviteByCodeWith(tx, code, true)
		if err != nil {
			return err
		}
		if _, err := checkInviteForUserWith(tx, invite, userID); err != nil {
			return err
		}
		_, err = tx.Exec(`
			UPDATE pet_access_invite SET status = 'declined', to_user_id = $2, resolved_at = now() WHERE id = $1
		`, invite.ID, userID)
		return err
	})
}

// AcceptPetAccessInvite принимает приглашение по коду от имени userID в одной
// транзакции и возвращает питомцев, доступ к которым появился. Питомцы
// блокируются раньше приглашения; набор действующих питомцев, срок и лимит
// участников проверяются повторно под блокировками. sql.ErrNoRows —
// приглашение не найдено или неактивно; ErrAccessOwnPet;
// ErrAccessAlreadyGranted; ErrAccessLimitReached — ни одному питомцу не
// удалось выдать доступ.
func AcceptPetAccessInvite(code, userID string) ([]uuid.UUID, error) {
	var granted []uuid.UUID
	err := RunInTx(func(tx *sql.Tx) error {
		granted = nil
		probe, err := findPendingPetAccessInviteByCodeWith(tx, code, false)
		if err != nil {
			return err
		}
		if _, err := uuidColumnWith(tx, `
			SELECT id FROM pet WHERE id = ANY($1) ORDER BY id FOR UPDATE
		`, pq.Array(probe.PetIDs)); err != nil {
			return err
		}

		invite, err := findPendingPetAccessInviteByCodeWith(tx, code, true)
		if err != nil {
			return err
		}
		grantable, err := checkInviteForUserWith(tx, invite, userID)
		if err != nil {
			return err
		}

		for _, petID := range grantable {
			slots, err := petAccessSlotsWith(tx, petID, invite.ID)
			if err != nil {
				return err
			}
			if slots >= PetAccessMaxMembers {
				continue
			}
			if _, err := tx.Exec(`
				INSERT INTO pet_access (pet_id, user_id, role, granted_by)
				VALUES ($1, $2, $3, $4)
			`, petID, userID, invite.Role, invite.FromUserID); err != nil {
				return err
			}
			if _, err := tx.Exec(`
				DELETE FROM pet_access_notice WHERE user_id = $1 AND pet_id = $2
			`, userID, petID); err != nil {
				return err
			}
			granted = append(granted, petID)
		}
		if len(granted) == 0 {
			return ErrAccessLimitReached
		}

		_, err = tx.Exec(`
			UPDATE pet_access_invite SET status = 'accepted', to_user_id = $2, resolved_at = now() WHERE id = $1
		`, invite.ID, userID)
		return err
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrAccessOwnPet) &&
			!errors.Is(err, ErrAccessAlreadyGranted) && !errors.Is(err, ErrAccessLimitReached) {
			log.Println("AcceptPetAccessInvite error:", err)
		}
		return nil, err
	}
	return granted, nil
}

// ExpireStalePetAccessInvites переводит истёкшие активные приглашения в
// expired, а приглашения без действующих питомцев — в cancelled; возвращает
// число изменённых приглашений (фоновая очистка).
func ExpireStalePetAccessInvites() (int64, error) {
	expired, err := DB.Exec(`
		UPDATE pet_access_invite SET status = 'expired', resolved_at = now()
		WHERE status = 'pending' AND expires_at <= now()
	`)
	if err != nil {
		return 0, err
	}
	expiredCount, err := expired.RowsAffected()
	if err != nil {
		return 0, err
	}
	cancelled, err := DB.Exec(`
		UPDATE pet_access_invite i SET status = 'cancelled', resolved_at = now()
		WHERE i.status = 'pending' AND NOT EXISTS (
			SELECT 1 FROM pet_access_invite_pet ip
			JOIN pet p ON p.id = ip.pet_id AND p.deleted_at IS NULL AND p.user_id = i.from_user_id
			WHERE ip.invite_id = i.id
		)
	`)
	if err != nil {
		return expiredCount, err
	}
	cancelledCount, err := cancelled.RowsAffected()
	return expiredCount + cancelledCount, err
}

// ---------------------------------------------------------------------------
// Уведомления о потере доступа.
// ---------------------------------------------------------------------------

// PetAccessNotice — уведомление пользователя о потере доступа к питомцу.
type PetAccessNotice struct {
	PetID      uuid.UUID
	Name       string
	Species    string
	Reason     string
	OccurredAt time.Time
}

// ListPetAccessNotices возвращает уведомления пользователя о потере доступа
// по убыванию времени.
func ListPetAccessNotices(userID string) ([]PetAccessNotice, error) {
	rows, err := DB.Query(`
		SELECT pet_id, pet_name, pet_species, reason, created_at
		FROM pet_access_notice
		WHERE user_id = $1
		ORDER BY created_at DESC, pet_id
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notices := []PetAccessNotice{}
	for rows.Next() {
		var notice PetAccessNotice
		if err := rows.Scan(&notice.PetID, &notice.Name, &notice.Species, &notice.Reason, &notice.OccurredAt); err != nil {
			return nil, err
		}
		notices = append(notices, notice)
	}
	return notices, rows.Err()
}

// DeletePetAccessNotice удаляет уведомление пользователя о потере доступа к
// питомцу petID; отсутствие уведомления не ошибка.
func DeletePetAccessNotice(userID string, petID uuid.UUID) error {
	_, err := DB.Exec(`DELETE FROM pet_access_notice WHERE user_id = $1 AND pet_id = $2`, userID, petID)
	return err
}

// ---------------------------------------------------------------------------
// Завершение доступа при удалении и передаче питомца, удалении аккаунта.
// ---------------------------------------------------------------------------

// recordPetAccessLostWith записывает участникам питомцев petIDs, кроме
// exceptUserID, уведомление о потере доступа с причиной reason (новое
// заменяет прежнее). Строки pet_access не меняются.
func recordPetAccessLostWith(exec dbExecutor, petIDs []uuid.UUID, reason, exceptUserID string) error {
	_, err := exec.Exec(`
		INSERT INTO pet_access_notice (user_id, pet_id, pet_name, pet_species, reason)
		SELECT a.user_id, p.id, p.name, p.species, $2
		FROM pet_access a
		JOIN pet p ON p.id = a.pet_id
		WHERE a.pet_id = ANY($1) AND a.user_id::text <> $3
		ON CONFLICT (user_id, pet_id) DO UPDATE
		SET pet_name = EXCLUDED.pet_name, pet_species = EXCLUDED.pet_species,
		    reason = EXCLUDED.reason, created_at = now()
	`, pq.Array(petIDs), reason, exceptUserID)
	return err
}

// cancelPetAccessInvitesWithoutPetsWith переводит в cancelled активные
// приглашения, в составе которых есть питомцы petIDs и не осталось ни одного
// действующего. Вызывается после того, как питомцы перестали быть
// действующими (мягко удалены или переданы).
func cancelPetAccessInvitesWithoutPetsWith(exec dbExecutor, petIDs []uuid.UUID) error {
	_, err := exec.Exec(`
		UPDATE pet_access_invite i SET status = 'cancelled', resolved_at = now()
		WHERE i.status = 'pending'
		AND EXISTS (SELECT 1 FROM pet_access_invite_pet ipx WHERE ipx.invite_id = i.id AND ipx.pet_id = ANY($1))
		AND NOT EXISTS (
			SELECT 1 FROM pet_access_invite_pet ip
			JOIN pet p ON p.id = ip.pet_id AND p.deleted_at IS NULL AND p.user_id = i.from_user_id
			WHERE ip.invite_id = i.id
		)
	`, pq.Array(petIDs))
	return err
}

// endPetAccessOnDeleteWith завершает доступ к мягко удаляемому питомцу:
// участники получают уведомление pet_deleted, строки pet_access остаются
// (доступ скрыт вместе с питомцем), приглашения без действующих питомцев
// отменяются. Вызывается после того, как питомец помечен удалённым.
func endPetAccessOnDeleteWith(exec dbExecutor, petID uuid.UUID) error {
	petIDs := []uuid.UUID{petID}
	if err := recordPetAccessLostWith(exec, petIDs, PetAccessLostPetDeleted, ""); err != nil {
		return err
	}
	return cancelPetAccessInvitesWithoutPetsWith(exec, petIDs)
}

// endPetAccessOnTransferWith завершает доступ к передаваемому питомцу:
// участники, кроме нового владельца, получают уведомление pet_transferred,
// все строки pet_access питомца удаляются, приглашения без действующих
// питомцев отменяются. Вызывается после смены владельца питомца.
func endPetAccessOnTransferWith(exec dbExecutor, petID uuid.UUID, newOwnerID string) error {
	petIDs := []uuid.UUID{petID}
	if err := recordPetAccessLostWith(exec, petIDs, PetAccessLostPetTransferred, newOwnerID); err != nil {
		return err
	}
	if _, err := exec.Exec(`DELETE FROM pet_access WHERE pet_id = $1`, petID); err != nil {
		return err
	}
	return cancelPetAccessInvitesWithoutPetsWith(exec, petIDs)
}

// clearPetAccessNoticesWith удаляет уведомления о потере доступа к питомцу
// (питомец восстановлен и снова доступен участникам).
func clearPetAccessNoticesWith(exec dbExecutor, petID uuid.UUID) error {
	_, err := exec.Exec(`DELETE FROM pet_access_notice WHERE pet_id = $1 AND reason = $2`, petID, PetAccessLostPetDeleted)
	return err
}

// endUserPetAccessWith подготавливает удаление аккаунта userID: участники его
// питомцев получают уведомление pet_deleted, его активные приглашения
// отменяются, записи про чужих питомцев, автором которых он был, переходят к
// владельцу этих питомцев, а записи других авторов про его питомцев
// удаляются вместе с ними (питомцы удаляются каскадом).
func endUserPetAccessWith(exec dbExecutor, userID string) error {
	if _, err := exec.Exec(`
		INSERT INTO pet_access_notice (user_id, pet_id, pet_name, pet_species, reason)
		SELECT a.user_id, p.id, p.name, p.species, $2
		FROM pet_access a
		JOIN pet p ON p.id = a.pet_id
		WHERE p.user_id = $1 AND p.deleted_at IS NULL
		ON CONFLICT (user_id, pet_id) DO UPDATE
		SET pet_name = EXCLUDED.pet_name, pet_species = EXCLUDED.pet_species,
		    reason = EXCLUDED.reason, created_at = now()
	`, userID, PetAccessLostPetDeleted); err != nil {
		return err
	}
	if _, err := exec.Exec(`
		UPDATE pet_access_invite SET status = 'cancelled', resolved_at = now()
		WHERE from_user_id = $1 AND status = 'pending'
	`, userID); err != nil {
		return err
	}

	// Записи других авторов про питомцев удаляемого владельца удаляются:
	// питомцы исчезают, и записи остались бы без связей.
	if _, err := exec.Exec(`
		DELETE FROM event WHERE id IN (
			SELECT ep.event_id FROM event_pet ep JOIN pet p ON p.id = ep.pet_id WHERE p.user_id = $1
		)
	`, userID); err != nil {
		return err
	}
	if _, err := exec.Exec(`
		DELETE FROM reminder_plan WHERE id IN (
			SELECT rpp.plan_id FROM reminder_plan_pet rpp JOIN pet p ON p.id = rpp.pet_id WHERE p.user_id = $1
		)
	`, userID); err != nil {
		return err
	}

	// Записи удаляемого автора про чужих питомцев переходят к владельцу этих
	// питомцев; ключ идемпотентности сбрасывается, чтобы не конфликтовать с
	// ключами владельца, а файлы записи переходят вместе с ней.
	if _, err := exec.Exec(`
		WITH reassigned AS (
			UPDATE event e SET user_id = o.owner_id, idempotency_key = NULL
			FROM (
				SELECT ep.event_id, MIN(p.user_id::text)::uuid AS owner_id
				FROM event_pet ep JOIN pet p ON p.id = ep.pet_id
				GROUP BY ep.event_id
				HAVING bool_or(p.user_id <> $1)
			) o
			WHERE e.id = o.event_id AND e.user_id = $1
			RETURNING e.id, o.owner_id
		)
		UPDATE file f SET user_id = r.owner_id
		FROM reassigned r
		WHERE f.owner_type = $2 AND f.owner_id = r.id
	`, userID, eventFileOwnerType); err != nil {
		return err
	}
	planRows, err := exec.Query(`
		UPDATE reminder_plan rp SET user_id = o.owner_id
		FROM (
			SELECT rpp.plan_id, MIN(p.user_id::text)::uuid AS owner_id
			FROM reminder_plan_pet rpp JOIN pet p ON p.id = rpp.pet_id
			GROUP BY rpp.plan_id
			HAVING bool_or(p.user_id <> $1)
		) o
		WHERE rp.id = o.plan_id AND rp.user_id = $1
		RETURNING rp.id, o.owner_id
	`, userID)
	if err != nil {
		return err
	}
	type reassignedPlan struct{ planID, ownerID uuid.UUID }
	var plans []reassignedPlan
	for planRows.Next() {
		var plan reassignedPlan
		if err := planRows.Scan(&plan.planID, &plan.ownerID); err != nil {
			planRows.Close()
			return err
		}
		plans = append(plans, plan)
	}
	if err := planRows.Err(); err != nil {
		planRows.Close()
		return err
	}
	planRows.Close()
	for _, plan := range plans {
		if _, err := exec.Exec(`
			UPDATE file SET user_id = $1
			WHERE (owner_type = $2 AND owner_id = $3)
			   OR (owner_type = $4 AND owner_id IN (SELECT id FROM reminder WHERE plan_id = $3))
		`, plan.ownerID, ReminderPlanFileOwnerType, plan.planID, ReminderFileOwnerType); err != nil {
			return err
		}
	}
	return nil
}

// ownerNameSQL — выражение имени владельца питомца для ответов API:
// first_name и last_name профиля (непустые, через пробел), а если профиля нет
// или имена пусты — логин. Профиль доступен под алиасом opr, пользователь —
// под ou.
const ownerNameSQL = `COALESCE(NULLIF(trim(concat_ws(' ', NULLIF(trim(opr.first_name), ''), NULLIF(trim(opr.last_name), ''))), ''), ou.login)`

// GetOwnerNames возвращает имена владельцев питомцев (id пользователя → имя)
// одним запросом.
func GetOwnerNames(userIDs []string) (map[string]string, error) {
	names := make(map[string]string, len(userIDs))
	if len(userIDs) == 0 {
		return names, nil
	}
	rows, err := DB.Query(`
		SELECT ou.id::text, `+ownerNameSQL+`
		FROM users ou
		LEFT JOIN profile opr ON opr.user_id = ou.id
		WHERE ou.id = ANY($1::uuid[])
	`, pq.Array(userIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
	}
	return names, rows.Err()
}
