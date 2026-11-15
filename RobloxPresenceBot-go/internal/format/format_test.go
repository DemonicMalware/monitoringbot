package format

import (
	"testing"
	"time"
)

func int64Ptr(value int64) *int64 {
	return &value
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		name string
		ms   *int64
		want string
	}{
		{"nil", nil, "desconocido"},
		{"negativo", int64Ptr(-1), "desconocido"},
		{"cero", int64Ptr(0), "menos de 1 s"},
		{"segundos", int64Ptr(45000), "45 s"},
		{"hora y minuto", int64Ptr(3661000), "1 h 1 min"},
		{"dias", int64Ptr(90061000), "1 d 1 h 1 min"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := FormatDuration(test.ms); got != test.want {
				t.Fatalf("FormatDuration(%v) = %q, se esperaba %q", test.ms, got, test.want)
			}
		})
	}
}

func TestDiscordAndUTCTime(t *testing.T) {
	value := time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC)
	if got := DiscordTime(value, "F"); got != "<t:1794679200:F>" {
		t.Fatalf("DiscordTime inesperado: %s", got)
	}
	if got := UTCString(value); got != "2026-11-14 18:00:00 UTC" {
		t.Fatalf("UTCString inesperado: %s", got)
	}
	if got := ISOMillis(value); got != "2026-11-14T18:00:00.000Z" {
		t.Fatalf("ISOMillis inesperado: %s", got)
	}
}

func TestEscapeAndTruncate(t *testing.T) {
	if got := EscapeMarkdown("Juego_*uno*"); got != `Juego\_\*uno\*` {
		t.Fatalf("escape inesperado: %s", got)
	}
	if got := Truncate("abcdef", 4); got != "abc…" {
		t.Fatalf("truncate inesperado: %s", got)
	}
	if got := Truncate("abc", 4); got != "abc" {
		t.Fatalf("truncate no debía cambiar el texto: %s", got)
	}
}
