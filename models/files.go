package models

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// PostFilesUploadUrlRequestFilenameMaxLen — максимальная длина filename в
// теле POST /files/upload-url (см. «Общие требования: Файлы сущностей»).
const PostFilesUploadUrlRequestFilenameMaxLen = 255

// PostFilesUploadUrlRequest — тело запроса POST /files/upload-url.
type PostFilesUploadUrlRequest struct {
	OwnerType   string `json:"owner_type"`
	OwnerID     string `json:"owner_id"`
	ContentType string `json:"content_type"`
	// Filename — опциональное исходное имя файла (не более 255 символов),
	// сохраняется как есть и возвращается читающей стороной конкретного
	// owner_type, которому оно нужно (см. «Файлы события — Backend»).
	Filename *string `json:"filename,omitempty"`
}

// PostFilesUploadUrlResponse — тело ответа POST /files/upload-url.
type PostFilesUploadUrlResponse struct {
	FileID      string `json:"file_id"`
	UploadURL   string `json:"upload_url"`
	ContentType string `json:"content_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// FileDB — строка таблицы file (см. «Общие требования: Файлы сущностей»).
// ConfirmedAt.Valid == false означает незавершённую загрузку — такие строки
// не должны учитываться при чтении файлов сущности или подсчёте
// кардинальности, но должны быть находимы по id для complete/delete.
type FileDB struct {
	ID          uuid.UUID
	OwnerType   string
	OwnerID     uuid.UUID
	UserID      string
	ObjectKey   string
	ContentType string
	Filename    sql.NullString
	Position    sql.NullInt32
	ConfirmedAt sql.NullTime
	CreatedAt   time.Time
}

// EventFileItem — элемент поля `files` в EventResponse (см. «Файлы события
// — Backend», раздел «Чтение: files и files_count в ответах события»).
type EventFileItem struct {
	FileID string `json:"file_id"`
	// URL — presigned GET URL, временная ссылка (см. «Общие требования: S3-
	// хранилище файлов»).
	URL         string  `json:"url"`
	ContentType string  `json:"content_type"`
	Filename    *string `json:"filename"`
}
