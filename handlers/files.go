// Package handlers: generic-механизм файлов сущностей (см.
// artifacts/PET/pages/integrations/obschie-trebovaniya-fayly-sushchnostei.md).
// Три endpoint'а (upload-url/complete/delete) параметризованы owner_type и
// работают через статический «реестр типов владельцев» (ownerTypeRegistry) —
// добавление нового owner_type означает добавление записи в реестр, а не
// ветки if/switch здесь.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Storage описывает возможности S3-совместимого хранилища, нужные
// generic-механизму файлов сущностей: presigned PUT/GET URL и best-effort
// удаление объекта. Реализация — *s3client.Client, устанавливаемая в
// main.go через SetStorage; в тестах подменяется фейком, чтобы не делать
// реальных сетевых обращений.
type Storage interface {
	PresignPutURL(ctx context.Context, objectKey, contentType string) (url string, expiresIn int, err error)
	PresignGetURL(ctx context.Context, objectKey string) (url string, err error)
	DeleteObject(ctx context.Context, objectKey string) error
}

// storage — активная реализация Storage, устанавливается один раз при
// старте (см. SetStorage), аналогично database.DB.
var storage Storage

// SetStorage устанавливает реализацию Storage, используемую generic-хендлерами
// файлов. Вызывается один раз из main.go после чтения конфигурации S3 из
// переменных окружения.
func SetStorage(s Storage) {
	storage = s
}

// ownerCardinality — режим кардинальности файлов владельца (см. «Общие
// требования: Файлы сущностей», раздел «Реестр типов владельцев»).
type ownerCardinality int

const (
	// cardinalityExactlyOne — у владельца не может быть больше одного файла;
	// подтверждение новой загрузки заменяет предыдущую (см. pet_photo).
	cardinalityExactlyOne ownerCardinality = iota
	// cardinalityUpToN — у владельца может быть до maxCount подтверждённых
	// файлов; подтверждение сверх лимита даёт 409, а не тихую замену (см.
	// event_file, «Файлы события — Backend»).
	cardinalityUpToN
)

// eventFileOwnerType — owner_type, под которым файлы события зарегистрированы
// в реестре типов владельцев (см. «Файлы события — Backend»).
const eventFileOwnerType = "event_file"

// eventFileMaxCount — лимит кардинальности «до N» для event_file (см. «Файлы
// события — Backend», раздел «Назначение»).
const eventFileMaxCount = 10

// ownerTypeSpec — запись реестра типов владельцев: как проверить владение,
// какая кардинальность и какие content-type допустимы для owner_type.
type ownerTypeSpec struct {
	cardinality         ownerCardinality
	allowedContentTypes map[string]bool
	// maxCount — лимит кардинальности «до N»; не используется (игнорируется)
	// для cardinalityExactlyOne.
	maxCount int
	// checkOwnership возвращает true, если ownerID существует и принадлежит
	// userID (для pet_photo — ещё и не удалён). Ошибка — сбой БД (500);
	// false без ошибки — единый 404 (см. «Общие требования: IDOR и владение
	// ресурсами»), без утечки деталей о причине отказа.
	checkOwnership func(ownerID uuid.UUID, userID string) (bool, error)
}

// ownerTypeRegistry — статическая таблица «owner_type → { проверка владения,
// кардинальность, допустимые content-type }», см. «Общие требования: Файлы
// сущностей», раздел «Реестр типов владельцев». pet_photo — первое
// подключение, кардинальность «ровно 1» (см. «Фотография питомца —
// Backend»); event_file — второе, кардинальность «до 10» (см. «Файлы
// события — Backend»). Будущие owner_type добавляются сюда отдельной
// работой, сами хендлеры ниже не меняются.
var ownerTypeRegistry = map[string]ownerTypeSpec{
	"pet_photo": {
		cardinality: cardinalityExactlyOne,
		allowedContentTypes: map[string]bool{
			"image/jpeg": true,
			"image/png":  true,
			"image/webp": true,
		},
		checkOwnership: database.CheckPetOwnership,
	},
	eventFileOwnerType: {
		cardinality: cardinalityUpToN,
		maxCount:    eventFileMaxCount,
		allowedContentTypes: map[string]bool{
			"image/jpeg":      true,
			"image/png":       true,
			"image/webp":      true,
			"application/pdf": true,
		},
		checkOwnership: database.CheckEventFileOwnership,
	},
}

// FilesByIDHandler маршрутизирует /files/{file_id} и /files/{file_id}/complete.
// Точный путь /files/upload-url зарегистрирован отдельно в NewMux и выигрывает
// у этого поддерева — та же идиома, что и /events/stats против /events/.
func FilesByIDHandler(w http.ResponseWriter, r *http.Request) {
	segments := pathSegments(r, "/files/")

	if len(segments) == 2 && segments[1] == "complete" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
			return
		}
		FilesCompleteHandler(w, r, segments[0])
		return
	}

	if len(segments) == 1 {
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
			return
		}
		FilesDeleteHandler(w, r, segments[0])
		return
	}

	writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Не найдено")
}

// FilesUploadUrlHandler обрабатывает POST /files/upload-url — шаг 1
// «Основного сценария: загрузка/замена файла».
func FilesUploadUrlHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	var req models.PostFilesUploadUrlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	if req.OwnerType == "" || req.OwnerID == "" || req.ContentType == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поля owner_type, owner_id и content_type обязательны")
		return
	}

	if req.Filename != nil && len(*req.Filename) > models.PostFilesUploadUrlRequestFilenameMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле filename не должно превышать 255 символов")
		return
	}

	ownerID, err := uuid.Parse(req.OwnerID)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный owner_id")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	// (b) owner_type отсутствует в реестре — 400 (ошибка клиента, а не
	// вопрос владения ресурсом).
	spec, known := ownerTypeRegistry[req.OwnerType]
	if !known {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Неизвестный owner_type")
		return
	}

	// (c) проверка владения — единый 404, если ресурс не существует, чужой
	// или мягко удалён.
	owns, err := spec.checkOwnership(ownerID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки владения")
		return
	}
	if !owns {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Ресурс не найден")
		return
	}

	// (d) content_type проверяется после владения — намеренно тот же порядок,
	// что в требованиях.
	if !spec.allowedContentTypes[req.ContentType] {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Недопустимый content_type для данного owner_type")
		return
	}

	if storage == nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Хранилище файлов не сконфигурировано")
		return
	}

	// (e) file.id, object_key, неподтверждённая строка file.
	fileID := uuid.New()
	objectKey := req.OwnerType + "/" + ownerID.String() + "/" + fileID.String()

	if err := database.InsertUnconfirmedFile(fileID, req.OwnerType, ownerID, userID, objectKey, req.ContentType, req.Filename); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать запись файла")
		return
	}

	// (f) presigned PUT URL.
	uploadURL, expiresIn, err := storage.PresignPutURL(r.Context(), objectKey, req.ContentType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось подписать ссылку на загрузку")
		return
	}

	// (g)
	writeJSON(w, http.StatusOK, models.PostFilesUploadUrlResponse{
		FileID:      fileID.String(),
		UploadURL:   uploadURL,
		ContentType: req.ContentType,
		ExpiresIn:   expiresIn,
	})
}

// FilesCompleteHandler обрабатывает POST /files/{file_id}/complete —
// «Подтверждение загрузки».
func FilesCompleteHandler(w http.ResponseWriter, r *http.Request, fileIDStr string) {
	fileID, err := uuid.Parse(fileIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id файла")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	file, ok := resolveOwnedFile(w, fileID, userID)
	if !ok {
		return
	}

	spec := ownerTypeRegistry[file.OwnerType] // существование гарантировано upload-url

	var replacedObjectKeys []string
	switch spec.cardinality {
	case cardinalityExactlyOne:
		replacedObjectKeys, err = database.ConfirmFileExactlyOne(file.ID, file.OwnerType, file.OwnerID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось подтвердить загрузку файла")
			return
		}
	case cardinalityUpToN:
		limitReached, err := database.ConfirmFileUpToN(file.ID, file.OwnerType, file.OwnerID, spec.maxCount)
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось подтвердить загрузку файла")
			return
		}
		if limitReached {
			writeError(w, http.StatusConflict, openapi.CONFLICT, "Достигнут лимит количества файлов для этого владельца")
			return
		}
	default:
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Неподдерживаемая кардинальность owner_type")
		return
	}

	for _, objectKey := range replacedObjectKeys {
		bestEffortDeleteObject(r.Context(), objectKey)
	}

	w.WriteHeader(http.StatusNoContent)
}

// FilesDeleteHandler обрабатывает DELETE /files/{file_id} — «Удаление файла».
func FilesDeleteHandler(w http.ResponseWriter, r *http.Request, fileIDStr string) {
	fileID, err := uuid.Parse(fileIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id файла")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	file, ok := resolveOwnedFile(w, fileID, userID)
	if !ok {
		return
	}

	objectKey, err := database.DeleteFileByID(file.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			// Гонка: файл удалён между resolveOwnedFile и этим вызовом —
			// тот же 404, трактуется клиентом как «уже удалено».
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Файл не найден")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось удалить файл")
		return
	}

	bestEffortDeleteObject(r.Context(), objectKey)

	w.WriteHeader(http.StatusNoContent)
}

// resolveOwnedFile находит строку file по fileID и повторно проверяет
// владение по правилу из реестра для её owner_type/owner_id — обязательный
// шаг для complete/delete: между upload-url и этим вызовом могло пройти
// время (питомец мог быть удалён), а file_id в пути — единственный параметр
// запроса. При ошибке/отсутствии сама пишет 404/500.
func resolveOwnedFile(w http.ResponseWriter, fileID uuid.UUID, userID string) (*models.FileDB, bool) {
	file, err := database.GetFileByID(fileID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Файл не найден")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения файла")
		return nil, false
	}

	spec, known := ownerTypeRegistry[file.OwnerType]
	if !known {
		// Не должно происходить: строка file создаётся только через
		// upload-url, который сам сверяет owner_type с реестром. Трактуем
		// как внутреннюю ошибку данных, а не как 404/400 клиента.
		log.Printf("resolveOwnedFile: неизвестный owner_type %q для file %s", file.OwnerType, file.ID)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Внутренняя ошибка данных файла")
		return nil, false
	}

	owns, err := spec.checkOwnership(file.OwnerID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки владения")
		return nil, false
	}
	if !owns {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Файл не найден")
		return nil, false
	}

	return file, true
}

// bestEffortDeleteObject удаляет объект в S3, не проваливая запрос клиента
// при ошибке (см. «Удаление файла», шаг 4) — только логирует.
func bestEffortDeleteObject(ctx context.Context, objectKey string) {
	if storage == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := storage.DeleteObject(ctx, objectKey); err != nil {
		log.Printf("best-effort DeleteObject(%s) failed: %v", objectKey, err)
	}
}
