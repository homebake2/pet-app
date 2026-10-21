package handlers

import (
	"database/sql"
	"errors"
	"myauthservice/database"
	"myauthservice/utils"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// assertError is a generic sentinel used to simulate unexpected DB failures in tests.
var assertError = errors.New("db failure")

// setupMockDB swaps database.DB for a sqlmock instance for the duration of the test.
func setupMockDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	original := database.DB
	database.DB = db
	t.Cleanup(func() {
		database.DB = original
		db.Close()
	})
	return mock
}

func validAccessToken(t *testing.T, userID string) string {
	t.Helper()
	token, err := utils.GenerateToken("login", userID, "access", time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	return token
}

func validRefreshToken(t *testing.T, login, userID string) string {
	t.Helper()
	token, err := utils.GenerateToken(login, userID, "refresh", time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	return token
}

// expectTokensValid mocks the tokens_invalidated_at lookup that requireUserID
// performs for every authenticated request, reporting the user's tokens as
// never revoked. Call it first, before any handler-specific expectations.
func expectTokensValid(mock sqlmock.Sqlmock, userID string) {
	mock.ExpectQuery(`SELECT tokens_invalidated_at FROM users WHERE id=\$1`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"tokens_invalidated_at"}).AddRow(nil))
}

// expectEndUserPetAccess мокирует завершение доступов при удалении аккаунта
// (database.endUserPetAccessWith): уведомления участникам, отмена приглашений,
// удаление чужих записей про питомцев пользователя и передача владельцу его
// записей про чужих питомцев; записей для передачи нет.
func expectEndUserPetAccess(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`INSERT INTO pet_access_notice`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE pet_access_invite SET status = 'cancelled'`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM event WHERE id IN`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM reminder_plan WHERE id IN`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`WITH reassigned AS`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`UPDATE reminder_plan rp SET user_id`).WillReturnRows(sqlmock.NewRows([]string{"id", "owner_id"}))
}

// expectPetRole мокирует определение роли пользователя у питомца
// (database.GetPetRole).
func expectPetRole(mock sqlmock.Sqlmock, role string) {
	mock.ExpectQuery(`SELECT CASE WHEN p\.user_id = \$2 THEN 'owner' ELSE pa\.role END\s+FROM pet p\s+LEFT JOIN pet_access pa`).
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow(role))
}

// expectPetRoleMissing мокирует отсутствие доступа к питомцу.
func expectPetRoleMissing(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT CASE WHEN p\.user_id = \$2 THEN 'owner' ELSE pa\.role END\s+FROM pet p\s+LEFT JOIN pet_access pa`).
		WillReturnError(sql.ErrNoRows)
}

// expectNoSharedPets мокирует определение автора записи (database.ResolveAuthors)
// для питомцев без участников: author в ответе равен null.
func expectNoSharedPets(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT DISTINCT pet_id FROM pet_access WHERE pet_id = ANY`).
		WillReturnRows(sqlmock.NewRows([]string{"pet_id"}))
}

var accessNoticeColumns = []string{"pet_id", "pet_name", "pet_species", "reason", "created_at"}

// expectEndPetAccessOnDelete мокирует завершение доступов при мягком удалении
// питомца: уведомления участникам и отмена приглашений без действующих питомцев.
func expectEndPetAccessOnDelete(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`INSERT INTO pet_access_notice`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE pet_access_invite i SET status = 'cancelled'`).WillReturnResult(sqlmock.NewResult(0, 0))
}

// expectEndPetAccessOnTransfer мокирует завершение доступов при принятии
// передачи: уведомления участникам, удаление строк доступа, отмена приглашений.
func expectEndPetAccessOnTransfer(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`INSERT INTO pet_access_notice`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM pet_access WHERE pet_id = \$1`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE pet_access_invite i SET status = 'cancelled'`).WillReturnResult(sqlmock.NewResult(0, 0))
}

// expectVetPassportAuthors мокирует определение автора записей Ветпаспорта
// (handlers.vetPassportAuthors) для питомцев без участников: автор записи —
// testUserID, author в ответе равен null.
func expectVetPassportAuthors(mock sqlmock.Sqlmock, ids ...string) {
	rows := sqlmock.NewRows([]string{"id", "author_id"})
	for _, id := range ids {
		rows.AddRow(id, testUserID)
	}
	mock.ExpectQuery(`SELECT t\.id, COALESCE\(t\.user_id, p\.user_id\)::text`).WillReturnRows(rows)
	expectNoSharedPets(mock)
}

// expectOwnerNames мокирует пакетную загрузку имён владельцев питомцев списка
// (database.GetOwnerNames).
func expectOwnerNames(mock sqlmock.Sqlmock, names map[string]string) {
	rows := sqlmock.NewRows([]string{"id", "name"})
	for id, name := range names {
		rows.AddRow(id, name)
	}
	mock.ExpectQuery(`SELECT ou\.id::text, COALESCE\(NULLIF\(trim\(concat_ws`).WillReturnRows(rows)
}
