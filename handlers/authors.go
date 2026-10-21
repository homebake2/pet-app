package handlers

import (
	"myauthservice/database"
	"myauthservice/models"

	"github.com/google/uuid"
)

// Поле author ответов (см. «Общие требования: Доступ к питомцу», раздел
// «Автор записи»): имя автора и признак «это я». Заполняется, только если
// хотя бы одним из видимых питомцев записи пользуются несколько человек.

// eventAuthors возвращает поле author для каждого из событий events в том же
// порядке.
func eventAuthors(viewerID string, events []database.EventFull) ([]*models.EventAuthor, error) {
	refs := make([]database.AuthorRef, len(events))
	for i, eventFull := range events {
		refs[i] = database.AuthorRef{AuthorID: eventFull.Event.UserID.String(), PetIDs: petIDsOf(eventFull.Pets)}
	}
	return database.ResolveAuthors(viewerID, refs)
}

// eventAuthor возвращает поле author одного события.
func eventAuthor(viewerID string, eventFull *database.EventFull) (*models.EventAuthor, error) {
	authors, err := eventAuthors(viewerID, []database.EventFull{*eventFull})
	if err != nil {
		return nil, err
	}
	return authors[0], nil
}

// planAuthor возвращает поле author настроек напоминания (и их напоминаний):
// автор — пользователь, создавший настройки.
func planAuthor(viewerID string, plan models.ReminderPlanDB, pets []database.LinkedPet) (*models.EventAuthor, error) {
	authors, err := database.ResolveAuthors(viewerID, []database.AuthorRef{
		{AuthorID: plan.UserID.String(), PetIDs: petIDsOf(pets)},
	})
	if err != nil {
		return nil, err
	}
	return authors[0], nil
}

// vetPassportAuthors возвращает поле author записей ids сущности Ветпаспорта
// питомца petID по id записи; автор записи — пользователь, создавший её.
func vetPassportAuthors(viewerID string, table database.VetPassportTable, petID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*models.EventAuthor, error) {
	result := make(map[uuid.UUID]*models.EventAuthor, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	authorIDs, err := database.GetVetPassportAuthorIDs(table, ids)
	if err != nil {
		return nil, err
	}
	refs := make([]database.AuthorRef, len(ids))
	for i, id := range ids {
		refs[i] = database.AuthorRef{AuthorID: authorIDs[id], PetIDs: []uuid.UUID{petID}}
	}
	authors, err := database.ResolveAuthors(viewerID, refs)
	if err != nil {
		return nil, err
	}
	for i, id := range ids {
		result[id] = authors[i]
	}
	return result, nil
}
