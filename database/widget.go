package database

import (
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

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
