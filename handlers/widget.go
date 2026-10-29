package handlers

import (
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"

	"myauthservice/database"
	"myauthservice/eventreg"
	"myauthservice/models"
	"myauthservice/openapi"
)

const (
	// widgetUpcomingRemindersLimit — сколько ближайших напоминаний питомца
	// нужно виджету (три — большому, одно — малому).
	widgetUpcomingRemindersLimit = 3
	// widgetQuickActionsLimit — максимум кнопок быстрого действия на питомца
	// (шесть — большому виджету, четыре — среднему; клиент берёт первые N).
	widgetQuickActionsLimit = 6
	// widgetFrequencyWindowDays — окно подсчёта частоты использования типов
	// события для сортировки кнопок.
	widgetFrequencyWindowDays = 365
)

// GetWidgetHandler обрабатывает GET /widget — данные виджета главного
// экрана в режиме network за один запрос (см. «Данные виджета — Backend»).
func GetWidgetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	loc, ok := parseTimeZoneParam(w, r)
	if !ok {
		return
	}

	pets, err := database.GetAccessiblePetsByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось получить питомцев")
		return
	}

	now := time.Now()
	today := now.In(loc)
	start, end := localDaysBounds(today.AddDate(0, 0, -widgetFrequencyWindowDays), today, loc)

	petIDs := make([]uuid.UUID, 0, len(pets))
	photoItems := make([]models.PetItem, 0, len(pets))
	for _, pet := range pets {
		petIDs = append(petIDs, pet.ID)
		photoItems = append(photoItems, models.PetItem{ID: pet.ID.String()})
	}
	// Фото, частота событий и ближайшие напоминания зависят только от списка
	// питомцев и читаются одновременно. Фото — единственный шаг, который
	// пишет в w (ответ об ошибке), поэтому проверяется первым.
	var (
		photosOK       bool
		frequency      map[uuid.UUID]map[string]int
		frequencyErr   error
		remindersByPet map[uuid.UUID][]database.WidgetUpcomingReminder
		remindersErr   error
	)
	runParallel(
		func() { photosOK = attachPetPhotos(w, r, petIDs, photoItems) },
		func() { frequency, frequencyErr = database.CountEventsByPetsGroupedByType(userID, petIDs, start, end) },
		func() {
			remindersByPet, remindersErr = database.GetUpcomingRemindersByPetIDs(userID, petIDs, now.UTC(), widgetUpcomingRemindersLimit)
		},
	)
	if !photosOK {
		return
	}
	if frequencyErr != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось получить события питомцев")
		return
	}
	if remindersErr != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения напоминаний")
		return
	}

	response := models.WidgetResponse{Items: make([]models.WidgetPet, 0, len(pets))}
	for i, pet := range pets {
		reminders := remindersByPet[pet.ID]
		upcoming := make([]models.WidgetUpcomingReminder, 0, len(reminders))
		for _, row := range reminders {
			upcoming = append(upcoming, models.WidgetUpcomingReminder{
				Type:     row.Type,
				RemindAt: row.RemindAt.UTC().Format(time.RFC3339),
			})
		}

		// Кнопки быстрого создания события предлагаются только там, где
		// создание доступно: при роли view их нет.
		quickActions := []models.WidgetQuickAction{}
		if database.CanWriteRole(pet.Access) {
			quickActions = widgetQuickActions(pet.Species, frequency[pet.ID])
		}

		response.Items = append(response.Items, models.WidgetPet{
			ID:                pet.ID.String(),
			Name:              pet.Name,
			Species:           pet.Species,
			PhotoURL:          photoItems[i].PhotoURL,
			UpcomingReminders: upcoming,
			QuickActions:      quickActions,
		})
	}

	writeJSON(w, http.StatusOK, response)
}

// widgetQuickActions выбирает кнопки быстрого действия питомца: кандидаты
// вида species (в порядке справочника), отсортированные по убыванию
// частоты frequencyByType (при равенстве порядок справочника сохраняется —
// сортировка устойчивая), не более widgetQuickActionsLimit.
func widgetQuickActions(species string, frequencyByType map[string]int) []models.WidgetQuickAction {
	candidates := eventreg.QuickActionCandidates(species)
	sort.SliceStable(candidates, func(i, j int) bool {
		return frequencyByType[candidates[i].Type] > frequencyByType[candidates[j].Type]
	})
	if len(candidates) > widgetQuickActionsLimit {
		candidates = candidates[:widgetQuickActionsLimit]
	}
	actions := make([]models.WidgetQuickAction, 0, len(candidates))
	for _, candidate := range candidates {
		actions = append(actions, models.WidgetQuickAction{Type: candidate.Type, Value: candidate.Value})
	}
	return actions
}
