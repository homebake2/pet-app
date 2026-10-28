package database

// AppConfigRecord — единственная строка таблицы app_config.
type AppConfigRecord struct {
	AboutMeURL string
}

// GetAppConfig читает единственную строку app_config; sql.ErrNoRows, если
// строки нет. Вызывается на каждый запрос GET /config без кеша, поэтому
// изменение значения в БД вступает в силу без перезапуска сервиса.
func GetAppConfig() (AppConfigRecord, error) {
	var rec AppConfigRecord
	err := DB.QueryRow(`SELECT about_me_url FROM app_config WHERE id`).Scan(&rec.AboutMeURL)
	if err != nil {
		return AppConfigRecord{}, err
	}
	return rec, nil
}
