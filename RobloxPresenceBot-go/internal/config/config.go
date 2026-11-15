package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FlexInt acepta números o cadenas numéricas en JSON.
type FlexInt int

func (f *FlexInt) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		raw = strings.TrimSpace(text)
	}
	if n, err := strconv.Atoi(raw); err == nil {
		*f = FlexInt(n)
		return nil
	}
	if n, err := strconv.ParseFloat(raw, 64); err == nil {
		*f = FlexInt(int(n))
		return nil
	}
	return fmt.Errorf("valor numérico inválido: %s", raw)
}

// FlexBool acepta true/false, 1/0 o sus versiones en texto.
type FlexBool bool

func (f *FlexBool) UnmarshalJSON(data []byte) error {
	raw := strings.Trim(strings.TrimSpace(string(data)), `"`)
	switch strings.ToLower(raw) {
	case "true", "1", "yes", "on":
		*f = true
	case "false", "0", "no", "off", "", "null":
		*f = false
	default:
		return fmt.Errorf("valor booleano inválido: %s", raw)
	}
	return nil
}

type Notify struct {
	Connect        bool `json:"connect"`
	Disconnect     bool `json:"disconnect"`
	GameStart      bool `json:"gameStart"`
	GameChange     bool `json:"gameChange"`
	LeaveGame      bool `json:"leaveGame"`
	Studio         bool `json:"studio"`
	StatusChange   bool `json:"statusChange"`
	StartupSummary bool `json:"startupSummary"`
}

type StatusPanel struct {
	Enabled bool `json:"enabled"`
}

type Config struct {
	WebhookURL           string      `json:"webhookUrl"`
	WebhookUsername      string      `json:"webhookUsername"`
	WebhookAvatarURL     string      `json:"webhookAvatarUrl"`
	PollIntervalSeconds  FlexInt     `json:"pollIntervalSeconds"`
	RequestTimeoutMs     FlexInt     `json:"requestTimeoutMs"`
	WebhookMinIntervalMs FlexInt     `json:"webhookMinIntervalMs"`
	Mention              string      `json:"mention"`
	RobloxCookie         string      `json:"robloxCookie"`
	TimeZone             string      `json:"timeZone"`
	LogLevel             string      `json:"logLevel"`
	DryRun               FlexBool    `json:"dryRun"`
	StatusPanel          StatusPanel `json:"statusPanel"`
	Notify               Notify      `json:"notify"`
}

func Default() Config {
	return Config{
		WebhookURL:           "",
		WebhookUsername:      "",
		WebhookAvatarURL:     "",
		PollIntervalSeconds:  30,
		RequestTimeoutMs:     15000,
		WebhookMinIntervalMs: 600,
		Mention:              "",
		RobloxCookie:         "",
		TimeZone:             "",
		LogLevel:             "info",
		DryRun:               false,
		StatusPanel:          StatusPanel{Enabled: true},
		Notify: Notify{
			Connect:        true,
			Disconnect:     true,
			GameStart:      true,
			GameChange:     true,
			LeaveGame:      true,
			Studio:         true,
			StatusChange:   true,
			StartupSummary: false,
		},
	}
}

// Load lee .env, config.json, config.local.json y las variables de entorno.
func Load(rootDir string) (*Config, error) {
	loadEnvFile(filepath.Join(rootDir, ".env"))

	cfg := Default()
	if err := mergeFile(filepath.Join(rootDir, "config.json"), &cfg); err != nil {
		return nil, err
	}
	if err := mergeFile(filepath.Join(rootDir, "config.local.json"), &cfg); err != nil {
		return nil, err
	}

	if value := os.Getenv("DISCORD_WEBHOOK_URL"); value != "" {
		cfg.WebhookURL = value
	}
	if value := os.Getenv("ROBLOSECURITY"); value != "" {
		cfg.RobloxCookie = value
	}
	if value := os.Getenv("POLL_INTERVAL_SECONDS"); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			cfg.PollIntervalSeconds = FlexInt(n)
		}
	}
	if value := os.Getenv("TIME_ZONE"); value != "" {
		cfg.TimeZone = value
	}
	if value := os.Getenv("LOG_LEVEL"); value != "" {
		cfg.LogLevel = value
	}
	if os.Getenv("DRY_RUN") == "1" {
		cfg.DryRun = true
	}

	cfg.WebhookURL = strings.TrimSpace(cfg.WebhookURL)
	cfg.WebhookUsername = strings.TrimSpace(cfg.WebhookUsername)
	cfg.WebhookAvatarURL = strings.TrimSpace(cfg.WebhookAvatarURL)
	cfg.Mention = strings.TrimSpace(cfg.Mention)
	cfg.RobloxCookie = NormalizeCookie(cfg.RobloxCookie)
	cfg.TimeZone = strings.TrimSpace(cfg.TimeZone)
	cfg.PollIntervalSeconds = FlexInt(clamp(int(cfg.PollIntervalSeconds), 10, 3600, 30))
	cfg.RequestTimeoutMs = FlexInt(clamp(int(cfg.RequestTimeoutMs), 3000, 60000, 15000))
	cfg.WebhookMinIntervalMs = FlexInt(clamp(int(cfg.WebhookMinIntervalMs), 250, 5000, 600))

	switch strings.ToLower(cfg.LogLevel) {
	case "debug", "info", "warn", "error":
		cfg.LogLevel = strings.ToLower(cfg.LogLevel)
	default:
		cfg.LogLevel = "info"
	}

	return &cfg, nil
}

func mergeFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("no se pudo leer %s: %w", path, err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("no se pudo interpretar %s: %w", path, err)
	}
	return nil
}

// LoadEnvFile carga .env sin sobrescribir variables reales del sistema.
func loadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		separator := strings.Index(trimmed, "=")
		if separator <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:separator])
		value := strings.TrimSpace(trimmed[separator+1:])
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}

func clamp(value, min, max, _ int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func NormalizeCookie(value string) string {
	cookie := strings.TrimSpace(value)
	if cookie == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(cookie), ".roblosecurity=") {
		cookie = cookie[len(".roblosecurity="):]
	}
	if len(cookie) >= 2 {
		if (cookie[0] == '"' && cookie[len(cookie)-1] == '"') ||
			(cookie[0] == '\'' && cookie[len(cookie)-1] == '\'') {
			cookie = cookie[1 : len(cookie)-1]
		}
	}
	return strings.TrimSpace(cookie)
}

func ValidateWebhookURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("el webhookUrl no es una URL válida")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("el webhookUrl debe usar http o https")
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com":
		return nil
	default:
		return errors.New("el webhookUrl no parece ser un webhook de Discord")
	}
}

func (c *Config) PollInterval() int       { return int(c.PollIntervalSeconds) }
func (c *Config) RequestTimeout() int     { return int(c.RequestTimeoutMs) }
func (c *Config) WebhookMinInterval() int { return int(c.WebhookMinIntervalMs) }
func (c *Config) IsDryRun() bool          { return bool(c.DryRun) }
