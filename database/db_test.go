package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithBinaryParameters(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "URL без параметров",
			in:   "postgres://postgres@127.0.0.1:5555/pets",
			want: "postgres://postgres@127.0.0.1:5555/pets?binary_parameters=yes",
		},
		{
			name: "URL с параметрами сохраняет их",
			in:   "postgresql://u:p@host/db?sslmode=require",
			want: "postgresql://u:p@host/db?binary_parameters=yes&sslmode=require",
		},
		{
			name: "явно заданный режим не меняется",
			in:   "postgres://host/db?binary_parameters=no",
			want: "postgres://host/db?binary_parameters=no",
		},
		{
			name: "строка key=value",
			in:   "host=localhost dbname=pets",
			want: "host=localhost dbname=pets binary_parameters=yes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := withBinaryParameters(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
