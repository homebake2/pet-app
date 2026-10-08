package database

import (
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/lib/pq"
)

// BannerTranslationRecord — строка banner_translation (переводимые поля баннера).
// Все поля nullable: обязательность определяется правилами шаблона, а не схемой.
type BannerTranslationRecord struct {
	Overline       *string
	Title          *string
	Body           *string
	Badge          *string
	ImageURL       *string
	ImageBlurhash  *string
	CtaLabel       *string
	SecondaryLabel *string
}

// BannerSlideTranslationRecord — строка banner_slide_translation.
type BannerSlideTranslationRecord struct {
	Title         *string
	Body          *string
	ImageURL      *string
	ImageBlurhash *string
}

// BannerSlideRecord — слайд баннера с переводами по языкам (ключ — locale).
type BannerSlideRecord struct {
	ID           string
	Position     int
	Translations map[string]BannerSlideTranslationRecord
}

// BannerTargetingRecord — строка banner_targeting. Params — сырой jsonb:
// разбор и проверка соответствия типу правила выполняются в handlers.
type BannerTargetingRecord struct {
	Type   string
	Params json.RawMessage
}

// BannerRecord — баннер со всеми связанными строками. Для публичной выдачи
// переводы и слайды загружаются только для нужных языков.
type BannerRecord struct {
	ID           string
	Layout       string
	Tone         *string
	IsShow       bool
	StartsAt     *time.Time
	EndsAt       *time.Time
	Priority     int
	Frequency    string
	CtaAction    *string
	CtaTarget    *string
	CreatedAt    time.Time
	Translations map[string]BannerTranslationRecord
	Slides       []BannerSlideRecord
	Targeting    []BannerTargetingRecord
	UserIDs      []string
}

const bannerColumns = `b.id, b.layout, b.tone, b.is_show, b.starts_at, b.ends_at, b.priority, b.frequency, b.cta_action, b.cta_target, b.created_at`

func scanBannerBase(rows *sql.Rows) (BannerRecord, error) {
	var rec BannerRecord
	err := rows.Scan(&rec.ID, &rec.Layout, &rec.Tone, &rec.IsShow, &rec.StartsAt, &rec.EndsAt,
		&rec.Priority, &rec.Frequency, &rec.CtaAction, &rec.CtaTarget, &rec.CreatedAt)
	return rec, err
}

func queryBannerBase(query string, args ...any) ([]BannerRecord, error) {
	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []BannerRecord
	for rows.Next() {
		rec, err := scanBannerBase(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, rec)
	}
	return result, rows.Err()
}

// ListBannerCandidates возвращает баннеры, прошедшие условия, которые целиком
// выражаются в SQL: ручной выключатель, окно показа и персональный список
// banner_user (нет строк — доступен всем, иначе только перечисленным).
// Таргетинг по правилам вычисляется в handlers. Порядок выдачи:
// priority DESC, created_at DESC. Переводы, слайды и правила не загружаются.
func ListBannerCandidates(userID string) ([]BannerRecord, error) {
	return queryBannerBase(`
		SELECT `+bannerColumns+`
		FROM banner b
		WHERE b.is_show
		  AND (b.starts_at IS NULL OR b.starts_at <= now())
		  AND (b.ends_at IS NULL OR now() < b.ends_at)
		  AND (
		        NOT EXISTS (SELECT 1 FROM banner_user bu WHERE bu.banner_id = b.id)
		        OR EXISTS (SELECT 1 FROM banner_user bu WHERE bu.banner_id = b.id AND bu.user_id = $1)
		      )
		ORDER BY b.priority DESC, b.created_at DESC
	`, userID)
}

// UserHasPets сообщает, есть ли у пользователя хотя бы один не удалённый питомец.
func UserHasPets(userID string) (bool, error) {
	var exists bool
	err := DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM pet WHERE user_id = $1 AND deleted_at IS NULL)`, userID).Scan(&exists)
	return exists, err
}

// GetBannerTargeting возвращает правила таргетинга по баннерам одним запросом.
func GetBannerTargeting(bannerIDs []string) (map[string][]BannerTargetingRecord, error) {
	rows, err := DB.Query(`
		SELECT banner_id, type, params
		FROM banner_targeting
		WHERE banner_id = ANY($1::uuid[])
		ORDER BY banner_id, type
	`, pq.Array(bannerIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string][]BannerTargetingRecord{}
	for rows.Next() {
		var bannerID string
		var rec BannerTargetingRecord
		if err := rows.Scan(&bannerID, &rec.Type, &rec.Params); err != nil {
			return nil, err
		}
		result[bannerID] = append(result[bannerID], rec)
	}
	return result, rows.Err()
}

// GetBannerTranslations возвращает строки перевода баннеров одним запросом:
// banner_id -> locale -> перевод. Пустой locales означает «все языки».
func GetBannerTranslations(bannerIDs []string, locales []string) (map[string]map[string]BannerTranslationRecord, error) {
	if locales == nil {
		locales = []string{}
	}
	rows, err := DB.Query(`
		SELECT banner_id, locale, overline, title, body, badge, image_url, image_blurhash, cta_label, secondary_label
		FROM banner_translation
		WHERE banner_id = ANY($1::uuid[])
		  AND (cardinality($2::text[]) = 0 OR locale = ANY($2::text[]))
	`, pq.Array(bannerIDs), pq.Array(locales))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]map[string]BannerTranslationRecord{}
	for rows.Next() {
		var bannerID, locale string
		var tr BannerTranslationRecord
		if err := rows.Scan(&bannerID, &locale, &tr.Overline, &tr.Title, &tr.Body, &tr.Badge,
			&tr.ImageURL, &tr.ImageBlurhash, &tr.CtaLabel, &tr.SecondaryLabel); err != nil {
			return nil, err
		}
		if result[bannerID] == nil {
			result[bannerID] = map[string]BannerTranslationRecord{}
		}
		result[bannerID][locale] = tr
	}
	return result, rows.Err()
}

// GetBannerSlides возвращает слайды баннеров вместе с переводами одним
// запросом (LEFT JOIN: слайд без единого перевода тоже попадает в результат,
// чтобы его можно было признать невалидным). Слайды упорядочены по position.
// Пустой locales означает «все языки».
func GetBannerSlides(bannerIDs []string, locales []string) (map[string][]BannerSlideRecord, error) {
	if locales == nil {
		locales = []string{}
	}
	rows, err := DB.Query(`
		SELECT s.banner_id, s.id, s.position, t.locale, t.title, t.body, t.image_url, t.image_blurhash
		FROM banner_slide s
		LEFT JOIN banner_slide_translation t
		       ON t.slide_id = s.id
		      AND (cardinality($2::text[]) = 0 OR t.locale = ANY($2::text[]))
		WHERE s.banner_id = ANY($1::uuid[])
		ORDER BY s.banner_id, s.position
	`, pq.Array(bannerIDs), pq.Array(locales))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string][]BannerSlideRecord{}
	for rows.Next() {
		var bannerID string
		var slide BannerSlideRecord
		var locale sql.NullString
		var tr BannerSlideTranslationRecord
		if err := rows.Scan(&bannerID, &slide.ID, &slide.Position, &locale, &tr.Title, &tr.Body, &tr.ImageURL, &tr.ImageBlurhash); err != nil {
			return nil, err
		}

		slides := result[bannerID]
		if len(slides) == 0 || slides[len(slides)-1].ID != slide.ID {
			slide.Translations = map[string]BannerSlideTranslationRecord{}
			slides = append(slides, slide)
		}
		if locale.Valid {
			slides[len(slides)-1].Translations[locale.String] = tr
		}
		result[bannerID] = slides
	}
	return result, rows.Err()
}

// GetBannerUserIDs возвращает персональные списки banner_user по баннерам.
func GetBannerUserIDs(bannerIDs []string) (map[string][]string, error) {
	rows, err := DB.Query(`
		SELECT banner_id, user_id
		FROM banner_user
		WHERE banner_id = ANY($1::uuid[])
		ORDER BY banner_id, user_id
	`, pq.Array(bannerIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string][]string{}
	for rows.Next() {
		var bannerID, userID string
		if err := rows.Scan(&bannerID, &userID); err != nil {
			return nil, err
		}
		result[bannerID] = append(result[bannerID], userID)
	}
	return result, rows.Err()
}

// loadBannerRelations дозагружает в баннеры все связанные строки (все языки,
// слайды, правила, пользователей) — полное представление для admin API.
func loadBannerRelations(banners []BannerRecord) error {
	if len(banners) == 0 {
		return nil
	}
	ids := make([]string, len(banners))
	for i, b := range banners {
		ids[i] = b.ID
	}

	translations, err := GetBannerTranslations(ids, nil)
	if err != nil {
		return err
	}
	slides, err := GetBannerSlides(ids, nil)
	if err != nil {
		return err
	}
	targeting, err := GetBannerTargeting(ids)
	if err != nil {
		return err
	}
	userIDs, err := GetBannerUserIDs(ids)
	if err != nil {
		return err
	}

	for i := range banners {
		id := banners[i].ID
		banners[i].Translations = translations[id]
		banners[i].Slides = slides[id]
		banners[i].Targeting = targeting[id]
		banners[i].UserIDs = userIDs[id]
	}
	return nil
}

// ListAllBanners возвращает все баннеры (включая выключенные и вне окна
// показа) со всеми связанными строками, created_at DESC.
func ListAllBanners() ([]BannerRecord, error) {
	banners, err := queryBannerBase(`SELECT ` + bannerColumns + ` FROM banner b ORDER BY b.created_at DESC, b.id`)
	if err != nil {
		return nil, err
	}
	if err := loadBannerRelations(banners); err != nil {
		return nil, err
	}
	return banners, nil
}

// GetBannerByID возвращает баннер со всеми связанными строками;
// sql.ErrNoRows, если баннера нет.
func GetBannerByID(id string) (BannerRecord, error) {
	banners, err := queryBannerBase(`SELECT `+bannerColumns+` FROM banner b WHERE b.id = $1`, id)
	if err != nil {
		return BannerRecord{}, err
	}
	if len(banners) == 0 {
		return BannerRecord{}, sql.ErrNoRows
	}
	if err := loadBannerRelations(banners); err != nil {
		return BannerRecord{}, err
	}
	return banners[0], nil
}

// ExistingUserIDs возвращает подмножество переданных id, которые есть в users.
func ExistingUserIDs(ids []string) (map[string]bool, error) {
	rows, err := DB.Query(`SELECT id FROM users WHERE id = ANY($1::uuid[])`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

func sortedLocales[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SaveBanner записывает баннер и все связанные строки одной транзакцией.
// Если баннера с rec.ID нет — создаёт (created=true), иначе полностью
// заменяет: переводы, слайды, правила и пользователей, которых нет в rec,
// удаляются. created_at существующего баннера не меняется. Возвращает
// актуальный created_at.
func SaveBanner(rec BannerRecord) (created bool, createdAt time.Time, err error) {
	tx, err := DB.Begin()
	if err != nil {
		return false, time.Time{}, err
	}
	defer tx.Rollback()

	// xmax = 0 — признак того, что строка вставлена, а не обновлена через ON CONFLICT.
	err = tx.QueryRow(`
		INSERT INTO banner (id, layout, tone, is_show, starts_at, ends_at, priority, frequency, cta_action, cta_target)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			layout = EXCLUDED.layout,
			tone = EXCLUDED.tone,
			is_show = EXCLUDED.is_show,
			starts_at = EXCLUDED.starts_at,
			ends_at = EXCLUDED.ends_at,
			priority = EXCLUDED.priority,
			frequency = EXCLUDED.frequency,
			cta_action = EXCLUDED.cta_action,
			cta_target = EXCLUDED.cta_target
		RETURNING (xmax = 0), created_at
	`, rec.ID, rec.Layout, rec.Tone, rec.IsShow, rec.StartsAt, rec.EndsAt, rec.Priority, rec.Frequency, rec.CtaAction, rec.CtaTarget).
		Scan(&created, &createdAt)
	if err != nil {
		return false, time.Time{}, err
	}

	if !created {
		// banner_slide_translation удаляется каскадом вместе со слайдами.
		for _, table := range []string{"banner_translation", "banner_slide", "banner_targeting", "banner_user"} {
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE banner_id = $1`, rec.ID); err != nil {
				return false, time.Time{}, err
			}
		}
	}

	// Вставка в порядке языков: результат не зависит от порядка обхода map.
	for _, locale := range sortedLocales(rec.Translations) {
		tr := rec.Translations[locale]
		if _, err := tx.Exec(`
			INSERT INTO banner_translation (banner_id, locale, overline, title, body, badge, image_url, image_blurhash, cta_label, secondary_label)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, rec.ID, locale, tr.Overline, tr.Title, tr.Body, tr.Badge, tr.ImageURL, tr.ImageBlurhash, tr.CtaLabel, tr.SecondaryLabel); err != nil {
			return false, time.Time{}, err
		}
	}

	for _, slide := range rec.Slides {
		if _, err := tx.Exec(`INSERT INTO banner_slide (id, banner_id, position) VALUES ($1, $2, $3)`,
			slide.ID, rec.ID, slide.Position); err != nil {
			return false, time.Time{}, err
		}
		for _, locale := range sortedLocales(slide.Translations) {
			tr := slide.Translations[locale]
			if _, err := tx.Exec(`
				INSERT INTO banner_slide_translation (slide_id, locale, title, body, image_url, image_blurhash)
				VALUES ($1, $2, $3, $4, $5, $6)
			`, slide.ID, locale, tr.Title, tr.Body, tr.ImageURL, tr.ImageBlurhash); err != nil {
				return false, time.Time{}, err
			}
		}
	}

	for _, rule := range rec.Targeting {
		if _, err := tx.Exec(`INSERT INTO banner_targeting (banner_id, type, params) VALUES ($1, $2, $3)`,
			rec.ID, rule.Type, string(rule.Params)); err != nil {
			return false, time.Time{}, err
		}
	}

	for _, userID := range rec.UserIDs {
		if _, err := tx.Exec(`INSERT INTO banner_user (banner_id, user_id) VALUES ($1, $2)`, rec.ID, userID); err != nil {
			return false, time.Time{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return false, time.Time{}, err
	}
	return created, createdAt, nil
}

// SetBannerIsShow меняет только is_show; found=false, если баннера нет.
func SetBannerIsShow(id string, isShow bool) (found bool, err error) {
	result, err := DB.Exec(`UPDATE banner SET is_show = $2 WHERE id = $1`, id, isShow)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// DeleteBanner физически удаляет баннер (связанные строки — каскадом);
// found=false, если баннера нет.
func DeleteBanner(id string) (found bool, err error) {
	result, err := DB.Exec(`DELETE FROM banner WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
