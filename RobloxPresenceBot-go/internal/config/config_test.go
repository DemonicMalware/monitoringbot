package config

import (
	"os"
	"path/filepath"
	"testing"
)

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func clearEnv(t *testing.T) {
	for _, key := range []string{
		"DISCORD_WEBHOOK_URL",
		"ROBLOSECURITY",
		"POLL_INTERVAL_SECONDS",
		"TIME_ZONE",
		"LOG_LEVEL",
		"DRY_RUN",
	} {
		unsetEnv(t, key)
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval() != 30 {
		t.Fatalf("intervalo por defecto inesperado: %d", cfg.PollInterval())
	}
	if !cfg.StatusPanel.Enabled {
		t.Fatal("el panel debe estar activo por defecto")
	}
	if !cfg.Notify.Connect || !cfg.Notify.Disconnect || cfg.Notify.StartupSummary {
		t.Fatalf("notificaciones por defecto inesperadas: %+v", cfg.Notify)
	}
	if cfg.IsDryRun() {
		t.Fatal("dry-run debe estar desactivado por defecto")
	}
}

func TestLoadFilesAndEnvironment(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()

	configJSON := `{
		"pollIntervalSeconds": 5,
		"notify": {"connect": false},
		"statusPanel": {"enabled": false}
	}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	envFile := `DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/123/token
ROBLOSECURITY=.ROBLOSECURITY=cookie-de-prueba
`
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envFile), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval() != 10 {
		t.Fatalf("el intervalo debe limitarse a 10, se obtuvo %d", cfg.PollInterval())
	}
	if cfg.WebhookURL != "https://discord.com/api/webhooks/123/token" {
		t.Fatalf("webhook inesperado: %q", cfg.WebhookURL)
	}
	if cfg.RobloxCookie != "cookie-de-prueba" {
		t.Fatalf("cookie inesperada: %q", cfg.RobloxCookie)
	}
	if cfg.Notify.Connect {
		t.Fatal("notify.connect debe respetar config.json")
	}
	if !cfg.Notify.Disconnect {
		t.Fatal("notify.disconnect debe conservar el valor por defecto")
	}
	if cfg.StatusPanel.Enabled {
		t.Fatal("statusPanel.enabled debe respetar config.json")
	}

	t.Setenv("POLL_INTERVAL_SECONDS", "120")
	t.Setenv("TIME_ZONE", "America/Argentina/Buenos_Aires")
	cfg, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval() != 120 {
		t.Fatalf("la variable de entorno debe tener prioridad: %d", cfg.PollInterval())
	}
	if cfg.TimeZone != "America/Argentina/Buenos_Aires" {
		t.Fatalf("zona horaria inesperada: %q", cfg.TimeZone)
	}
}

func TestValidateWebhookURL(t *testing.T) {
	valid := []string{
		"https://discord.com/api/webhooks/123/token",
		"https://discordapp.com/api/webhooks/123/token",
		"https://ptb.discord.com/api/webhooks/123/token",
		"https://canary.discord.com/api/webhooks/123/token",
	}
	for _, value := range valid {
		if err := ValidateWebhookURL(value); err != nil {
			t.Fatalf("%q debería ser válido: %v", value, err)
		}
	}

	invalid := []string{
		"",
		"no es una url",
		"ftp://discord.com/api/webhooks/123/token",
		"https://example.com/api/webhooks/123/token",
	}
	for _, value := range invalid {
		if err := ValidateWebhookURL(value); err == nil {
			t.Fatalf("%q debería ser inválido", value)
		}
	}
}

func TestNormalizeCookie(t *testing.T) {
	cases := map[string]string{
		".ROBLOSECURITY=abc":   "abc",
		"  .roblosecurity=abc": "abc",
		`"abc"`:                "abc",
		"'abc'":                "abc",
		"abc":                  "abc",
	}
	for input, want := range cases {
		if got := NormalizeCookie(input); got != want {
			t.Fatalf("NormalizeCookie(%q) = %q, se esperaba %q", input, got, want)
		}
	}
}
