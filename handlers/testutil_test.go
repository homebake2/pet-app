package handlers

import (
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
