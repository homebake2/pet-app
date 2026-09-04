package models

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// PostFilesUploadUrlRequest — тело запроса POST /files/upload-url.
type PostFilesUploadUrlRequest struct {
	OwnerType   string `json:"owner_type"`
	OwnerID     string `json:"owner_id"`
	ContentType string `json:"content_type"`
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
	Position    sql.NullInt32
	ConfirmedAt sql.NullTime
	CreatedAt   time.Time
}
