package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const discordMaxMessageRunes = 2000

// WebhookInfo es la información pública de un webhook de Discord.
type WebhookInfo struct {
	Name      string `json:"name"`
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id"`
}

// DiscordClient envía mensajes de texto planos a un webhook.
type DiscordClient struct {
	WebhookURL  string
	HTTPClient  *http.Client
	DryRun      bool
	Logger      *log.Logger
	RetryBase   time.Duration
	MaxAttempts int
}

func NewDiscordClient(webhookURL string, dryRun bool, logger *log.Logger) *DiscordClient {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &DiscordClient{
		WebhookURL: strings.TrimSpace(webhookURL),
		HTTPClient: &http.Client{
			Timeout: 20 * time.Second,
		},
		DryRun:      dryRun,
		Logger:      logger,
		RetryBase:   800 * time.Millisecond,
		MaxAttempts: 3,
	}
}

// Send publica un mensaje. Si DryRun está activo, solo lo muestra en consola.
func (d *DiscordClient) Send(ctx context.Context, content string) error {
	_, err := d.send(ctx, content, false)
	return err
}

// SendWait publica un mensaje y devuelve su ID para poder editarlo después.
func (d *DiscordClient) SendWait(ctx context.Context, content string) (string, error) {
	return d.send(ctx, content, true)
}

func (d *DiscordClient) send(ctx context.Context, content string, wait bool) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", nil
	}
	content = limitRunes(content, discordMaxMessageRunes)
	if d.DryRun {
		d.Logger.Printf("[dry-run] Mensaje que se enviaría a Discord:\n%s", content)
		return "", nil
	}
	if d.WebhookURL == "" {
		return "", fmt.Errorf("falta DISCORD_WEBHOOK_URL")
	}

	body, err := json.Marshal(map[string]any{
		"content": content,
		"allowed_mentions": map[string]any{
			"parse": []string{},
		},
	})
	if err != nil {
		return "", fmt.Errorf("no se pudo preparar el mensaje: %w", err)
	}

	endpoint := d.WebhookURL
	if wait {
		endpoint = withQuery(endpoint, "wait", "true")
	}
	responseBody, err := d.do(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return "", err
	}
	if !wait {
		return "", nil
	}

	var message struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &message); err != nil {
		return "", fmt.Errorf("Discord devolvió una respuesta inesperada: %w", err)
	}
	if strings.TrimSpace(message.ID) == "" {
		return "", fmt.Errorf("Discord no devolvió el ID del mensaje")
	}
	return message.ID, nil
}

// Edit actualiza un mensaje ya publicado por este webhook.
func (d *DiscordClient) Edit(ctx context.Context, messageID, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	content = limitRunes(content, discordMaxMessageRunes)
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return fmt.Errorf("falta el ID del mensaje a editar")
	}
	if d.DryRun {
		d.Logger.Printf("[dry-run] Mensaje que se editaría en Discord (%s):\n%s", messageID, content)
		return nil
	}
	if d.WebhookURL == "" {
		return fmt.Errorf("falta DISCORD_WEBHOOK_URL")
	}

	body, err := json.Marshal(map[string]any{
		"content": content,
		"allowed_mentions": map[string]any{
			"parse": []string{},
		},
	})
	if err != nil {
		return fmt.Errorf("no se pudo preparar el mensaje: %w", err)
	}

	endpoint, err := webhookMessageURL(d.WebhookURL, messageID)
	if err != nil {
		return err
	}
	_, err = d.do(ctx, http.MethodPatch, endpoint, body)
	return err
}

func webhookMessageURL(raw, messageID string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("la URL del webhook no es válida: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/messages/" + url.PathEscape(messageID)
	return u.String(), nil
}

// do ejecuta una petición a Discord con reintentos y devuelve el cuerpo.
func (d *DiscordClient) do(ctx context.Context, method, endpoint string, body []byte) ([]byte, error) {
	attempts := d.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
		if err != nil {
			return nil, fmt.Errorf("no se pudo crear la petición a Discord: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "RobloxPresenceSimple/1.0")

		resp, err := d.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < attempts {
				if err := sleepContext(ctx, d.retryDelay(attempt)); err != nil {
					return nil, err
				}
				continue
			}
			break
		}

		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < attempts {
				if err := sleepContext(ctx, d.retryDelay(attempt)); err != nil {
					return nil, err
				}
				continue
			}
			break
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return responseBody, nil
		}

		apiErr := discordStatusError(resp.StatusCode, responseBody)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = apiErr
			if attempt < attempts {
				delay := d.retryDelay(attempt)
				if retryAfter := discordRetryAfter(resp.Header.Get("Retry-After"), responseBody); retryAfter > 0 {
					delay = retryAfter
				}
				d.Logger.Printf("Discord limitó o falló la petición; reintentando en %s.", delay.Round(time.Millisecond))
				if err := sleepContext(ctx, delay); err != nil {
					return nil, err
				}
				continue
			}
			break
		}
		return nil, apiErr
	}

	if lastErr == nil {
		lastErr = errors.New("error desconocido")
	}
	return nil, fmt.Errorf("no se pudo completar la petición a Discord: %w", lastErr)
}

func withQuery(raw, key, value string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	query := u.Query()
	query.Set(key, value)
	u.RawQuery = query.Encode()
	return u.String()
}

// Info verifica el webhook y devuelve su nombre y canal.
func (d *DiscordClient) Info(ctx context.Context) (*WebhookInfo, error) {
	if d.WebhookURL == "" {
		return nil, fmt.Errorf("falta DISCORD_WEBHOOK_URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.WebhookURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "RobloxPresenceSimple/1.0")
	resp, err := d.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no se pudo contactar a Discord: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la respuesta de Discord: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, discordStatusError(resp.StatusCode, body)
	}
	var info WebhookInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("Discord devolvió una respuesta inesperada: %w", err)
	}
	return &info, nil
}

func (d *DiscordClient) retryDelay(attempt int) time.Duration {
	base := d.RetryBase
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	delay := base
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= 10*time.Second {
			return 10 * time.Second
		}
	}
	if delay > 10*time.Second {
		delay = 10 * time.Second
	}
	return delay
}

// DiscordHTTPError conserva el código HTTP para distinguir un webhook inválido
// de un fallo temporal de Discord.
type DiscordHTTPError struct {
	StatusCode int
	Message    string
}

func (e *DiscordHTTPError) Error() string {
	return e.Message
}

func discordStatusError(status int, body []byte) error {
	message := ""
	switch status {
	case http.StatusUnauthorized, http.StatusNotFound:
		message = fmt.Sprintf("el webhook de Discord no existe o fue eliminado (HTTP %d)", status)
	case http.StatusForbidden:
		message = fmt.Sprintf("Discord rechazó el webhook (HTTP %d)", status)
	case http.StatusBadRequest:
		message = fmt.Sprintf("Discord rechazó el mensaje (HTTP %d): %s", status, truncateText(string(body), 200))
	default:
		message = fmt.Sprintf("Discord respondió HTTP %d: %s", status, truncateText(string(body), 200))
	}
	return &DiscordHTTPError{StatusCode: status, Message: message}
}

func discordRetryAfter(header string, body []byte) time.Duration {
	if seconds, err := strconv.ParseFloat(strings.TrimSpace(header), 64); err == nil && seconds > 0 {
		return capDiscordDelay(seconds)
	}
	var payload struct {
		RetryAfter float64 `json:"retry_after"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.RetryAfter > 0 {
		return capDiscordDelay(payload.RetryAfter)
	}
	return 0
}

func capDiscordDelay(seconds float64) time.Duration {
	delay := time.Duration(seconds * float64(time.Second))
	if delay < 250*time.Millisecond {
		delay = 250 * time.Millisecond
	}
	if delay > 10*time.Second {
		delay = 10 * time.Second
	}
	return delay
}

func limitRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
