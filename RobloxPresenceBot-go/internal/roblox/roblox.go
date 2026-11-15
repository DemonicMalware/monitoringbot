package roblox

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"roblox-presence-bot/internal/httpclient"
	"roblox-presence-bot/internal/logger"
	"roblox-presence-bot/internal/model"
	"roblox-presence-bot/internal/presence"
)

const (
	presenceURL        = "https://presence.roblox.com/v1/presence/users"
	usernamesURL       = "https://users.roblox.com/v1/usernames/users"
	authenticatedURL   = "https://users.roblox.com/v1/users/authenticated"
	gamesURL           = "https://games.roblox.com/v1/games"
	gameIconsURL       = "https://thumbnails.roblox.com/v1/games/icons"
	userHeadshotsURL   = "https://thumbnails.roblox.com/v1/users/avatar-headshot"
	universeByPlaceURL = "https://apis.roblox.com/universes/v1/places/%d/universe"

	gameCacheTTL    = 6 * time.Hour
	placeCacheTTL   = 24 * time.Hour
	missingCacheTTL = 5 * time.Minute
	batchSize       = 50
)

type cacheEntry struct {
	value   any
	expires time.Time
}

// Client consulta las APIs públicas de Roblox. Reutiliza la cookie opcional
// para obtener nombres de juegos y servidores.
type Client struct {
	cookie    string
	timeout   time.Duration
	log       *logger.Logger
	csrfToken string

	mu                    sync.Mutex
	cache                 map[string]cacheEntry
	disableCookieNotified bool
}

func New(cookie string, timeout time.Duration, log *logger.Logger) *Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		cookie:  normalizeCookie(cookie),
		timeout: timeout,
		log:     log,
		cache:   make(map[string]cacheEntry),
	}
}

func normalizeCookie(value string) string {
	cookie := strings.TrimSpace(value)
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

func (c *Client) HasCookie() bool {
	return c.cookie != ""
}

func (c *Client) ClearCookie() {
	c.cookie = ""
	c.csrfToken = ""
}

func (c *Client) cookieHeader() map[string]string {
	if c.cookie == "" {
		return nil
	}
	return map[string]string{"cookie": ".ROBLOSECURITY=" + c.cookie}
}

func (c *Client) request(method, rawURL string, body any, headers map[string]string, retries int) (*httpclient.Response, error) {
	return httpclient.Request(rawURL, httpclient.Options{
		Method:     method,
		Headers:    headers,
		Body:       body,
		Timeout:    c.timeout,
		Retries:    retries,
		RetryDelay: 1200 * time.Millisecond,
		Logger:     c.log,
	})
}

func (c *Client) cacheGet(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expires) {
		delete(c.cache, key)
		return nil, false
	}
	return entry.value, true
}

func (c *Client) cacheSet(key string, value any, ttl time.Duration) {
	if value == nil && ttl > missingCacheTTL {
		ttl = missingCacheTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = cacheEntry{value: value, expires: time.Now().Add(ttl)}
}

func (c *Client) cached(key string, ttl time.Duration, loader func() (any, error)) (any, error) {
	if value, ok := c.cacheGet(key); ok {
		return value, nil
	}
	value, err := loader()
	if err != nil {
		return nil, err
	}
	c.cacheSet(key, value, ttl)
	return value, nil
}

type authenticatedUser struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// ValidateCookie comprueba la cookie y devuelve la cuenta autenticada.
// Devuelve nil sin error si la cookie no existe o ya no sirve.
func (c *Client) ValidateCookie() (*model.ResolvedUser, error) {
	if c.cookie == "" {
		return nil, nil
	}

	response, err := c.request("GET", authenticatedURL, nil, c.cookieHeader(), 1)
	if err != nil {
		return nil, err
	}
	if response.Status == 401 || response.Status == 403 {
		return nil, nil
	}
	if !response.OK {
		return nil, fmt.Errorf("No se pudo validar la cookie (HTTP %d).", response.Status)
	}

	var user authenticatedUser
	if strings.TrimSpace(response.Text) != "" {
		if err := json.Unmarshal([]byte(response.Text), &user); err != nil {
			return nil, nil
		}
	}
	if user.ID == 0 && user.Name == "" {
		return nil, nil
	}
	display := user.DisplayName
	if display == "" {
		display = user.Name
	}
	return &model.ResolvedUser{ID: user.ID, Name: user.Name, DisplayName: display}, nil
}

// ResolveUsernames convierte nombres en IDs. La clave del mapa es el nombre
// solicitado en minúsculas.
func (c *Client) ResolveUsernames(usernames []string) (map[string]model.ResolvedUser, error) {
	unique := make([]string, 0, len(usernames))
	seen := make(map[string]struct{}, len(usernames))
	for _, username := range usernames {
		username = strings.TrimSpace(username)
		if username == "" {
			continue
		}
		key := strings.ToLower(username)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, username)
	}

	result := make(map[string]model.ResolvedUser, len(unique))
	for start := 0; start < len(unique); start += batchSize {
		end := start + batchSize
		if end > len(unique) {
			end = len(unique)
		}
		response, err := c.request("POST", usernamesURL, map[string]any{
			"usernames":          unique[start:end],
			"excludeBannedUsers": false,
		}, nil, 2)
		if err != nil {
			return nil, err
		}
		if !response.OK {
			text := response.Text
			if len(text) > 200 {
				text = text[:200]
			}
			return nil, fmt.Errorf("No se pudieron resolver los usuarios (HTTP %d): %s", response.Status, text)
		}

		var body struct {
			Data []struct {
				RequestedUsername string `json:"requestedUsername"`
				ID                int64  `json:"id"`
				Name              string `json:"name"`
				DisplayName       string `json:"displayName"`
				HasVerifiedBadge  bool   `json:"hasVerifiedBadge"`
			} `json:"data"`
		}
		if strings.TrimSpace(response.Text) != "" {
			_ = json.Unmarshal([]byte(response.Text), &body)
		}
		for _, user := range body.Data {
			display := user.DisplayName
			if display == "" {
				display = user.Name
			}
			result[strings.ToLower(user.RequestedUsername)] = model.ResolvedUser{
				ID:               user.ID,
				Name:             user.Name,
				DisplayName:      display,
				HasVerifiedBadge: user.HasVerifiedBadge,
			}
		}
	}
	return result, nil
}

// GetPresences consulta la presencia de varias cuentas en lotes de 50.
func (c *Client) GetPresences(userIDs []int64) ([]presence.RawPresence, error) {
	unique := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}

	presences := make([]presence.RawPresence, 0, len(unique))
	for start := 0; start < len(unique); start += batchSize {
		end := start + batchSize
		if end > len(unique) {
			end = len(unique)
		}
		batch, err := c.getPresenceBatch(unique[start:end])
		if err != nil {
			return nil, err
		}
		presences = append(presences, batch...)
		if len(unique) > batchSize {
			time.Sleep(300 * time.Millisecond)
		}
	}
	return presences, nil
}

func (c *Client) getPresenceBatch(userIDs []int64) ([]presence.RawPresence, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}

	for attempt := 0; attempt < 3; attempt++ {
		headers := c.cookieHeader()
		if headers == nil {
			headers = map[string]string{}
		}
		if c.csrfToken != "" {
			headers["x-csrf-token"] = c.csrfToken
		}

		response, err := c.request("POST", presenceURL, map[string]any{"userIds": userIDs}, headers, 2)
		if err != nil {
			return nil, err
		}

		if response.Status == 400 &&
			strings.Contains(strings.ToLower(response.Text), "too many user ids") &&
			len(userIDs) > 1 {
			middle := (len(userIDs) + 1) / 2
			first, err := c.getPresenceBatch(userIDs[:middle])
			if err != nil {
				return nil, err
			}
			second, err := c.getPresenceBatch(userIDs[middle:])
			if err != nil {
				return nil, err
			}
			return append(first, second...), nil
		}

		// Roblox pide resolver el token CSRF cuando se usa cookie en un POST.
		if response.Status == 403 {
			token := response.Header.Get("x-csrf-token")
			if token != "" && token != c.csrfToken {
				c.csrfToken = token
				continue
			}
		}

		// Cookie caducada: seguimos sin ella para no detener el monitor.
		if (response.Status == 401 || response.Status == 403) && c.cookie != "" {
			if !c.disableCookieNotified {
				if c.log != nil {
					c.log.Warnf("La cookie .ROBLOSECURITY no es válida o caducó. Se continuará sin cookie: no se verán nombres de juegos ni servidores.")
				}
				c.disableCookieNotified = true
			}
			c.cookie = ""
			c.csrfToken = ""
			continue
		}

		if !response.OK {
			text := response.Text
			if len(text) > 300 {
				text = text[:300]
			}
			return nil, fmt.Errorf("Presence API respondió HTTP %d: %s", response.Status, text)
		}

		var body struct {
			UserPresences []presence.RawPresence `json:"userPresences"`
		}
		if strings.TrimSpace(response.Text) != "" {
			_ = json.Unmarshal([]byte(response.Text), &body)
		}
		return body.UserPresences, nil
	}

	return nil, fmt.Errorf("No se pudo obtener la presencia después de varios intentos")
}

func (c *Client) getUniverseID(placeID int64) (*int64, error) {
	key := fmt.Sprintf("place:%d", placeID)
	value, err := c.cached(key, placeCacheTTL, func() (any, error) {
		response, err := c.request("GET", fmt.Sprintf(universeByPlaceURL, placeID), nil, nil, 1)
		if err != nil {
			return nil, err
		}
		if !response.OK {
			return nil, nil
		}
		var body struct {
			UniverseID *int64 `json:"universeId"`
		}
		if strings.TrimSpace(response.Text) != "" {
			_ = json.Unmarshal([]byte(response.Text), &body)
		}
		if body.UniverseID == nil || *body.UniverseID <= 0 {
			return nil, nil
		}
		return body.UniverseID, nil
	})
	if err != nil || value == nil {
		return nil, err
	}
	id, _ := value.(*int64)
	return id, nil
}

type gameDetails struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	RootPlaceID *int64 `json:"rootPlaceId"`
}

// GetGameByUniverse devuelve los datos del juego y su icono.
func (c *Client) GetGameByUniverse(universeID int64, fallbackPlaceID *int64) (*model.Game, error) {
	if universeID <= 0 {
		return nil, nil
	}

	key := fmt.Sprintf("game:%d", universeID)
	value, err := c.cached(key, gameCacheTTL, func() (any, error) {
		var details *gameDetails
		var iconURL string
		var wait sync.WaitGroup
		wait.Add(2)

		go func() {
			defer wait.Done()
			rawURL := fmt.Sprintf("%s?universeIds=%s", gamesURL, url.QueryEscape(fmt.Sprintf("%d", universeID)))
			response, err := c.request("GET", rawURL, nil, nil, 1)
			if err != nil || !response.OK {
				return
			}
			var body struct {
				Data []gameDetails `json:"data"`
			}
			if strings.TrimSpace(response.Text) != "" {
				_ = json.Unmarshal([]byte(response.Text), &body)
			}
			if len(body.Data) > 0 {
				found := body.Data[0]
				details = &found
			}
		}()

		go func() {
			defer wait.Done()
			rawURL := fmt.Sprintf(
				"%s?universeIds=%s&size=512x512&format=Png",
				gameIconsURL,
				url.QueryEscape(fmt.Sprintf("%d", universeID)),
			)
			response, err := c.request("GET", rawURL, nil, nil, 1)
			if err != nil || !response.OK {
				return
			}
			var body struct {
				Data []struct {
					ImageURL string `json:"imageUrl"`
				} `json:"data"`
			}
			if strings.TrimSpace(response.Text) != "" {
				_ = json.Unmarshal([]byte(response.Text), &body)
			}
			if len(body.Data) > 0 {
				iconURL = body.Data[0].ImageURL
			}
		}()

		wait.Wait()

		if details == nil && iconURL == "" {
			return nil, nil
		}

		rootPlaceID := fallbackPlaceID
		if details != nil && details.RootPlaceID != nil {
			rootPlaceID = details.RootPlaceID
		}
		name := ""
		if details != nil {
			name = details.Name
		}
		game := &model.Game{
			Name:        name,
			UniverseID:  model.Ptr(universeID),
			RootPlaceID: cloneInt64(rootPlaceID),
			IconURL:     iconURL,
		}
		if rootPlaceID != nil {
			game.URL = fmt.Sprintf("https://www.roblox.com/games/%d", *rootPlaceID)
		}
		return game, nil
	})
	if err != nil || value == nil {
		return nil, err
	}
	game, _ := value.(*model.Game)
	return game, nil
}

// GetPlaceDetails resuelve primero el universo del lugar.
func (c *Client) GetPlaceDetails(placeID int64) (*model.Game, error) {
	if placeID <= 0 {
		return nil, nil
	}
	universeID, err := c.getUniverseID(placeID)
	if err != nil {
		return nil, err
	}
	if universeID == nil {
		return &model.Game{
			RootPlaceID: model.Ptr(placeID),
			URL:         fmt.Sprintf("https://www.roblox.com/games/%d", placeID),
		}, nil
	}
	return c.GetGameByUniverse(*universeID, &placeID)
}

// GetAvatarURL devuelve la URL del avatar de una cuenta.
func (c *Client) GetAvatarURL(userID int64) (string, error) {
	if userID <= 0 {
		return "", nil
	}

	key := fmt.Sprintf("avatar:%d", userID)
	value, err := c.cached(key, gameCacheTTL, func() (any, error) {
		rawURL := fmt.Sprintf(
			"%s?userIds=%s&size=420x420&format=Png",
			userHeadshotsURL,
			url.QueryEscape(fmt.Sprintf("%d", userID)),
		)
		response, err := c.request("GET", rawURL, nil, nil, 1)
		if err != nil || !response.OK {
			return "", nil
		}
		var body struct {
			Data []struct {
				ImageURL string `json:"imageUrl"`
			} `json:"data"`
		}
		if strings.TrimSpace(response.Text) != "" {
			_ = json.Unmarshal([]byte(response.Text), &body)
		}
		if len(body.Data) == 0 {
			return "", nil
		}
		return body.Data[0].ImageURL, nil
	})
	if err != nil {
		return "", err
	}
	avatar, _ := value.(string)
	return avatar, nil
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
