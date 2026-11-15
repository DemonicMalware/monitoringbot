// Roblox Presence Monitor -> Discord Webhook (Go, solo librería estándar)
//
// Monitorea cuentas de Roblox por username y avisa por webhook de Discord cuando:
//   - se conecta / se desconecta
//   - entra a jugar (con nombre del juego y link)
//   - cambia de juego, sale del juego o abre Roblox Studio
//
// Cada aviso incluye la fecha y hora exacta del cambio de estado.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // zona horaria embebida: no depende del sistema del hosting
)

// ───────────────────────── Configuración ─────────────────────────

type Config struct {
	WebhookURL    string
	Roblosecurity string
	Usernames     []string
	PollInterval  time.Duration
	SummaryEvery  time.Duration
	Loc           *time.Location
	StateFile     string
	BotName       string
}

var cfg = &Config{Loc: time.UTC, BotName: "Roblox Monitor", PollInterval: 30 * time.Second}

func envStr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return def
	}
	return v
}

// loadDotEnv carga KEY=VALUE de un archivo .env sin pisar variables ya definidas.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

func loadUsernames() []string {
	var names []string
	for _, u := range strings.Split(os.Getenv("ROBLOX_USERNAMES"), ",") {
		if u = strings.TrimSpace(u); u != "" {
			names = append(names, u)
		}
	}
	if len(names) == 0 {
		if data, err := os.ReadFile("usernames.txt"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					names = append(names, line)
				}
			}
		}
	}
	return names
}

func loadConfig() error {
	loc, err := time.LoadLocation(envStr("TIMEZONE", "America/Argentina/Buenos_Aires"))
	if err != nil {
		return fmt.Errorf("TIMEZONE inválida: %w", err)
	}
	poll := envInt("POLL_INTERVAL", 30)
	if poll < 15 {
		poll = 15
	}
	summary := envInt("SUMMARY_INTERVAL_MIN", 0)
	if summary < 0 {
		summary = 0
	}
	cfg = &Config{
		WebhookURL:    strings.TrimSpace(os.Getenv("DISCORD_WEBHOOK_URL")),
		Roblosecurity: strings.TrimSpace(os.Getenv("ROBLOSECURITY")),
		Usernames:     loadUsernames(),
		PollInterval:  time.Duration(poll) * time.Second,
		SummaryEvery:  time.Duration(summary) * time.Minute,
		Loc:           loc,
		StateFile:     envStr("STATE_FILE", "state.json"),
		BotName:       envStr("BOT_NAME", "Roblox Monitor"),
	}
	return nil
}

// ───────────────────────── Constantes y tipos ─────────────────────────

// Estados de presencia de Roblox.
const (
	Offline = 0
	Online  = 1
	InGame  = 2
	Studio  = 3
)

var colors = map[string]int{
	"green":  0x2ECC71,
	"red":    0xE74C3C,
	"blue":   0x3498DB,
	"orange": 0xF39C12,
	"yellow": 0xF1C40F,
	"purple": 0x9B59B6,
	"grey":   0x95A5A6,
}

// Endpoints (variables para poder apuntarlos a un servidor de prueba).
var (
	usersURL    = "https://users.roblox.com/v1/usernames/users"
	presenceURL = "https://presence.roblox.com/v1/presence/users"
	gamesURL    = "https://games.roblox.com/v1/games"
	thumbsURL   = "https://thumbnails.roblox.com/v1/users/avatar-headshot"
)

var errAuth = errors.New("Roblox devolvió 401 en la API de presencia")

type User struct {
	ID      int64
	Name    string
	Display string
}

type Presence struct {
	Type         int    `json:"userPresenceType"`
	LastLocation string `json:"lastLocation"`
	PlaceID      int64  `json:"placeId"`
	RootPlaceID  int64  `json:"rootPlaceId"`
	UniverseID   int64  `json:"universeId"`
	UserID       int64  `json:"userId"`
	LastOnline   string `json:"lastOnline"`
}

// Record es el estado guardado de cada cuenta (se persiste en state.json).
type Record struct {
	Status      int    `json:"status"`
	PlaceID     int64  `json:"place_id"`
	UniverseID  int64  `json:"universe_id"`
	Game        string `json:"game"`
	LastOnline  string `json:"last_online"`
	OnlineSince string `json:"online_since"`
	GameSince   string `json:"game_since"`
}

type Field struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type Embed struct {
	Title       string  `json:"title,omitempty"`
	URL         string  `json:"url,omitempty"`
	Description string  `json:"description,omitempty"`
	Color       int     `json:"color,omitempty"`
	Fields      []Field `json:"fields,omitempty"`
	Thumbnail   *struct {
		URL string `json:"url"`
	} `json:"thumbnail,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	Footer    *struct {
		Text string `json:"text"`
	} `json:"footer,omitempty"`
}

func footer(text string) *struct {
	Text string `json:"text"`
} {
	return &struct {
		Text string `json:"text"`
	}{Text: text}
}

// ───────────────────────── Utilidades ─────────────────────────

func nowLocal() time.Time { return time.Now().In(cfg.Loc) }

// fmtFull: 15/11/2026 14:32:07 (UTC-03:00)
func fmtFull(t time.Time) string {
	return t.Format("02/01/2006 15:04:05") + " (UTC" + t.Format("-07:00") + ")"
}

func fmtPlain(t time.Time) string { return t.Format("02/01/2006 15:04:05") }

func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.In(cfg.Loc), true
}

func human(d time.Duration) string {
	total := int(d.Seconds())
	if total < 0 {
		total = 0
	}
	h, m, s := total/3600, (total%3600)/60, total%60
	var parts []string
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	parts = append(parts, fmt.Sprintf("%ds", s))
	return strings.Join(parts, " ")
}

func mdLink(text, link string) string {
	text = strings.NewReplacer("[", "(", "]", ")").Replace(text)
	if link == "" {
		return text
	}
	return "[" + text + "](" + link + ")"
}

func profileURL(id int64) string {
	return fmt.Sprintf("https://www.roblox.com/users/%d/profile", id)
}

func gameLink(name string, placeID int64) string {
	if name == "" {
		name = "Juego privado / desconocido"
	}
	link := ""
	if placeID != 0 {
		link = fmt.Sprintf("https://www.roblox.com/games/%d", placeID)
	}
	return mdLink(name, link)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// ───────────────────────── Cliente de Roblox ─────────────────────────

var (
	httpClient = &http.Client{Timeout: 20 * time.Second}
	csrfToken  string
	gameCache  = map[int64]string{}
	avatarMemo = map[int64]string{}
)

type httpResult struct {
	Status int
	Body   []byte
}

// robloxDo hace la request con manejo de X-CSRF-TOKEN y rate limit (429).
func robloxDo(ctx context.Context, method, rawURL string, query url.Values, payload interface{}) (*httpResult, error) {
	var body []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = b
	}
	if len(query) > 0 {
		rawURL += "?" + query.Encode()
	}
	var last *httpResult
	for attempt := 0; attempt < 5; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; RobloxPresenceMonitor/1.0)")
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if cfg.Roblosecurity != "" {
			req.Header.Set("Cookie", ".ROBLOSECURITY="+cfg.Roblosecurity)
		}
		if csrfToken != "" {
			req.Header.Set("X-CSRF-TOKEN", csrfToken)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		last = &httpResult{Status: resp.StatusCode, Body: data}

		if resp.StatusCode == http.StatusForbidden {
			if tok := resp.Header.Get("X-Csrf-Token"); tok != "" {
				csrfToken = tok
				continue
			}
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			wait := time.Duration(10*(attempt+1)) * time.Second
			if secs, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil {
				wait = time.Duration(secs * float64(time.Second))
			}
			log.Printf("[WARN] Rate limit de Roblox, esperando %s", wait.Round(time.Second))
			if !sleepCtx(ctx, wait) {
				return nil, ctx.Err()
			}
			continue
		}
		return last, nil
	}
	return last, nil
}

// resolveUsernames devuelve las cuentas en el mismo orden pedido y los usernames no encontrados.
func resolveUsernames(ctx context.Context, names []string) ([]*User, []string, error) {
	found := map[string]*User{}
	for i := 0; i < len(names); i += 100 {
		end := i + 100
		if end > len(names) {
			end = len(names)
		}
		res, err := robloxDo(ctx, http.MethodPost, usersURL, nil, map[string]interface{}{
			"usernames":          names[i:end],
			"excludeBannedUsers": false,
		})
		if err != nil {
			return nil, nil, err
		}
		if res.Status != http.StatusOK {
			return nil, nil, fmt.Errorf("users API respondió %d: %s", res.Status, truncate(string(res.Body), 200))
		}
		var parsed struct {
			Data []struct {
				Requested string `json:"requestedUsername"`
				ID        int64  `json:"id"`
				Name      string `json:"name"`
				Display   string `json:"displayName"`
			} `json:"data"`
		}
		if err := json.Unmarshal(res.Body, &parsed); err != nil {
			return nil, nil, err
		}
		for _, d := range parsed.Data {
			disp := d.Display
			if disp == "" {
				disp = d.Name
			}
			found[strings.ToLower(d.Requested)] = &User{ID: d.ID, Name: d.Name, Display: disp}
		}
	}
	var users []*User
	var missing []string
	seen := map[int64]bool{}
	for _, n := range names {
		u, ok := found[strings.ToLower(n)]
		if !ok {
			missing = append(missing, n)
			continue
		}
		if !seen[u.ID] {
			seen[u.ID] = true
			users = append(users, u)
		}
	}
	return users, missing, nil
}

func fetchPresences(ctx context.Context, ids []int64) (map[int64]Presence, error) {
	out := map[int64]Presence{}
	for i := 0; i < len(ids); i += 100 {
		end := i + 100
		if end > len(ids) {
			end = len(ids)
		}
		res, err := robloxDo(ctx, http.MethodPost, presenceURL, nil, map[string]interface{}{"userIds": ids[i:end]})
		if err != nil {
			return nil, err
		}
		if res.Status == http.StatusUnauthorized {
			return nil, errAuth
		}
		if res.Status != http.StatusOK {
			return nil, fmt.Errorf("presence API respondió %d: %s", res.Status, truncate(string(res.Body), 200))
		}
		var parsed struct {
			UserPresences []Presence `json:"userPresences"`
		}
		if err := json.Unmarshal(res.Body, &parsed); err != nil {
			return nil, err
		}
		for _, p := range parsed.UserPresences {
			out[p.UserID] = p
		}
	}
	return out, nil
}

func gameName(ctx context.Context, p Presence) string {
	if p.UniverseID != 0 {
		if name, ok := gameCache[p.UniverseID]; ok {
			return name
		}
		q := url.Values{"universeIds": {strconv.FormatInt(p.UniverseID, 10)}}
		if res, err := robloxDo(ctx, http.MethodGet, gamesURL, q, nil); err == nil && res.Status == http.StatusOK {
			var parsed struct {
				Data []struct {
					Name string `json:"name"`
				} `json:"data"`
			}
			if json.Unmarshal(res.Body, &parsed) == nil && len(parsed.Data) > 0 && parsed.Data[0].Name != "" {
				gameCache[p.UniverseID] = parsed.Data[0].Name
				return parsed.Data[0].Name
			}
		} else if err != nil {
			log.Printf("[WARN] No pude obtener el nombre del juego: %v", err)
		}
	}
	if p.LastLocation != "" {
		return p.LastLocation
	}
	return "Juego privado / desconocido"
}

func avatarURL(ctx context.Context, id int64) string {
	if u, ok := avatarMemo[id]; ok {
		return u
	}
	q := url.Values{
		"userIds": {strconv.FormatInt(id, 10)},
		"size":    {"150x150"},
		"format":  {"Png"},
	}
	res, err := robloxDo(ctx, http.MethodGet, thumbsURL, q, nil)
	if err != nil || res.Status != http.StatusOK {
		return ""
	}
	var parsed struct {
		Data []struct {
			ImageURL string `json:"imageUrl"`
		} `json:"data"`
	}
	if json.Unmarshal(res.Body, &parsed) == nil && len(parsed.Data) > 0 && parsed.Data[0].ImageURL != "" {
		avatarMemo[id] = parsed.Data[0].ImageURL
		return parsed.Data[0].ImageURL
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ───────────────────────── Estado y detección ─────────────────────────

func snapshot(ctx context.Context, p Presence) Record {
	status := p.Type
	if status != Offline && status != Online && status != InGame && status != Studio {
		status = Offline
	}
	place := p.RootPlaceID
	if place == 0 {
		place = p.PlaceID
	}
	r := Record{Status: status, PlaceID: place, UniverseID: p.UniverseID, LastOnline: p.LastOnline}
	if status == InGame {
		r.Game = gameName(ctx, p)
	}
	return r
}

func gameKey(r *Record) int64 {
	if r.UniverseID != 0 {
		return r.UniverseID
	}
	return r.PlaceID
}

// mergeRecord combina el estado anterior con el nuevo y calcula desde cuándo dura cada estado.
func mergeRecord(old *Record, snap Record, when time.Time) Record {
	iso := when.Format(time.RFC3339Nano)
	rec := snap
	if snap.Status != Offline {
		if old != nil && old.Status != Offline && old.OnlineSince != "" {
			rec.OnlineSince = old.OnlineSince
		} else {
			rec.OnlineSince = iso
		}
	}
	if snap.Status == InGame {
		if old != nil && old.Status == InGame && gameKey(old) == gameKey(&snap) && old.GameSince != "" {
			rec.GameSince = old.GameSince
		} else {
			rec.GameSince = iso
		}
	}
	return rec
}

// detect devuelve el tipo de evento ("" si no hubo cambio).
func detect(old *Record, nw *Record) string {
	if old == nil {
		return ""
	}
	o, n := old.Status, nw.Status
	if o == n {
		ok, nk := gameKey(old), gameKey(nw)
		if n == InGame && ok != 0 && nk != 0 && ok != nk {
			return "SWITCH"
		}
		return ""
	}
	switch {
	case n == Offline:
		return "DISCONNECT"
	case o == Offline:
		switch n {
		case InGame:
			return "CONNECT_PLAY"
		case Studio:
			return "CONNECT_STUDIO"
		}
		return "CONNECT"
	case n == InGame:
		return "PLAY"
	case n == Studio:
		return "STUDIO"
	case o == InGame:
		return "LEFT_GAME"
	}
	return "LEFT_STUDIO"
}

var eventText = map[string]struct{ Emoji, Text, Color string }{
	"CONNECT":        {"🟢", "se conectó", "green"},
	"CONNECT_PLAY":   {"🎮", "se conectó y entró a jugar", "blue"},
	"CONNECT_STUDIO": {"🛠️", "se conectó y abrió Roblox Studio", "purple"},
	"PLAY":           {"🎮", "entró a jugar", "blue"},
	"SWITCH":         {"🔄", "cambió de juego", "orange"},
	"LEFT_GAME":      {"🚪", "salió del juego (sigue conectado)", "yellow"},
	"STUDIO":         {"🛠️", "abrió Roblox Studio", "purple"},
	"LEFT_STUDIO":    {"🚪", "salió de Studio (sigue conectado)", "yellow"},
	"DISCONNECT":     {"🔴", "se desconectó", "red"},
}

func buildEventEmbed(ctx context.Context, u *User, kind string, old, nw *Record, when time.Time, late bool) Embed {
	ev := eventText[kind]
	fields := []Field{{
		Name:   "Usuario",
		Value:  mdLink(fmt.Sprintf("%s (@%s)", u.Display, u.Name), profileURL(u.ID)),
		Inline: true,
	}}

	switch kind {
	case "CONNECT_PLAY", "PLAY":
		fields = append(fields, Field{"Juego", gameLink(nw.Game, nw.PlaceID), true})
	case "SWITCH":
		fields = append(fields,
			Field{"Antes", gameLink(old.Game, old.PlaceID), true},
			Field{"Ahora", gameLink(nw.Game, nw.PlaceID), true})
	case "LEFT_GAME":
		fields = append(fields, Field{"Jugaba", gameLink(old.Game, old.PlaceID), true})
	}

	if !late && old != nil {
		if kind == "DISCONNECT" {
			if t, err := time.Parse(time.RFC3339, old.OnlineSince); err == nil {
				fields = append(fields, Field{"Tiempo conectado", human(when.Sub(t)), true})
			}
		} else if kind == "LEFT_GAME" || kind == "SWITCH" {
			if t, err := time.Parse(time.RFC3339, old.GameSince); err == nil {
				fields = append(fields, Field{"Tiempo en ese juego", human(when.Sub(t)), true})
			}
		}
	}

	desc := "🕒 **" + fmtFull(when) + "**"
	if late {
		desc += "\n_Cambio detectado al (re)iniciar el bot; la hora es la de detección._"
	}

	e := Embed{
		Title:       fmt.Sprintf("%s %s %s", ev.Emoji, u.Name, ev.Text),
		URL:         profileURL(u.ID),
		Description: desc,
		Color:       colors[ev.Color],
		Fields:      fields,
		Timestamp:   when.UTC().Format(time.RFC3339),
		Footer:      footer(fmt.Sprintf("ID %d", u.ID)),
	}
	if av := avatarURL(ctx, u.ID); av != "" {
		e.Thumbnail = &struct {
			URL string `json:"url"`
		}{URL: av}
	}
	return e
}

func buildSummaryEmbed(users []*User, state map[string]*Record, title string, when time.Time) Embed {
	var lines []string
	for _, u := range users {
		rec := state[strconv.FormatInt(u.ID, 10)]
		name := "**" + u.Name + "**"
		if rec == nil {
			lines = append(lines, "❔ "+name+" — sin datos")
			continue
		}
		since := ""
		if t, ok := parseTime(rec.OnlineSince); ok {
			since = fmtPlain(t)
		}
		switch rec.Status {
		case InGame:
			gs := ""
			if t, ok := parseTime(rec.GameSince); ok {
				gs = " desde " + fmtPlain(t)
			}
			lines = append(lines, "🎮 "+name+" — jugando "+gameLink(rec.Game, rec.PlaceID)+gs)
		case Studio:
			l := "🛠️ " + name + " — en Roblox Studio"
			if since != "" {
				l += " (conectado desde " + since + ")"
			}
			lines = append(lines, l)
		case Online:
			l := "🟢 " + name + " — online"
			if since != "" {
				l += " desde " + since
			}
			lines = append(lines, l)
		default:
			l := "⚫ " + name + " — offline"
			if t, ok := parseTime(rec.LastOnline); ok {
				l += " (última vez: " + fmtPlain(t) + ")"
			}
			lines = append(lines, l)
		}
	}
	desc := strings.Join(lines, "\n")
	if desc == "" {
		desc = "Sin cuentas"
	}
	return Embed{
		Title:       title,
		Description: truncate(desc, 4000),
		Color:       colors["grey"],
		Footer:      footer(fmt.Sprintf("%s • consulta cada %ds", fmtFull(when), int(cfg.PollInterval.Seconds()))),
	}
}

func loadState() map[string]*Record {
	state := map[string]*Record{}
	if data, err := os.ReadFile(cfg.StateFile); err == nil {
		if json.Unmarshal(data, &state) != nil || state == nil {
			state = map[string]*Record{}
		}
	}
	return state
}

func saveState(state map[string]*Record) {
	data, err := json.MarshalIndent(state, "", " ")
	if err == nil {
		tmp := cfg.StateFile + ".tmp"
		if err = os.WriteFile(tmp, data, 0o644); err == nil {
			err = os.Rename(tmp, cfg.StateFile)
		}
	}
	if err != nil {
		log.Printf("[WARN] No pude guardar el estado: %v", err)
	}
}

// ───────────────────────── Discord ─────────────────────────

func sendDiscord(ctx context.Context, embeds ...Embed) bool {
	if len(embeds) > 10 {
		embeds = embeds[:10]
	}
	payload, err := json.Marshal(map[string]interface{}{"username": cfg.BotName, "embeds": embeds})
	if err != nil {
		log.Printf("[ERROR] No pude armar el mensaje: %v", err)
		return false
	}
	for attempt := 0; attempt < 5; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.WebhookURL, bytes.NewReader(payload))
		if err != nil {
			log.Printf("[ERROR] Webhook inválido")
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			log.Printf("[ERROR] Error enviando a Discord: %v", err)
			sleepCtx(ctx, 3*time.Second)
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			wait := 2.0
			var rl struct {
				RetryAfter float64 `json:"retry_after"`
			}
			if json.Unmarshal(data, &rl) == nil && rl.RetryAfter > 0 {
				wait = rl.RetryAfter
			}
			sleepCtx(ctx, time.Duration((wait+0.5)*float64(time.Second)))
			continue
		}
		if resp.StatusCode >= 400 {
			log.Printf("[ERROR] Discord respondió %d: %s", resp.StatusCode, truncate(string(data), 200))
			return false
		}
		return true
	}
	return false
}

// ───────────────────────── Bucle principal ─────────────────────────

func pollOnce(ctx context.Context, users []*User, state map[string]*Record, first bool) error {
	ids := make([]int64, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	presences, err := fetchPresences(ctx, ids)
	if err != nil {
		return err
	}
	when := nowLocal()
	for _, u := range users {
		p, ok := presences[u.ID]
		if !ok {
			continue
		}
		key := strconv.FormatInt(u.ID, 10)
		snap := snapshot(ctx, p)
		old := state[key]
		kind := detect(old, &snap)
		rec := mergeRecord(old, snap, when)
		if kind != "" {
			log.Printf("[INFO] %s -> %s", u.Name, kind)
			sendDiscord(ctx, buildEventEmbed(ctx, u, kind, old, &rec, when, first))
		}
		state[key] = &rec
	}
	saveState(state)
	return nil
}

func main() {
	loadDotEnv(".env")
	if err := loadConfig(); err != nil {
		log.Fatal(err)
	}
	if !strings.HasPrefix(cfg.WebhookURL, "https://discord.com/api/webhooks/") &&
		!strings.HasPrefix(cfg.WebhookURL, "https://discordapp.com/api/webhooks/") {
		log.Fatal("Falta DISCORD_WEBHOOK_URL (definila en el archivo .env).")
	}
	if len(cfg.Usernames) == 0 {
		log.Fatal("Falta ROBLOX_USERNAMES (usernames separados por coma) o usernames.txt.")
	}
	if cfg.Roblosecurity == "" {
		log.Println("[WARN] Sin ROBLOSECURITY: la API de presencia de Roblox suele exigir sesión (401).")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var users []*User
	var missing []string
	for attempt := 1; ; attempt++ {
		var err error
		users, missing, err = resolveUsernames(ctx, cfg.Usernames)
		if err == nil {
			break
		}
		if attempt >= 5 || ctx.Err() != nil {
			log.Fatalf("No pude resolver los usernames: %v", err)
		}
		log.Printf("[WARN] Error resolviendo usernames (%v), reintentando...", err)
		sleepCtx(ctx, 10*time.Second)
	}
	if len(missing) > 0 {
		log.Printf("[WARN] Usernames no encontrados: %s", strings.Join(missing, ", "))
		var l []string
		for _, m := range missing {
			l = append(l, "• `"+m+"`")
		}
		sendDiscord(ctx, Embed{Title: "⚠️ Usernames no encontrados", Description: strings.Join(l, "\n"), Color: colors["orange"]})
	}
	if len(users) == 0 {
		log.Fatal("Ningún username válido para monitorear.")
	}
	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, u.Name)
	}
	log.Printf("[INFO] Monitoreando %d cuenta(s): %s", len(users), strings.Join(names, ", "))

	state := loadState()
	first, authAlerted, errCount := true, false, 0
	lastSummary := time.Now()

	for {
		err := pollOnce(ctx, users, state, first)
		switch {
		case ctx.Err() != nil:
			log.Println("[INFO] Detenido.")
			return
		case err == nil:
			if first {
				sendDiscord(ctx, buildSummaryEmbed(users, state, "📡 Monitoreo iniciado — estado actual", nowLocal()))
				first = false
			}
			authAlerted, errCount = false, 0
		case errors.Is(err, errAuth):
			errCount++
			log.Println("[ERROR] Sesión de Roblox inválida o ausente (401).")
			if !authAlerted {
				sendDiscord(ctx, Embed{
					Title: "⚠️ No pude consultar la presencia de Roblox",
					Description: "Roblox devolvió **401**. Revisá `ROBLOSECURITY`: la cookie falta, " +
						"venció o fue invalidada. El bot reintenta solo.",
					Color:  colors["orange"],
					Footer: footer(fmtFull(nowLocal())),
				})
				authAlerted = true
			}
		default:
			errCount++
			log.Printf("[ERROR] Error consultando Roblox: %v", err)
		}

		if cfg.SummaryEvery > 0 && !first && time.Since(lastSummary) >= cfg.SummaryEvery {
			sendDiscord(ctx, buildSummaryEmbed(users, state, "📋 Resumen de conectividad", nowLocal()))
			lastSummary = time.Now()
		}

		wait := cfg.PollInterval
		if errCount > 0 {
			shift := errCount
			if shift > 4 {
				shift = 4
			}
			wait = cfg.PollInterval << uint(shift)
			if wait > 5*time.Minute {
				wait = 5 * time.Minute
			}
		}
		if !sleepCtx(ctx, wait) {
			log.Println("[INFO] Detenido.")
			return
		}
	}
}
