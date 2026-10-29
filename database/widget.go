package database

import (
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// WidgetUpcomingReminder — ближайшее незавершённое напоминание питомца для
// виджета: тип настроек и момент напоминания.
type WidgetUpcomingReminder struct {
	Type     string
	RemindAt time.Time
}

// GetUpcomingRemindersByPetIDs возвращает для каждого питомца из petIDs до
// limit ближайших незавершённых напоминаний с remind_at >= now по настройкам,
// видимым userID и привязанным к питомцу (в том числе общим с другими
// питомцами), — в порядке remind_at, затем id; то же, что
// GetUpcomingRemindersByPetID по каждому питомцу, но одним запросом. Питомцы
// без напоминаний в результате отсутствуют.
func GetUpcomingRemindersByPetIDs(userID string, petIDs []uuid.UUID, now time.Time, limit int) (map[uuid.UUID][]WidgetUpcomingReminder, error) {
	result := make(map[uuid.UUID][]WidgetUpcomingReminder, len(petIDs))
	if len(petIDs) == 0 {
		return result, nil
	}

	rows, err := DB.Query(`
	SELECT pets.pet_id, upcoming.type, upcoming.remind_at
	FROM unnest($2::uuid[]) WITH ORDINALITY AS pets(pet_id, ord)
	CROSS JOIN LATERAL (
		SELECT p.type, r.remind_at, r.id
		FROM reminder_plan_pet own_link
		JOIN reminder_plan p ON p.id = own_link.plan_id
		JOIN reminder r ON r.plan_id = p.id
		WHERE own_link.pet_id = pets.pet_id
		AND r.closed_at IS NULL
		AND r.remind_at >= $3
		AND `+reminderPlanReadableClause("$1")+`
		ORDER BY r.remind_at, r.id
		LIMIT $4
	) upcoming
	ORDER BY pets.ord, upcoming.remind_at, upcoming.id`, userID, pq.Array(petIDs), now.UTC(), limit)
	if err != nil {
		log.Println("GetUpcomingRemindersByPetIDs error:", err)
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var petID uuid.UUID
		var item WidgetUpcomingReminder
		if err := rows.Scan(&petID, &item.Type, &item.RemindAt); err != nil {
			return nil, err
		}
		result[petID] = append(result[petID], item)
	}
	return result, rows.Err()
}

// CountEventsByPetsGroupedByType возвращает для каждого питомца из petIDs
// число его не удалённых событий, видимых userID, каждого типа с date_time в
// полуоткрытом интервале [start, end), — petID → type → count. Событие, общее с другими
// питомцами, считается для каждого из них. Питомцы и типы без событий
// отсутствуют в результате.
func CountEventsByPetsGroupedByType(userID string, petIDs []uuid.UUID, start, end time.Time) (map[uuid.UUID]map[string]int, error) {
	result := make(map[uuid.UUID]map[string]int, len(petIDs))
	if len(petIDs) == 0 {
		return result, nil
	}
	ids := make([]string, len(petIDs))
	for i, id := range petIDs {
		ids[i] = id.String()
	}

	rows, err := DB.Query(`
	SELECT ep.pet_id, e.type, COUNT(*)
	FROM event e
	JOIN event_pet ep ON ep.event_id = e.id
	WHERE ep.pet_id = ANY($2::uuid[])
	AND e.deleted_at IS NULL
	AND e.date_time >= $3
	AND e.date_time < $4
	AND `+eventPetLinks.readableClause("e", "id", "$1")+`
	GROUP BY ep.pet_id, e.type`, userID, pq.Array(ids), start.UTC(), end.UTC())
	if err != nil {
		log.Println("CountEventsByPetsGroupedByType error:", err)
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var petID uuid.UUID
		var eventType string
		var count int
		if err := rows.Scan(&petID, &eventType, &count); err != nil {
			return nil, err
		}
		if result[petID] == nil {
			result[petID] = make(map[string]int)
		}
		result[petID][eventType] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
