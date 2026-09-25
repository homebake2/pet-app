// Package s3client оборачивает генерацию presigned PUT/GET URL для
// S3-совместимого хранилища (Backblaze B2), используемого generic-механизмом
// файлов сущностей (см. artifacts/PET/pages/integrations/
// obschie-trebovaniya-s3-hranilische-faylov.md). Backend никогда не
// проксирует байты файла и не выдаёт клиенту постоянные S3-ключи — только
// временные подписанные ссылки на конкретную операцию.
package s3client

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// UploadURLTTL и DownloadURLTTL — время жизни presigned URL. Конкретное
// значение — деталь реализации backend (см. «Общие требования:
// S3-хранилище файлов»). UploadURLTTL остаётся коротким — ссылка
// расходуется сразу же одним PUT-запросом клиента. DownloadURLTTL выставлен
// в максимум, допустимый протоколом SigV4 для долгоживущих ключей доступа
// (X-Amz-Expires не может превышать 7 дней), чтобы photo_url не устаревал
// в течение обычной сессии просмотра карточки питомца.
const (
	UploadURLTTL   = 5 * time.Minute
	DownloadURLTTL = 7 * 24 * time.Hour
)

// GetURLCacheTTL — окно, в течение которого PresignGetURL повторно отдаёт
// уже подписанную ссылку на тот же objectKey вместо новой подписи. Без этого
// каждый вызов (например список питомцев и карточка питомца, запрошенные
// с разницей в секунды) получает разные query-параметры подписи
// (X-Amz-Signature/X-Amz-Date) для одного и того же файла, и клиентский
// image-кэш (ключ — строка URL) считает это разными ресурсами и качает файл
// заново.
//
// Значение равно DownloadURLTTL: ссылка переиспользуется всё время, пока она
// физически действительна, а не только в течение какого-то короткого окна.
// Это безопасно, потому что на клиенте нет фонового обновления данных о
// питомце помимо явных запросов — а любой запрос, меняющий фото (новую
// загрузку), создаёт новый objectKey и инвалидирует связанные данные на
// фронтенде, так что старая закэшированная ссылка на предыдущий objectKey
// просто перестаёт запрашиваться.
const GetURLCacheTTL = DownloadURLTTL

// Config — параметры доступа к S3-совместимому хранилищу. Значения приходят
// из переменных окружения (см. ConfigFromEnv), никогда не хранятся в
// репозитории и никогда не передаются клиенту.
type Config struct {
	Endpoint       string // endpoint S3-совместимого API (Backblaze B2)
	KeyID          string // access key
	ApplicationKey string // secret key
	Bucket         string
	Region         string // Backblaze B2 не проверяет регион, но SDK требует непустое значение
}

// ConfigFromEnv читает конфигурацию S3 из переменных окружения:
// S3_ENDPOINT, S3_KEY_ID, S3_APPLICATION_KEY, S3_BUCKET (обязательные),
// S3_REGION (необязательная, по умолчанию "us-east-1").
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		Endpoint:       os.Getenv("S3_ENDPOINT"),
		KeyID:          os.Getenv("S3_KEY_ID"),
		ApplicationKey: os.Getenv("S3_APPLICATION_KEY"),
		Bucket:         os.Getenv("S3_BUCKET"),
		Region:         os.Getenv("S3_REGION"),
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	var missing []string
	if cfg.Endpoint == "" {
		missing = append(missing, "S3_ENDPOINT")
	}
	if cfg.KeyID == "" {
		missing = append(missing, "S3_KEY_ID")
	}
	if cfg.ApplicationKey == "" {
		missing = append(missing, "S3_APPLICATION_KEY")
	}
	if cfg.Bucket == "" {
		missing = append(missing, "S3_BUCKET")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("не заданы переменные окружения для S3-хранилища: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

// Client — тонкая обёртка над AWS S3 SDK v2, настроенная на кастомный
// S3-совместимый endpoint с path-style адресацией (это нужно Backblaze B2).
type Client struct {
	s3      *s3.Client
	presign *s3.PresignClient
	bucket  string

	// getURLCacheMu защищает getURLCache: PresignGetURL вызывается из
	// обработчиков параллельных HTTP-запросов (см. handlers/pet.go,
	// handlers/events.go), а Client — единственный синглтон-инстанс на
	// процесс (см. main.go).
	getURLCacheMu sync.Mutex
	getURLCache   map[string]cachedGetURL
}

// cachedGetURL — запись кэша presigned GET URL для одного objectKey.
type cachedGetURL struct {
	url      string
	cachedAt time.Time
}

// New создаёт Client по конфигурации. Ключи (KeyID/ApplicationKey) остаются
// только на стороне backend — Client лишь подписывает presigned URL ими,
// сами ключи наружу не отдаются.
func New(cfg Config) *Client {
	s3Svc := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		// Backblaze B2 S3-совместимый API требует path-style адресации
		// (https://endpoint/bucket/key), а не virtual-hosted-style.
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.KeyID, cfg.ApplicationKey, ""),
		// DeleteObject вызывается только как best-effort (см. «Удаление
		// файла») и его ошибка не проваливает запрос клиента — незачем
		// удерживать запрос повторными попытками при недоступном S3.
		RetryMaxAttempts: 1,
	})
	return &Client{
		s3:          s3Svc,
		presign:     s3.NewPresignClient(s3Svc),
		bucket:      cfg.Bucket,
		getURLCache: make(map[string]cachedGetURL),
	}
}

// PresignPutURL подписывает presigned PUT URL на объект objectKey с
// привязкой к contentType и TTL = UploadURLTTL. Подпись — чисто локальная
// операция (не требует сетевого обращения к S3).
func (c *Client) PresignPutURL(ctx context.Context, objectKey, contentType string) (url string, expiresIn int, err error) {
	req, err := c.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(objectKey),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(UploadURLTTL))
	if err != nil {
		return "", 0, err
	}
	return req.URL, int(UploadURLTTL.Seconds()), nil
}

// PresignGetURL подписывает presigned GET URL на объект objectKey с
// TTL = DownloadURLTTL. Повторные вызовы для одного и того же objectKey в
// течение GetURLCacheTTL отдают ту же самую строку URL из кэша вместо новой
// подписи — см. комментарий к GetURLCacheTTL.
func (c *Client) PresignGetURL(ctx context.Context, objectKey string) (url string, err error) {
	if cached, ok := c.cachedGetURL(objectKey); ok {
		return cached, nil
	}

	req, err := c.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(objectKey),
	}, s3.WithPresignExpires(DownloadURLTTL))
	if err != nil {
		return "", err
	}

	c.getURLCacheMu.Lock()
	c.getURLCache[objectKey] = cachedGetURL{url: req.URL, cachedAt: time.Now()}
	c.getURLCacheMu.Unlock()

	return req.URL, nil
}

// cachedGetURL возвращает ранее закэшированный presigned GET URL для
// objectKey, если он ещё не устарел (см. GetURLCacheTTL).
func (c *Client) cachedGetURL(objectKey string) (string, bool) {
	c.getURLCacheMu.Lock()
	defer c.getURLCacheMu.Unlock()

	entry, ok := c.getURLCache[objectKey]
	if !ok || time.Since(entry.cachedAt) >= GetURLCacheTTL {
		return "", false
	}
	return entry.url, true
}

// DeleteObject удаляет объект в S3 напрямую (не presigned — backend имеет
// собственные ключи доступа). Вызывающий код (см. handlers/files.go)
// трактует ошибку как best-effort сбой и не проваливает запрос клиента.
func (c *Client) DeleteObject(ctx context.Context, objectKey string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(objectKey),
	})
	return err
}
