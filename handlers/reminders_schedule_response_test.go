package handlers

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"myauthservice/models"
)

func TestPlanScheduleOptionals(t *testing.T) {
	weekdays, interval, end := planScheduleOptionals(models.ReminderPlanDB{})
	assert.Nil(t, weekdays)
	assert.Nil(t, interval)
	assert.Nil(t, end)

	weekdays, interval, end = planScheduleOptionals(models.ReminderPlanDB{
		Weekdays:     []int{1, 3},
		IntervalDays: sql.NullInt64{Int64: 2, Valid: true},
		EndDate:      sql.NullTime{Time: time.Date(2030, 3, 4, 0, 0, 0, 0, time.UTC), Valid: true},
	})
	assert.Equal(t, []int{1, 3}, *weekdays)
	assert.Equal(t, 2, *interval)
	assert.Equal(t, "2030-03-04", *end)
}
