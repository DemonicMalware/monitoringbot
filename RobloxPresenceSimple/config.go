package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata"
)

const (
	defaultPollInterval = 10
	minPollInterval     = 5
)

// NotifyConfig controla qué avisos se envían a Discord.
type NotifyConfig struct {
	Connect        bool `json:"connect"`
	Disconnect     bool `json:"disconnect"`
	GameStart      bool `json:"gameStart"`
	GameChange     bool `json:"gameChange"`
	LeaveGame      bool `json:"leaveGame"`
	StartupSummary bool `json:"startupSummary"`
}

// Config es la configuración del monitor.
type Config struct {
	Usernames           []string     `json:"usernames"`
	PollIntervalSeconds int          `json:"pollIntervalSeconds"`
	TimeZone            string       `json:"timeZone"`
	TimeZoneLabel       string       `json:"timeZoneLabel"`
	StateFile           string       `json:"stateFile"`
	WebhookURL          string       `json:"webhookUrl"`
	RobloxCookie        string       `json:"robloxCookie"`
	Port                string       `json:"port"`
	DryRun              bool         `json:"dryRun"`
	Notify              NotifyConfig `json:"notify"`

	rootDir string
	loc     *time.Location
}

func defaultConfig() *Config {
	return &Config{
		Usernames:           []string{"agustin563554", "agusstinn2801"},
		PollIntervalSeconds: defaultPollInterval,
		TimeZone:            "America/Puerto_Rico",
		TimeZoneLabel:       "hora Puerto Rico",
		StateFile:           "data/simple-state.json",
		Port:                "8080",
		Notify: NotifyConfig{
			Connect:        true,
			Disconnect:     true,
			GameStart:      true,
			GameChange:     true,
			LeaveGame:      false,
			StartupSummary: true,
		},
	}
}

// LoadConfig lee config.json, .env y las variables de entorno.
// Las variables de entorno tienen prioridad sobre el archivo.
func LoadConfig(configPath string) (*Config, error) {
	cfg := defaultConfig()

	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		configPath = "config.json"
	}
	if abs, err := filepath.Abs(configPath); err == nil {
		cfg.rootDir = filepath.Dir(abs)
	} else {
		cfg.rootDir = filepath.Dir(configPath)
	}

	data, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("el archivo %s no es un JSON válido: %w", configPath, err)
		}
	case os.IsNotExist(err):
		// Se permite arrancar solo con variables de entorno.
	default:
		return nil, fmt.Errorf("no se pudo leer %s: %w", configPath, err)
	}

	loadEnvFile(filepath.Join(cfg.rootDir, ".env"))
	if cwd, err := os.Getwd(); err == nil {
		cwdEnv := filepath.Join(cwd, ".env")
		if filepath.Clean(cwdEnv) != filepath.Clean(filepath.Join(cfg.rootDir, ".env")) {
			loadEnvFile(cwdEnv)
		}
	}

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	if err := cfg.finalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) error {
	if value := cleanSecret(os.Getenv("DISCORD_WEBHOOK_URL")); value != "" {
		cfg.WebhookURL = value
	}
	if value := cleanSecret(os.Getenv("ROBLOSECURITY")); value != "" {
		cfg.RobloxCookie = value
	}
	if value := strings.TrimSpace(os.Getenv("USERNAMES")); value != "" {
		cfg.Usernames = splitUsernames(value)
	} else if value := strings.TrimSpace(os.Getenv("ROBLOX_USERNAMES")); value != "" {
		cfg.Usernames = splitUsernames(value)
	}
	if value := strings.TrimSpace(os.Getenv("POLL_INTERVAL_SECONDS")); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("POLL_INTERVAL_SECONDS debe ser un número entero: %q", value)
		}
		cfg.PollIntervalSeconds = seconds
	}
	if value := strings.TrimSpace(os.Getenv("TIME_ZONE")); value != "" {
		cfg.TimeZone = value
	}
	if value := strings.TrimSpace(os.Getenv("TIME_ZONE_LABEL")); value != "" {
		cfg.TimeZoneLabel = value
	}
	if value := strings.TrimSpace(os.Getenv("STATE_FILE")); value != "" {
		cfg.StateFile = value
	}
	if value := strings.TrimSpace(os.Getenv("PORT")); value != "" {
		cfg.Port = value
	}
	if value := strings.TrimSpace(os.Getenv("DRY_RUN")); value != "" {
		enabled, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("DRY_RUN debe ser 1, true, yes o no: %q", value)
		}
		cfg.DryRun = enabled
	}
	return nil
}

func (c *Config) finalize() error {
	usernames := make([]string, 0, len(c.Usernames))
	seen := make(map[string]struct{}, len(c.Usernames))
	for _, raw := range c.Usernames {
		name := strings.TrimSpace(raw)
		name = strings.TrimPrefix(name, "@")
		if name == "" {
			continue
		}
		if strings.IndexFunc(name, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ','
		}) >= 0 {
			return fmt.Errorf("el usuario %q contiene espacios o comas; usa un nombre por entrada", raw)
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		usernames = append(usernames, name)
	}
	if len(usernames) == 0 {
		return fmt.Errorf("no hay usuarios configurados; añade nombres en \"usernames\" o en la variable USERNAMES")
	}
	c.Usernames = usernames

	if c.PollIntervalSeconds == 0 {
		c.PollIntervalSeconds = defaultPollInterval
	}
	if c.PollIntervalSeconds < minPollInterval {
		return fmt.Errorf("pollIntervalSeconds debe ser como mínimo %d; Roblox limita las consultas demasiado frecuentes", minPollInterval)
	}

	c.TimeZone = strings.TrimSpace(c.TimeZone)
	if c.TimeZone == "" {
		c.TimeZone = "America/Puerto_Rico"
	}
	loc, err := time.LoadLocation(c.TimeZone)
	if err != nil {
		return fmt.Errorf("timeZone inválida %q: %w", c.TimeZone, err)
	}
	c.loc = loc

	c.TimeZoneLabel = strings.TrimSpace(c.TimeZoneLabel)
	if c.TimeZoneLabel == "" {
		c.TimeZoneLabel = c.TimeZone
	}
	if strings.ContainsAny(c.TimeZoneLabel, "\r\n") {
		return fmt.Errorf("timeZoneLabel no puede contener saltos de línea")
	}

	c.StateFile = strings.TrimSpace(c.StateFile)
	if c.StateFile == "" {
		c.StateFile = "data/simple-state.json"
	}

	c.Port = strings.TrimSpace(c.Port)
	if c.Port == "" {
		c.Port = "8080"
	}
	c.Port = strings.TrimPrefix(c.Port, ":")
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("port inválido %q; usa un número entre 1 y 65535", c.Port)
	}

	c.WebhookURL = cleanSecret(c.WebhookURL)
	if c.WebhookURL != "" && !validWebhookURL(c.WebhookURL) {
		return fmt.Errorf("DISCORD_WEBHOOK_URL no parece una URL de webhook de Discord válida")
	}
	c.RobloxCookie = normalizeCookie(c.RobloxCookie)
	return nil
}

func (c *Config) PollInterval() time.Duration {
	return time.Duration(c.PollIntervalSeconds) * time.Second
}

func (c *Config) Location() *time.Location {
	if c.loc != nil {
		return c.loc
	}
	loc, err := time.LoadLocation(c.TimeZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// StatePath devuelve la ruta del estado, relativa a la carpeta del config.json.
func (c *Config) StatePath() string {
	if filepath.IsAbs(c.StateFile) {
		return c.StateFile
	}
	if c.rootDir == "" {
		return c.StateFile
	}
	return filepath.Join(c.rootDir, c.StateFile)
}

func splitUsernames(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func validWebhookURL(raw string) bool {
	if strings.ContainsAny(raw, "\r\n\t ") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com":
	default:
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "webhooks" {
		return false
	}
	if _, err := strconv.ParseUint(parts[2], 10, 64); err != nil {
		return false
	}
	return strings.TrimSpace(parts[3]) != ""
}

func normalizeCookie(value string) string {
	value = cleanSecret(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, ".roblosecurity="):
		return ".ROBLOSECURITY=" + value[len(".roblosecurity="):]
	case strings.HasPrefix(lower, "roblosecurity="):
		return ".ROBLOSECURITY=" + value[len("roblosecurity="):]
	default:
		return ".ROBLOSECURITY=" + value
	}
}

func cleanSecret(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "\ufeff"))
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			value = strings.TrimSpace(value[1 : len(value)-1])
		}
	}
	return value
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "si", "sí", "on":
		return true, nil
	case "0", "false", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("valor booleano inválido")
	}
}

// loadEnvFile carga un .env sin sobrescribir variables ya definidas.
func loadEnvFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t") {
			continue
		}
		value = cleanSecret(value)
		if current, exists := os.LookupEnv(key); exists && strings.TrimSpace(current) != "" {
			continue
		}
		_ = os.Setenv(key, value)
	}
}
