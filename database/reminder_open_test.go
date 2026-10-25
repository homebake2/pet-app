package database

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOpenReminderRemindAtWith(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	planID, reminderID := uuid.New(), uuid.New()
	at := time.Date(2030, 1, 1, 8, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT remind_at FROM reminder WHERE id = \$1 AND plan_id = \$2 AND closed_at IS NULL`).
		WithArgs(reminderID, planID).
		WillReturnRows(sqlmock.NewRows([]string{"remind_at"}).AddRow(at))
	got, found, err := GetOpenReminderRemindAtWith(db, planID, reminderID)
	require.NoError(t, err)
	assert.True(t, found)
	assert.True(t, got.Equal(at))

	// Закрытое, удалённое или чужое напоминание — строк нет, это не ошибка.
	mock.ExpectQuery(`SELECT remind_at FROM reminder`).
		WithArgs(reminderID, planID).
		WillReturnRows(sqlmock.NewRows([]string{"remind_at"}))
	_, found, err = GetOpenReminderRemindAtWith(db, planID, reminderID)
	require.NoError(t, err)
	assert.False(t, found)

	assert.NoError(t, mock.ExpectationsWereMet())
}
