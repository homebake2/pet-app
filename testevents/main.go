package main

import (
	"fmt"
	"time"
)

func main() {
	// Создаём map с пустыми значениями
	eventsByDay := make(map[string][]string)

	// Дата, для которой нет событий
	testDate := time.Now().Format("2006-01-02")

	// Получаем значение по ключу
	events := eventsByDay[testDate]

	// Проверяем, что это не nil
	if events == nil {
		fmt.Println("events is nil")
	} else {
		fmt.Printf("events is empty slice: %v, length: %d, capacity: %d\n", events, len(events), cap(events))
	}

	// Добавляем событие
	eventsByDay[testDate] = append(eventsByDay[testDate], "event1")

	// Проверяем снова
	events = eventsByDay[testDate]
	fmt.Printf("events after adding: %v, length: %d, capacity: %d\n", events, len(events), cap(events))
}
