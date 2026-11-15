package discord

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"roblox-presence-bot/internal/httpclient"
	"roblox-presence-bot/internal/logger"
)

// Error conserva el código HTTP para que el monitor pueda reaccionar a un 404
// (mensaje del panel borrado) sin depender del texto del error.
type Error struct {
	Status  int
	Body    any
	Message string
}

func (e *Error) Error() string {
	return e.Message
}

// Webhook envía y edita mensajes de Discord. Las operaciones se serializan y
// respetan el intervalo mínimo entre peticiones.
type Webhook struct {
	url         string
	username    string
	avatarURL   string
	minInterval time.Duration
	timeout     time.Duration
	log         *logger.Logger
	dryRun      bool

	mu          sync.Mutex
	lastRequest time.Time
}

func New(rawURL, username, avatarURL string, minInterval, timeout time.Duration, log *logger.Logger, dryRun bool) *Webhook {
	if minInterval < 0 {
		minInterval = 0
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Webhook{
		url:         rawURL,
		username:    username,
		avatarURL:   avatarURL,
		minInterval: minInterval,
		timeout:     timeout,
		log:         log,
		dryRun:      dryRun,
	}
}

// Validate comprueba que el webhook exista.
func (w *Webhook) Validate() (map[string]any, error) {
	if w.dryRun {
		return map[string]any{"name": "dry-run", "channel_id": "dry-run"}, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	response, err := httpclient.Request(w.url, httpclient.Options{
		Timeout: w.timeout,
		Retries: 1,
		Logger:  w.log,
	})
	if err != nil {
		return nil, err
	}
	if response.Status == 401 || response.Status == 404 {
		return nil, fmt.Errorf("El webhook de Discord no existe o fue eliminado. Comprueba la URL en config.json.")
	}
	if !response.OK {
		return nil, fmt.Errorf("Discord respondió HTTP %d al validar el webhook.", response.Status)
	}
	if data, ok := response.Data.(map[string]any); ok {
		return data, nil
	}
	return map[string]any{}, nil
}

func (w *Webhook) Send(payload map[string]any, wait bool) (map[string]any, error) {
	return w.execute("POST", "", payload, wait)
}

// Edit modifica un mensaje ya publicado por este webhook (panel de estado).
func (w *Webhook) Edit(messageID string, payload map[string]any) (map[string]any, error) {
	return w.execute("PATCH", messageID, payload, false)
}

func (w *Webhook) execute(method, messageID string, payload map[string]any, wait bool) (map[string]any, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.lastRequest.IsZero() && w.minInterval > 0 {
		if remaining := w.minInterval - time.Since(w.lastRequest); remaining > 0 {
			time.Sleep(remaining)
		}
	}

	result, err := w.executeOne(method, messageID, payload, wait)
	w.lastRequest = time.Now()
	return result, err
}

func (w *Webhook) executeOne(method, messageID string, payload map[string]any, wait bool) (map[string]any, error) {
	prepared := make(map[string]any, len(payload)+2)
	for key, value := range payload {
		prepared[key] = value
	}
	if w.username != "" {
		prepared["username"] = w.username
	}
	if w.avatarURL != "" {
		prepared["avatar_url"] = w.avatarURL
	}

	isEdit := method == "PATCH"
	if w.dryRun {
		if isEdit {
			w.logf("[dry-run] Se editaría el mensaje %s:\n%s", messageID, prettyJSON(prepared))
			return map[string]any{"id": messageID, "channel_id": "dry-run", "dryRun": true}, nil
		}
		w.logf("[dry-run] Mensaje para Discord:\n%s", prettyJSON(prepared))
		return map[string]any{"id": "dry-run", "channel_id": "dry-run", "dryRun": true}, nil
	}

	target := w.url
	if isEdit {
		target = fmt.Sprintf("%s/messages/%s", w.url, url.PathEscape(messageID))
	} else if wait {
		target += "?wait=true"
	}

	for attempt := 1; attempt <= 4; attempt++ {
		response, err := httpclient.Request(target, httpclient.Options{
			Method:  method,
			Body:    prepared,
			Timeout: w.timeout,
			Retries: 0,
			Logger:  w.log,
		})
		if err != nil {
			return nil, err
		}

		if response.Status == 200 || response.Status == 204 {
			if data, ok := response.Data.(map[string]any); ok {
				return data, nil
			}
			return map[string]any{"id": messageID, "status": response.Status}, nil
		}

		if response.Status == 429 {
			seconds := retryAfterSeconds(response)
			w.logf("Discord limitó el envío (429). Esperando %ss...", formatSeconds(seconds))
			time.Sleep(time.Duration(seconds*float64(time.Second)) + 300*time.Millisecond)
			continue
		}

		if response.Status == 400 {
			if _, hasComponents := prepared["components"]; hasComponents {
				w.logf("Discord rechazó los botones del mensaje. Se reintentará sin botones.")
				delete(prepared, "components")
				continue
			}
		}

		if response.Status >= 500 && attempt < 4 {
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}

		return nil, &Error{
			Status: response.Status,
			Body:   response.Data,
			Message: fmt.Sprintf(
				"Discord respondió HTTP %d: %s",
				response.Status,
				shorten(response.Text, 400),
			),
		}
	}

	return nil, fmt.Errorf("No se pudo enviar el mensaje a Discord después de varios intentos")
}

// Flush espera a que termine cualquier envío en curso.
func (w *Webhook) Flush(timeout time.Duration) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if w.mu.TryLock() {
			w.mu.Unlock()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (w *Webhook) logf(format string, args ...any) {
	if w.log != nil {
		w.log.Infof(format, args...)
	}
}

func prettyJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func retryAfterSeconds(response *httpclient.Response) float64 {
	if response == nil {
		return 1
	}
	if header := strings.TrimSpace(response.Header.Get("Retry-After")); header != "" {
		if seconds, err := strconv.ParseFloat(header, 64); err == nil && seconds > 0 {
			return seconds
		}
	}
	if data, ok := response.Data.(map[string]any); ok {
		switch value := data["retry_after"].(type) {
		case float64:
			if value > 0 {
				return value
			}
		case string:
			if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds > 0 {
				return seconds
			}
		}
	}
	return 1
}

func formatSeconds(seconds float64) string {
	if seconds == float64(int64(seconds)) {
		return strconv.FormatInt(int64(seconds), 10)
	}
	return strconv.FormatFloat(seconds, 'f', -1, 64)
}

func shorten(text string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max])
}
