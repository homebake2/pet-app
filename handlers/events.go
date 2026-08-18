package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/utils"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// GetActivitiesHandler handles GET requests to /activities endpoint
func GetActivitiesHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Обработчик /activities GET вызван")

	// Проверка метода запроса
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Method not allowed",
			Code:  "MethodNotAllowed",
		})
		return
	}

	// Проверка и извлечение query параметров
	fromStr := r.URL.Query().Get("from")
	if fromStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Отсутствует обязательный параметр from",
			Code:  "BadRequest",
		})
		return
	}

	toStr := r.URL.Query().Get("to")
	if toStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Отсутствует обязательный параметр to",
			Code:  "BadRequest",
		})
		return
	}

	petIDStr := r.URL.Query().Get("pet_id")
	if petIDStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Отсутствует обязательный параметр pet_id",
			Code:  "BadRequest",
		})
		return
	}

	// Парсинг дат
	fromDate, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный формат даты from (ожидается YYYY-MM-DD)",
			Code:  "BadRequest",
		})
		return
	}

	toDate, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный формат даты to (ожидается YYYY-MM-DD)",
			Code:  "BadRequest",
		})
		return
	}

	// Парсинг pet_id
	petID, err := uuid.Parse(petIDStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный формат pet_id",
			Code:  "BadRequest",
		})
		return
	}

	// Проверка авторизации
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Отсутствует токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	tokenParts := strings.Split(authHeader, "Bearer ")
	if len(tokenParts) != 2 {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	tokenStr := tokenParts[1]

	// Парсим токен через utils
	claims, err := utils.ParseToken(tokenStr)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Токен истек",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// Достаём user_id
	userIDRaw, ok := claims["id"]
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	userID, ok := userIDRaw.(string)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// Получаем profile_id
	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения профиля пользователя",
			Code:  "InternalError",
		})
		return
	}

	// Проверка принадлежности питомца пользователю
	belongs, err := database.CheckPetBelongsToProfile(petID, profileID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка проверки принадлежности питомца",
			Code:  "InternalError",
		})
		return
	}

	if !belongs {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "У данного пользователя нет питомца с id " + petIDStr,
			Code:  "Недостаточно прав",
		})
		return
	}

	// Получаем имя питомца
	petName, err := database.GetPetNameByID(petID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения имени питомца",
			Code:  "InternalError",
		})
		return
	}

	// Получаем события за период
	eventsDB, err := database.GetEventsByPetIDAndDateRange(petID, fromDate, toDate)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения событий",
			Code:  "InternalError",
		})
		return
	}

	// Группируем события по дням
	eventsByDay := make(map[string][]models.ActivityEvent)
	for _, eventDB := range eventsDB {
		dateStr := eventDB.Date.Format("2006-01-02")
		var notes *string
		if eventDB.Notes.Valid {
			notes = new(string)
			*notes = eventDB.Notes.String
		}
		activityEvent := models.ActivityEvent{
			ID:    eventDB.ID.String(),
			Date:  eventDB.Date.Format(time.RFC3339),
			Type:  eventDB.Type,
			Notes: notes,
			Value: eventDB.Value,
		}
		eventsByDay[dateStr] = append(eventsByDay[dateStr], activityEvent)
	}

	// Создаём список всех дней в периоде
	var items []models.ActivityDay
	currentDate := fromDate
	for currentDate.Before(toDate.AddDate(0, 0, 1)) {
		dateStr := currentDate.Format("2006-01-02")
		events, exists := eventsByDay[dateStr]
		if !exists {
			events = []models.ActivityEvent{} // Empty slice instead of nil
		}
		day := models.ActivityDay{
			Date:   dateStr,
			Events: events,
		}
		items = append(items, day)
		currentDate = currentDate.AddDate(0, 0, 1)
	}

	// Формируем ответ
	response := models.ActivitiesResponse{
		PetName: petName,
		Items:   items,
	}

	// Отправляем ответ
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func EventIDResonseHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		GetEventHandler(w, r)
	case http.MethodPut:
		UpdateEventHandler(w, r)
	case http.MethodDelete:
		DeleteEventHandler(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Method not allowed",
			Code:  "MethodNotAllowed",
		})
	}
}

// DELETE /events/{id}
func DeleteEventHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Обработчик /events/{id} DELETE вызван")
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Получаем Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Отсутствует токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	tokenParts := strings.Split(authHeader, "Bearer ")
	if len(tokenParts) != 2 {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	tokenStr := tokenParts[1]

	// 2. Парсим токен через utils
	claims, err := utils.ParseToken(tokenStr)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Токен истек",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// 3. Достаём user_id
	userIDRaw, ok := claims["id"]
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	userID, ok := userIDRaw.(string)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// 4. Получаем profile_id
	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения профиля",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	// 5. Получаем eventID из URL
	pathSegments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	var eventIDStr string
	if len(pathSegments) >= 2 && pathSegments[0] == "events" {
		eventIDStr = pathSegments[1]
	} else {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный путь",
			Code:  "BAD_REQUEST",
		})
		return
	}

	eventID, err := uuid.Parse(eventIDStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный ID события",
			Code:  "BAD_REQUEST",
		})
		return
	}

	// 6. Получаем информацию о событии и проверяем, принадлежит ли оно питомцу пользователя
	_, petID, _, err := database.GetEventByID(eventID)
	if err != nil {
		if err == sql.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Событие не найдено",
				Code:  "NOT_FOUND",
			})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения события",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	// 7. Проверяем, принадлежит ли питомец пользователю
	belongs, err := database.CheckPetBelongsToProfile(petID, profileID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка проверки принадлежности питомца",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	if !belongs {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "У данного пользователя нет питомца " + petID.String(),
			Code:  "Недостаточно прав",
		})
		return
	}

	// 8. Удаляем событие
	err = database.DeleteEvent(eventID)
	if err != nil {
		if err == sql.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Событие не найдено",
				Code:  "NOT_FOUND",
			})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка удаления события",
			Code:  "INTERNAL_ERROR",
		})
		return
	}

	// 9. Возвращаем успешный ответ
	w.WriteHeader(http.StatusNoContent)
}

// GET /event/{id}
func GetEventHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Обработчик /events/{id} вызван")
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Получаем Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Отсутствует токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	tokenParts := strings.Split(authHeader, "Bearer ")
	if len(tokenParts) != 2 {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	tokenStr := tokenParts[1]

	// 2. Парсим токен через utils
	claims, err := utils.ParseToken(tokenStr)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Токен истек",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// 3. Достаём user_id
	userIDRaw, ok := claims["id"]
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	userID, ok := userIDRaw.(string)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// 4. Получаем profile_id
	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения профиля",
			Code:  "DB_ERROR",
		})
		return
	}

	// 5. Получаем event_id из URL
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 || pathParts[1] != "events" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный путь",
			Code:  "BAD_REQUEST",
		})
		return
	}

	eventIDStr := pathParts[2]
	eventID, err := uuid.Parse(eventIDStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный ID события",
			Code:  "BAD_REQUEST",
		})
		return
	}

	// 6. Получаем событие из БД
	eventDB, petID, petName, err := database.GetEventByID(eventID)
	if err != nil {
		if err == sql.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Событие не найдено",
				Code:  "NOT_FOUND",
			})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Ошибка получения события",
				Code:  "DB_ERROR",
			})
		}
		return
	}

	// 7. Проверка принадлежности питомца пользователю
	belongs, err := database.CheckPetBelongsToProfile(petID, profileID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка проверки прав доступа",
			Code:  "DB_ERROR",
		})
		return
	}

	if !belongs {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "У данного пользователя нет питомца " + petID.String(),
			Code:  "Недостаточно прав",
		})
		return
	}

	// 8. Формируем ответ
	var notes *string
	if eventDB.Notes.Valid {
		notes = &eventDB.Notes.String
	}

	response := models.EventResponse{
		ID:      eventDB.ID.String(),
		Date:    eventDB.Date.Format(time.RFC3339),
		Value:   eventDB.Value,
		Notes:   notes,
		PetID:   petID.String(),
		PetName: petName,
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// POST /events
func CreateEventHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Обработчик /events вызван")
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 2. Получаем Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Отсутствует токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	tokenParts := strings.Split(authHeader, "Bearer ")
	if len(tokenParts) != 2 {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	tokenStr := tokenParts[1]

	// 3. Парсим токен через utils
	claims, err := utils.ParseToken(tokenStr)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Токен истек",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// 4. Достаём user_id
	userIDRaw, ok := claims["id"]
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	userID, ok := userIDRaw.(string)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// 5. Получаем profile_id
	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения профиля",
			Code:  "DB_ERROR",
		})
		return
	}

	// Парсинг тела запроса
	var req models.CreateEventRequest
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректное тело запроса",
			Code:  "Некорректный запрос",
		})
		return
	}

	// Проверка обязательных полей
	if req.ID == "" || req.Date == "" || req.Type == "" || req.Value == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Обязательные поля не заполнены",
			Code:  "Некорректный запрос",
		})
		return
	}

	// Проверка типа ивента
	if !models.IsValidEventType(req.Type) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректное значение type",
			Code:  "Некорректный тип события",
		})
		return
	}

	// Проверка, что питомец принадлежит пользователю и не удален
	petID, err := uuid.Parse(req.ID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный ID питомца",
			Code:  "Некорректный ID",
		})
		return
	}

	petDB, err := database.GetPetIdDBByIDAndProfileID(petID, profileID)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "У данного пользователя нет питомца " + req.ID,
			Code:  "Недостаточно прав",
		})
		return
	}

	// Проверка, что питомец не удален
	if petDB.DeletedAt.Valid {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невозможно создать запись о событии, так как питомец удален",
			Code:  "Питомец удален",
		})
		return
	}

	// Создание ивента
	eventID, err := database.InsertEvent(petID, req)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка при создании ивента",
			Code:  "Внутренняя ошибка",
		})
		return
	}

	// Ответ с ID созданного ивента
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(models.EventResponse{
		ID: eventID.String(),
	})
}

// PUT /events/{id}
func UpdateEventHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Обработчик /events/{id} PUT вызван")
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Получаем Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Отсутствует токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	tokenParts := strings.Split(authHeader, "Bearer ")
	if len(tokenParts) != 2 {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	tokenStr := tokenParts[1]

	// 2. Парсим токен через utils
	claims, err := utils.ParseToken(tokenStr)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Токен истек",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// 3. Достаём user_id
	userIDRaw, ok := claims["id"]
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	userID, ok := userIDRaw.(string)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// 4. Получаем profile_id
	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения профиля",
			Code:  "DB_ERROR",
		})
		return
	}

	// 5. Получаем event_id из URL
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 || pathParts[1] != "events" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный путь",
			Code:  "BAD_REQUEST",
		})
		return
	}

	eventIDStr := pathParts[2]
	eventID, err := uuid.Parse(eventIDStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный ID события",
			Code:  "BAD_REQUEST",
		})
		return
	}

	// 6. Проверяем существование события
	eventDB, err := database.GetEventByIDForUpdate(eventID)
	if err != nil {
		if err == sql.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Данное мероприятие не существует или не найдено",
				Code:  "NOT_FOUND",
			})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Ошибка получения события",
				Code:  "DB_ERROR",
			})
		}
		return
	}

	// 7. Парсинг тела запроса
	var req models.UpdateEventRequest
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректное тело запроса",
			Code:  "Некорректный запрос",
		})
		return
	}

	// 8. Проверка обязательного поля pet_id
	if req.PetID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Обязательное поле pet_id не заполнено",
			Code:  "Некорректный запрос",
		})
		return
	}

	// 9. Проверка, что хотя бы одно из полей для обновления передано
	if req.Date == nil && req.Type == nil && req.Notes == nil && req.Value == nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Необходимо указать хотя бы одно поле для обновления",
			Code:  "Некорректный запрос",
		})
		return
	}

	// 10. Проверка соответствия pet_id из запроса и из события
	reqPetID, err := uuid.Parse(req.PetID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный ID питомца",
			Code:  "Некорректный ID",
		})
		return
	}

	if eventDB.PetID != reqPetID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "У данного пользователя нет питомца " + req.PetID,
			Code:  "Недостаточно прав",
		})
		return
	}

	// 11. Проверка принадлежности питомца пользователю
	belongs, err := database.CheckPetBelongsToProfile(reqPetID, profileID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка проверки прав доступа",
			Code:  "DB_ERROR",
		})
		return
	}

	if !belongs {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "У данного пользователя нет питомца " + req.PetID,
			Code:  "Недостаточно прав",
		})
		return
	}

	// 12. Проверка, что питомец не удален
	petDB, err := database.GetPetById(reqPetID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения питомца",
			Code:  "DB_ERROR",
		})
		return
	}

	if petDB.DeletedAt.Valid {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невозможно редактировать запись о событии, так как питомец удален",
			Code:  "Питомец удален",
		})
		return
	}

	// 13. Проверка типа события (если передан)
	if req.Type != nil {
		if !models.IsValidEventType(*req.Type) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Некорректное значение type",
				Code:  "Некорректный тип события",
			})
			return
		}
	}

	// 14. Парсинг даты (если передана)
	var dateTime *time.Time
	if req.Date != nil {
		parsedDate, err := time.Parse(time.RFC3339, *req.Date)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Некорректный формат даты",
				Code:  "Некорректный запрос",
			})
			return
		}
		dateTime = &parsedDate
	}

	// 15. Обновление события в БД
	err = database.UpdateEvent(eventID, req, dateTime, req.Type, req.Notes, req.Value)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка при обновлении ивента",
			Code:  "Внутренняя ошибка",
		})
		return
	}

	// 16. Ответ с успешным статусом
	w.WriteHeader(http.StatusOK)
}
