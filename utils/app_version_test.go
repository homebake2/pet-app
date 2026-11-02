package utils

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAppVersionFromRequest(t *testing.T) {
	cases := map[string]string{
		"1.2.3":         "1.2.3",
		"":              "",
		"1.2":           "",
		"v1.2.3":        "",
		"1.2.3\nforged": "",
		"1.2.3-beta":    "",
		"999999.1.1":    "",
	}
	for header, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		if header != "" {
			r.Header[AppVersionHeader] = []string{header}
		}
		assert.Equal(t, want, AppVersionFromRequest(r), "заголовок %q", header)
	}
}
