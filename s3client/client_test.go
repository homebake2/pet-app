package s3client

import (
	"context"
	"net/url"
	"testing"
	"time"
)

// TestPresignGetURL_CachesSameURLForSameObjectKey проверяет, что повторный
// вызов PresignGetURL для того же objectKey отдаёт закэшированную запись, а
// не пересчитывает подпись заново (что дало бы другую строку URL и сломало
// бы клиентский image-кэш — см. GetURLCacheTTL).
func TestPresignGetURL_CachesSameURLForSameObjectKey(t *testing.T) {
	c := &Client{getURLCache: make(map[string]cachedGetURL)}

	const objectKey = "pets/photo-1.jpg"
	c.getURLCache[objectKey] = cachedGetURL{
		url:      "https://example.com/pets/photo-1.jpg?X-Amz-Signature=first",
		cachedAt: time.Now(),
	}

	got, ok := c.cachedGetURL(objectKey)
	if !ok {
		t.Fatalf("expected cache hit for %q, got miss", objectKey)
	}
	if got != "https://example.com/pets/photo-1.jpg?X-Amz-Signature=first" {
		t.Fatalf("unexpected cached URL: %q", got)
	}
}

// TestPresignGetURL_CacheMissForUnknownKey проверяет, что для ещё не
// закэшированного objectKey возвращается промах, а не паника/нулевое значение.
func TestPresignGetURL_CacheMissForUnknownKey(t *testing.T) {
	c := &Client{getURLCache: make(map[string]cachedGetURL)}

	if _, ok := c.cachedGetURL("unknown-key"); ok {
		t.Fatalf("expected cache miss for unknown key")
	}
}

// TestPresignGetURL_CacheExpires проверяет, что запись старше GetURLCacheTTL
// считается истёкшей и не возвращается из кэша.
func TestPresignGetURL_CacheExpires(t *testing.T) {
	c := &Client{getURLCache: make(map[string]cachedGetURL)}

	const objectKey = "pets/photo-1.jpg"
	c.getURLCache[objectKey] = cachedGetURL{
		url:      "https://example.com/pets/photo-1.jpg?X-Amz-Signature=stale",
		cachedAt: time.Now().Add(-GetURLCacheTTL - time.Second),
	}

	if _, ok := c.cachedGetURL(objectKey); ok {
		t.Fatalf("expected cache miss for expired entry")
	}
}

// TestPresignGetURL_SetsResponseCacheControl проверяет, что подписанная
// ссылка просит S3 вернуть Cache-Control, позволяющий клиентскому кэшу не
// скачивать картинку повторно.
func TestPresignGetURL_SetsResponseCacheControl(t *testing.T) {
	c := New(Config{
		Endpoint:       "https://s3.example.com",
		KeyID:          "key",
		ApplicationKey: "secret",
		Bucket:         "bucket",
		Region:         "us-east-1",
	})

	got, err := c.PresignGetURL(context.Background(), "pets/photo-1.jpg")
	if err != nil {
		t.Fatalf("PresignGetURL: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if cc := parsed.Query().Get("response-cache-control"); cc != GetResponseCacheControl {
		t.Fatalf("response-cache-control = %q, want %q", cc, GetResponseCacheControl)
	}
}
