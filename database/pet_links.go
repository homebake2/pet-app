package database

import (
	"myauthservice/models"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Записи (события и настройки напоминания) связаны с питомцами таблицами
// связи event_pet и reminder_plan_pet — по строке на пару «запись — питомец».
// Эти две таблицы устроены одинаково, поэтому чтение, привязка и отвязка
// питомцев реализованы один раз и параметризованы именем таблицы и столбца
// записи.
//
// Видимые питомцы записи — привязанные питомцы, не мягко удалённые. Запись
// показывается и доступна, пока у неё есть хотя бы один видимый питомец; в
// её списке питомцев отдаются только видимые.

// petLinks описывает таблицу связи записи с питомцами.
type petLinks struct {
	table       string
	ownerColumn string
}

var (
	eventPetLinks        = petLinks{table: "event_pet", ownerColumn: "event_id"}
	reminderPlanPetLinks = petLinks{table: "reminder_plan_pet", ownerColumn: "plan_id"}
)

// LinkedPet — питомец записи: данные, нужные ответам API (имя) и проверкам
// применимости типа события (вид).
type LinkedPet struct {
	ID      uuid.UUID
	Name    string
	Species string
}

// EventPetRefs превращает питомцев записи в элементы pets ответа API.
func EventPetRefs(pets []LinkedPet) []models.EventPetRef {
	refs := make([]models.EventPetRef, 0, len(pets))
	for _, pet := range pets {
		refs = append(refs, models.EventPetRef{PetID: pet.ID.String(), PetName: pet.Name})
	}
	return refs
}

// visibleExistsClause — условие «у записи есть хотя бы один видимый питомец»
// для запроса, в котором запись доступна под алиасом ownerAlias.
func (l petLinks) visibleExistsClause(ownerAlias, ownerIDColumn string) string {
	return `EXISTS (
		SELECT 1 FROM ` + l.table + ` link
		JOIN pet link_pet ON link_pet.id = link.pet_id
		WHERE link.` + l.ownerColumn + ` = ` + ownerAlias + `.` + ownerIDColumn + `
		AND link_pet.deleted_at IS NULL
	)`
}

// insert привязывает питомцев к записи в порядке petIDs (position = номер в
// списке, начиная с единицы). Уже существующие связи не меняются.
func (l petLinks) insert(exec dbExecutor, ownerID uuid.UUID, petIDs []uuid.UUID) error {
	_, err := exec.Exec(`
		INSERT INTO `+l.table+` (`+l.ownerColumn+`, pet_id, position)
		SELECT $1, t.pet_id, t.ord::int
		FROM unnest($2::uuid[]) WITH ORDINALITY AS t(pet_id, ord)
		ON CONFLICT DO NOTHING
	`, ownerID, pq.Array(petIDs))
	return err
}

// syncVisible приводит видимых питомцев записи к желаемому набору desired:
// связи видимых питомцев, не вошедших в набор, удаляются, связи отсутствующих
// питомцев добавляются после уже существующих. Связи с мягко удалёнными
// питомцами не затрагиваются. Повторный вызов с тем же набором ничего не
// меняет.
func (l petLinks) syncVisible(exec dbExecutor, ownerID uuid.UUID, desired []uuid.UUID) error {
	if _, err := exec.Exec(`
		DELETE FROM `+l.table+` link
		USING pet
		WHERE link.`+l.ownerColumn+` = $1
		AND pet.id = link.pet_id
		AND pet.deleted_at IS NULL
		AND NOT (link.pet_id = ANY($2::uuid[]))
	`, ownerID, pq.Array(desired)); err != nil {
		return err
	}
	_, err := exec.Exec(`
		INSERT INTO `+l.table+` (`+l.ownerColumn+`, pet_id, position)
		SELECT $1, t.pet_id,
		       (SELECT COALESCE(MAX(position), 0) FROM `+l.table+` WHERE `+l.ownerColumn+` = $1) + t.ord::int
		FROM unnest($2::uuid[]) WITH ORDINALITY AS t(pet_id, ord)
		ON CONFLICT DO NOTHING
	`, ownerID, pq.Array(desired))
	return err
}

// visiblePets возвращает видимых питомцев записей ownerIDs в порядке
// привязки. Записи без видимых питомцев в результате отсутствуют.
func (l petLinks) visiblePets(exec dbExecutor, ownerIDs []uuid.UUID) (map[uuid.UUID][]LinkedPet, error) {
	result := make(map[uuid.UUID][]LinkedPet, len(ownerIDs))
	if len(ownerIDs) == 0 {
		return result, nil
	}
	rows, err := exec.Query(`
		SELECT link.`+l.ownerColumn+`, pet.id, pet.name, pet.species
		FROM `+l.table+` link
		JOIN pet ON pet.id = link.pet_id
		WHERE link.`+l.ownerColumn+` = ANY($1) AND pet.deleted_at IS NULL
		ORDER BY link.`+l.ownerColumn+`, link.position, pet.id
	`, pq.Array(ownerIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var ownerID uuid.UUID
		var pet LinkedPet
		if err := rows.Scan(&ownerID, &pet.ID, &pet.Name, &pet.Species); err != nil {
			return nil, err
		}
		result[ownerID] = append(result[ownerID], pet)
	}
	return result, rows.Err()
}

// OwnedPet — питомец пользователя вместе с признаком мягкого удаления: по
// нему различаются «не найден или чужой» (404) и «мягко удалён» (400).
type OwnedPet struct {
	LinkedPet
	Deleted bool
}

// GetOwnedPetsWith возвращает питомцев petIDs, принадлежащих userID (в том
// числе мягко удалённых). Чужие и несуществующие питомцы в результате
// отсутствуют.
func GetOwnedPetsWith(exec dbExecutor, userID string, petIDs []uuid.UUID) (map[uuid.UUID]OwnedPet, error) {
	rows, err := exec.Query(`
		SELECT id, name, species, deleted_at IS NOT NULL
		FROM pet
		WHERE id = ANY($1) AND user_id = $2
	`, pq.Array(petIDs), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[uuid.UUID]OwnedPet, len(petIDs))
	for rows.Next() {
		var pet OwnedPet
		if err := rows.Scan(&pet.ID, &pet.Name, &pet.Species, &pet.Deleted); err != nil {
			return nil, err
		}
		result[pet.ID] = pet
	}
	return result, rows.Err()
}
