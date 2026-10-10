package handlers

import (
	"myauthservice/models"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// Общие ожидания sqlmock для событий, привязанных к питомцам через
// event_pet: загрузка питомцев пользователя, чтение события и его видимых
// питомцев, создание и привязка/отвязка.

const (
	testPetID2   = "66666666-6666-6666-6666-666666666666"
	testPetID3   = "77777777-7777-7777-7777-777777777777"
	testOtherPet = "88888888-8888-8888-8888-888888888888"

	ownedPetsQuery      = `SELECT id, name, species, deleted_at IS NOT NULL\s+FROM pet\s+WHERE id = ANY\(\$1\) AND user_id = \$2`
	eventByIDQuery      = `SELECT e.id, e.user_id, e.date_time, e.type, e.notes, e.value\s+FROM event e\s+WHERE e.id = \$1 AND e.deleted_at IS NULL`
	eventForUserQuery   = `SELECT e.id, e.user_id, e.date_time, e.type, e.notes, e.value\s+FROM event e\s+WHERE e.id = \$1 AND e.user_id = \$2 AND e.deleted_at IS NULL`
	eventIdempotencySQL = `SELECT e.id, e.user_id, e.date_time, e.type, e.notes, e.value\s+FROM event e\s+WHERE e.user_id = \$1 AND e.idempotency_key = \$2`
	eventPetsSQL        = `SELECT link\.event_id, pet\.id, pet\.name, pet\.species\s+FROM event_pet link`
	filesByOwnerSQL     = `SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = \$2 AND confirmed_at IS NOT NULL\s+ORDER BY position ASC`
)

var eventColumnNames = []string{"id", "user_id", "date_time", "type", "notes", "value"}

// testPet — питомец в ожиданиях тестов; Deleted — мягко удалён.
type testPet struct {
	ID      string
	Name    string
	Species string
	Deleted bool
}

var (
	petRex   = testPet{ID: testPetID, Name: "Rex", Species: "DOG"}
	petTom   = testPet{ID: testPetID2, Name: "Tom", Species: "CAT"}
	petNemo  = testPet{ID: testPetID3, Name: "Nemo", Species: "FISH"}
	allPets3 = []testPet{petRex, petTom, petNemo}
)

func petIDsOfTest(pets ...testPet) []string {
	ids := make([]string, len(pets))
	for i, p := range pets {
		ids[i] = p.ID
	}
	return ids
}

// expectOwnedPets мокирует загрузку питомцев пользователя по pet_ids:
// возвращаются перечисленные питомцы (отсутствующий в списке питомец —
// не найден или чужой).
func expectOwnedPets(mock sqlmock.Sqlmock, pets ...testPet) {
	rows := sqlmock.NewRows([]string{"id", "name", "species", "deleted"})
	for _, p := range pets {
		rows.AddRow(p.ID, p.Name, p.Species, p.Deleted)
	}
	mock.ExpectQuery(ownedPetsQuery).WillReturnRows(rows)
}

// expectEventPets мокирует чтение видимых питомцев события: мягко удалённые
// не возвращаются.
func expectEventPets(mock sqlmock.Sqlmock, eventID string, pets ...testPet) {
	rows := sqlmock.NewRows([]string{"event_id", "id", "name", "species"})
	for _, p := range pets {
		if !p.Deleted {
			rows.AddRow(eventID, p.ID, p.Name, p.Species)
		}
	}
	mock.ExpectQuery(eventPetsSQL).WillReturnRows(rows)
}

func eventRow(eventID, eventType, value string, date time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(eventColumnNames).
		AddRow(eventID, testUserID, date, eventType, nil, []byte(value))
}

// expectEventForUser мокирует чтение события владельца и его видимых
// питомцев (два запроса). lock — запрос с блокировкой строки внутри
// транзакции.
func expectEventForUser(mock sqlmock.Sqlmock, eventID, eventType, value string, pets ...testPet) {
	mock.ExpectQuery(eventForUserQuery).
		WillReturnRows(eventRow(eventID, eventType, value, time.Now().Add(-time.Hour)))
	expectEventPets(mock, eventID, pets...)
}

// expectEventFilesEmpty мокирует чтение файлов события без файлов.
func expectEventFilesEmpty(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(filesByOwnerSQL).WillReturnRows(sqlmock.NewRows(fileRowColumns))
}

// expectEventCreated мокирует вставку события в транзакции и чтение
// созданного события с его питомцами и файлами.
func expectEventCreated(mock sqlmock.Sqlmock, eventID, eventType, value string, pets ...testPet) {
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO event \(`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(eventID))
	mock.ExpectExec(`INSERT INTO event_pet`).WillReturnResult(sqlmock.NewResult(0, int64(len(pets))))
	mock.ExpectCommit()
	mock.ExpectQuery(eventByIDQuery).
		WillReturnRows(eventRow(eventID, eventType, value, time.Now()))
	expectEventPets(mock, eventID, pets...)
	expectEventFilesEmpty(mock)
}

func petRefsOf(pets ...testPet) []models.EventPetRef {
	refs := make([]models.EventPetRef, 0, len(pets))
	for _, p := range pets {
		refs = append(refs, models.EventPetRef{PetID: p.ID, PetName: p.Name})
	}
	return refs
}
