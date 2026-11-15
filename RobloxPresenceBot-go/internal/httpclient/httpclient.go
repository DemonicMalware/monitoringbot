package httpclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"roblox-presence-bot/internal/logger"
)

const userAgent = "RobloxPresenceBot/1.0 (Go)"

type Response struct {
	Status int
	OK     bool
	Header http.Header
	Text   string
	Data   any
}

type Options struct {
	Method     string
	Headers    map[string]string
	Body       any
	Timeout    time.Duration
	Retries    int
	RetryDelay time.Duration
	Logger     *logger.Logger
}

type HTTPError struct {
	URL    string
	Status int
	Body   string
	Err    error
}

func (e *HTTPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("no se pudo completar la petición a %s: %v", e.URL, e.Err)
	}
	return fmt.Sprintf("no se pudo completar la petición a %s (HTTP %d)", e.URL, e.Status)
}

func (e *HTTPError) Unwrap() error { return e.Err }

// Request hace una petición con timeout, reintentos y respeto de Retry-After.
// No devuelve error por respuestas 4xx/5xx: el llamador decide qué hacer.
func Request(rawURL string, options Options) (*Response, error) {
	method := options.Method
	if method == "" {
		method = http.MethodGet
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	retries := options.Retries
	if retries < 0 {
		retries = 0
	}
	retryDelay := options.RetryDelay
	if retryDelay <= 0 {
		retryDelay = 1200 * time.Millisecond
	}

	var bodyBytes []byte
	hasBody := options.Body != nil
	if hasBody {
		switch value := options.Body.(type) {
		case []byte:
			bodyBytes = value
		case string:
			bodyBytes = []byte(value)
		default:
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("no se pudo serializar el cuerpo: %w", err)
			}
			bodyBytes = encoded
		}
	}

	maxAttempts := retries + 1
	var lastErr error
	var lastResponse *Response

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		request, err := http.NewRequest(method, rawURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", userAgent)
		request.Header.Set("Accept", "application/json")
		if hasBody {
			request.Header.Set("Content-Type", "application/json")
		}
		for key, value := range options.Headers {
			request.Header.Set(key, value)
		}

		client := &http.Client{Timeout: timeout}
		response, err := client.Do(request)
		if err != nil {
			lastErr = err
			if attempt >= maxAttempts {
				break
			}
			wait := backoff(retryDelay, attempt)
			logWarn(options.Logger, "Error de red (%v). Reintentando en %s...", err, wait.Round(time.Second))
			time.Sleep(wait)
			continue
		}

		text, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt >= maxAttempts {
				break
			}
			wait := backoff(retryDelay, attempt)
			logWarn(options.Logger, "Error leyendo la respuesta (%v). Reintentando en %s...", readErr, wait.Round(time.Second))
			time.Sleep(wait)
			continue
		}

		parsed := parseJSON(text)
		result := &Response{
			Status: response.StatusCode,
			OK:     response.StatusCode >= 200 && response.StatusCode < 300,
			Header: response.Header,
			Text:   string(text),
			Data:   parsed,
		}
		lastResponse = result

		if response.StatusCode == http.StatusTooManyRequests && attempt < maxAttempts {
			retryAfter := retryAfterSeconds(response.Header.Get("Retry-After"), parsed)
			if retryAfter <= 0 {
				retryAfter = 2 * time.Second
			}
			logWarn(options.Logger, "Límite de peticiones (429). Reintentando en %s...", retryAfter.Round(time.Second))
			time.Sleep(retryAfter + 250*time.Millisecond)
			continue
		}

		if response.StatusCode >= 500 && attempt < maxAttempts {
			wait := backoff(retryDelay, attempt)
			logWarn(options.Logger, "Servidor respondió %d. Reintentando en %s...", response.StatusCode, wait.Round(time.Second))
			time.Sleep(wait)
			continue
		}

		return result, nil
	}

	return lastResponse, &HTTPError{URL: rawURL, Err: lastErr}
}

func parseJSON(data []byte) any {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil
	}
	return value
}

func retryAfterSeconds(header string, data any) time.Duration {
	if header != "" {
		if seconds, err := strconv.ParseFloat(strings.TrimSpace(header), 64); err == nil && seconds > 0 {
			return time.Duration(seconds * float64(time.Second))
		}
	}
	if object, ok := data.(map[string]any); ok {
		if value, ok := object["retry_after"]; ok {
			switch number := value.(type) {
			case float64:
				if number > 0 {
					return time.Duration(number * float64(time.Second))
				}
			case string:
				if seconds, err := strconv.ParseFloat(number, 64); err == nil && seconds > 0 {
					return time.Duration(seconds * float64(time.Second))
				}
			}
		}
	}
	return 0
}

func logWarn(log *logger.Logger, format string, args ...any) {
	if log != nil {
		log.Warnf(format, args...)
	}
}

func backoff(base time.Duration, attempt int) time.Duration {
	multiplier := 1 << (attempt - 1)
	jitter := time.Duration(rand.Intn(300)) * time.Millisecond
	return time.Duration(multiplier)*base + jitter
}
