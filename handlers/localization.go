package handlers

import "strings"

// Общий механизм выбора перевода при чтении контента из БД: для каждой
// переводимой сущности хранится таблица <entity>_translation, а здесь по
// цепочке «язык запроса -> язык по умолчанию» выбирается значение каждого
// поля независимо. Пара значений, имеющих смысл только вместе (например,
// URL картинки и её blurhash), берётся из одной строки перевода — для этого
// служит pickTranslationRow. Механизм не знает о баннерах и пригоден для
// любой следующей сущности с таблицей переводов.

// defaultLocale — язык по умолчанию (запасной в цепочке выбора).
const defaultLocale = "ru"

// supportedLocales — значения LanguageCode, поддерживаемые сервисом.
var supportedLocales = []string{"ru", "en"}

func isSupportedLocale(locale string) bool {
	for _, l := range supportedLocales {
		if l == locale {
			return true
		}
	}
	return false
}

// localeChain строит цепочку языков для выбора значений: язык запроса, затем
// язык по умолчанию (без повторов).
func localeChain(requested string) []string {
	if requested == defaultLocale {
		return []string{defaultLocale}
	}
	return []string{requested, defaultLocale}
}

// isBlankString: nil и пустая строка приравниваются к отсутствию перевода.
func isBlankString(s *string) bool {
	return s == nil || *s == ""
}

// nonBlankString возвращает s, если значение задано, иначе nil.
func nonBlankString(s *string) *string {
	if isBlankString(s) {
		return nil
	}
	return s
}

// pickTranslationRow возвращает первую по цепочке строку перевода, для
// которой has сообщает, что нужное значение (или группа значений) задано.
// ok=false — ни в одной строке цепочки значения нет.
func pickTranslationRow[T any](chain []string, rows map[string]T, has func(T) bool) (row T, ok bool) {
	for _, locale := range chain {
		candidate, found := rows[locale]
		if found && has(candidate) {
			return candidate, true
		}
	}
	var zero T
	return zero, false
}

// pickTranslatedString выбирает значение одного поля по цепочке; nil, если
// его нет ни для языка запроса, ни для языка по умолчанию.
func pickTranslatedString[T any](chain []string, rows map[string]T, get func(T) *string) *string {
	row, ok := pickTranslationRow(chain, rows, func(r T) bool { return !isBlankString(get(r)) })
	if !ok {
		return nil
	}
	return get(row)
}

// etagMatches проверяет заголовок If-None-Match на совпадение с etag
// (слабое сравнение: префикс W/ игнорируется, поддерживаются список и "*").
func etagMatches(ifNoneMatch, etag string) bool {
	for _, part := range strings.Split(ifNoneMatch, ",") {
		candidate := strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}
