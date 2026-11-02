package handlers

import (
	"strings"
	"time"
)

const (
	// maxLoginFailuresPerLogin — сколько неверных паролей для одного логина
	// допускается в окне loginFailureWindow, прежде чем вход в него начнёт
	// отвечать 429. Лимит по логину не зависит от IP, поэтому не обходится
	// подменой X-Forwarded-For.
	maxLoginFailuresPerLogin = 10
	// maxLoginFailuresPerIP — то же для всех логинов с одного IP: ограничивает
	// перебор по списку логинов.
	maxLoginFailuresPerIP = 30
	// loginFailureWindow — окно подсчёта неудачных попыток.
	loginFailureWindow = 15 * time.Minute
)

// loginRateLimitKeys возвращает ключи счётчиков неудачных попыток для
// логина (нормализованного так же, как при поиске пользователя) и IP.
func loginRateLimitKeys(login, ip string) (loginKey, ipKey string) {
	return "login:" + strings.ToLower(strings.TrimSpace(login)), "ip:" + ip
}
