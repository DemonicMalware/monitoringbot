package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	robloxUsersURL    = "https://users.roblox.com/v1/usernames/users"
	robloxPresenceURL = "https://presence.roblox.com/v1/presence/users"
)

// ResolvedUser es la respuesta de Roblox al resolver un nombre de usuario.
type ResolvedUser struct {
	RequestedUsername string `json:"requestedUsername"`
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	DisplayName       string `json:"displayName"`
}

// Presence es el estado de una cuenta en Roblox.
type Presence struct {
	UserID           int64  `json:"userId"`
	UserPresenceType int    `json:"userPresenceType"`
	LastLocation     string `json:"lastLocation"`
	PlaceID          int64  `json:"placeId"`
	RootPlaceID      int64  `json:"rootPlaceId"`
	GameID           string `json:"gameId"`
	UniverseID       int64  `json:"universeId"`
	LastOnline       string `json:"lastOnline"`
	LastSeen         string `json:"lastSeen"`
}

// RobloxClient consulta las APIs públicas de Roblox con reintentos.
type RobloxClient struct {
	HTTPClient  *http.Client
	UsersURL    string
	PresenceURL string
	Cookie      string
	UserAgent   string
	RetryBase   time.Duration
	MaxAttempts int
}

func NewRobloxClient(cookie string) *RobloxClient {
	return &RobloxClient{
		HTTPClient: &http.Client{
			Timeout: 20 * time.Second,
		},
		UsersURL:    robloxUsersURL,
		PresenceURL: robloxPresenceURL,
		Cookie:      cookie,
		UserAgent:   "RobloxPresenceSimple/1.0",
		RetryBase:   800 * time.Millisecond,
		MaxAttempts: 3,
	}
}

// Resolve convierte nombres de usuario en IDs. Acepta lotes de hasta 50 nombres.
func (c *RobloxClient) Resolve(ctx context.Context, usernames []string) ([]ResolvedUser, error) {
	if len(usernames) == 0 {
		return nil, nil
	}
	var result []ResolvedUser
	for start := 0; start < len(usernames); start += 50 {
		end := start + 50
		if end > len(usernames) {
			end = len(usernames)
		}
		batch := usernames[start:end]
		payload := map[string]any{
			"usernames":          batch,
			"excludeBannedUsers": false,
		}
		var response struct {
			Data []ResolvedUser `json:"data"`
		}
		if err := c.postJSON(ctx, c.UsersURL, payload, &response); err != nil {
			return nil, err
		}
		result = append(result, response.Data...)
	}
	return result, nil
}

// FetchPresence consulta el estado actual de varias cuentas en una sola llamada.
func (c *RobloxClient) FetchPresence(ctx context.Context, userIDs []int64) (map[int64]Presence, error) {
	result := make(map[int64]Presence, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	payload := map[string]any{"userIds": userIDs}
	var response struct {
		UserPresences []Presence `json:"userPresences"`
	}
	if err := c.postJSON(ctx, c.PresenceURL, payload, &response); err != nil {
		return nil, err
	}
	for _, presence := range response.UserPresences {
		if presence.UserID != 0 {
			result[presence.UserID] = presence
		}
	}
	return result, nil
}

func (c *RobloxClient) postJSON(ctx context.Context, endpoint string, payload any, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("no se pudo preparar la consulta a Roblox: %w", err)
	}

	attempts := c.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("no se pudo crear la consulta a Roblox: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.UserAgent)
		if c.Cookie != "" {
			req.Header.Set("Cookie", c.Cookie)
		}

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < attempts {
				if err := sleepContext(ctx, c.retryDelay(attempt)); err != nil {
					return err
				}
				continue
			}
			break
		}

		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("no se pudo leer la respuesta de Roblox: %w", readErr)
			if attempt < attempts {
				if err := sleepContext(ctx, c.retryDelay(attempt)); err != nil {
					return err
				}
				continue
			}
			break
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if err := json.Unmarshal(responseBody, target); err != nil {
				return fmt.Errorf("Roblox devolvió una respuesta inesperada: %w", err)
			}
			return nil
		}

		apiErr := fmt.Errorf("Roblox respondió %d: %s", resp.StatusCode, truncateText(string(responseBody), 300))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = apiErr
			if attempt < attempts {
				delay := c.retryDelay(attempt)
				if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After")); retryAfter > 0 {
					delay = retryAfter
				}
				if err := sleepContext(ctx, delay); err != nil {
					return err
				}
				continue
			}
			break
		}
		return apiErr
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("error desconocido")
	}
	return fmt.Errorf("no se pudo consultar Roblox: %w", lastErr)
}

func (c *RobloxClient) retryDelay(attempt int) time.Duration {
	base := c.RetryBase
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

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		delay := time.Duration(seconds * float64(time.Second))
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		return delay
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := time.Until(when)
		if delay > 0 && delay < 30*time.Second {
			return delay
		}
	}
	return 0
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func truncateText(value string, maxRunes int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}
