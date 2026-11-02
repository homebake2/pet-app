package utils

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	original := clientIPHeader
	t.Cleanup(func() { clientIPHeader = original })

	tests := []struct {
		name    string
		header  string
		headers map[string]string
		want    string
	}{
		{
			name:    "без доверенного заголовка X-Forwarded-For игнорируется",
			header:  "",
			headers: map[string]string{"X-Forwarded-For": "1.2.3.4, 5.6.7.8"},
			want:    "192.0.2.10",
		},
		{
			name:    "доверенный заголовок с корректным адресом",
			header:  "CF-Connecting-IP",
			headers: map[string]string{"CF-Connecting-IP": "203.0.113.7"},
			want:    "203.0.113.7",
		},
		{
			name:    "подделанный X-Forwarded-For не влияет при доверенном заголовке",
			header:  "CF-Connecting-IP",
			headers: map[string]string{"CF-Connecting-IP": "203.0.113.7", "X-Forwarded-For": "1.2.3.4"},
			want:    "203.0.113.7",
		},
		{
			name:    "IPv4-mapped IPv6 приводится к IPv4",
			header:  "CF-Connecting-IP",
			headers: map[string]string{"CF-Connecting-IP": "::ffff:203.0.113.7"},
			want:    "203.0.113.7",
		},
		{
			name:    "мусор в доверенном заголовке — адрес соединения",
			header:  "CF-Connecting-IP",
			headers: map[string]string{"CF-Connecting-IP": "не адрес"},
			want:    "192.0.2.10",
		},
		{
			name:   "доверенный заголовок отсутствует — адрес соединения",
			header: "CF-Connecting-IP",
			want:   "192.0.2.10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetClientIPHeader(tt.header)
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "192.0.2.10:54321"
			for name, value := range tt.headers {
				req.Header.Set(name, value)
			}

			if got := ClientIP(req); got != tt.want {
				t.Fatalf("ClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
