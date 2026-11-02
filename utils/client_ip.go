package utils

import (
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
)

// clientIPHeader — имя заголовка, из которого берётся адрес клиента. Заголовок
// должен выставлять только доверенный прокси перед сервисом, затирая значение,
// присланное клиентом (так делает Cloudflare с CF-Connecting-IP). Пусто —
// адрес берётся только из соединения (r.RemoteAddr).
var clientIPHeader = defaultClientIPHeader()

// defaultClientIPHeader читает CLIENT_IP_HEADER; на Render (там задана RENDER)
// без явной настройки используется CF-Connecting-IP: сервис стоит за
// Cloudflare. X-Forwarded-For не используется никогда: прокси дописывает
// адрес в конец уже присланного клиентом значения, поэтому любой его элемент
// может быть подделан.
func defaultClientIPHeader() string {
	if name := strings.TrimSpace(os.Getenv("CLIENT_IP_HEADER")); name != "" {
		return name
	}
	if os.Getenv("RENDER") != "" {
		return "CF-Connecting-IP"
	}
	return ""
}

// SetClientIPHeader переопределяет заголовок адреса клиента (для тестов).
// Вызывать до старта сервера: значение не защищено от конкурентной записи.
func SetClientIPHeader(name string) {
	clientIPHeader = name
}

// ClientIP определяет IP-адрес клиента для rate-limiting и логов. Если задан
// доверенный заголовок и в нём корректный адрес — берёт его, иначе адрес
// соединения.
func ClientIP(r *http.Request) string {
	if clientIPHeader != "" {
		if addr, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(clientIPHeader))); err == nil {
			return addr.Unmap().String()
		}
	}

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		return host
	}

	return r.RemoteAddr
}
