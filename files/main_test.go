package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Simula Roblox y Discord y recorre un ciclo completo de estados.
func TestFlujoCompleto(t *testing.T) {
	ar, _ := time.LoadLocation("America/Argentina/Buenos_Aires")
	cfg = &Config{Loc: ar, BotName: "test", PollInterval: 30 * time.Second, StateFile: t.TempDir() + "/state.json"}

	var current Presence
	csrfSeen := false
	roblox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/presence":
			if r.Header.Get("X-CSRF-TOKEN") == "" { // primer intento: Roblox pide token CSRF
				w.Header().Set("X-Csrf-Token", "tok123")
				w.WriteHeader(403)
				return
			}
			csrfSeen = true
			json.NewEncoder(w).Encode(map[string]interface{}{"userPresences": []Presence{current}})
		case "/games":
			names := map[string]string{"111": "Blox Fruits", "222": "Adopt Me!"}
			id := r.URL.Query().Get("universeIds")
			fmt.Fprintf(w, `{"data":[{"name":%q}]}`, names[id])
		case "/thumbs":
			fmt.Fprint(w, `{"data":[{"imageUrl":"https://example.com/a.png"}]}`)
		case "/users":
			fmt.Fprint(w, `{"data":[{"requestedUsername":"tester","id":1,"name":"Tester","displayName":"Tester"}]}`)
		}
	}))
	defer roblox.Close()
	usersURL, presenceURL = roblox.URL+"/users", roblox.URL+"/presence"
	gamesURL, thumbsURL = roblox.URL+"/games", roblox.URL+"/thumbs"

	var titles []string
	var last Embed
	discord := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Embeds []Embed }
		json.NewDecoder(r.Body).Decode(&body)
		titles = append(titles, body.Embeds[0].Title)
		last = body.Embeds[0]
		w.WriteHeader(204)
	}))
	defer discord.Close()
	cfg.WebhookURL = discord.URL

	ctx := context.Background()
	users, missing, err := resolveUsernames(ctx, []string{"Tester", "NoExiste"})
	if err != nil || len(users) != 1 || len(missing) != 1 || missing[0] != "NoExiste" {
		t.Fatalf("resolveUsernames: %v %v %v", users, missing, err)
	}

	state := map[string]*Record{}
	steps := []Presence{
		{UserID: 1, Type: 0, LastOnline: "2026-11-15T10:00:00.123Z"},
		{UserID: 1, Type: 1},
		{UserID: 1, Type: 2, UniverseID: 111, RootPlaceID: 1},
		{UserID: 1, Type: 2, UniverseID: 222, RootPlaceID: 2},
		{UserID: 1, Type: 1},
		{UserID: 1, Type: 3},
		{UserID: 1, Type: 0, LastOnline: "2026-11-15T10:00:00.123Z"},
	}
	for _, s := range steps {
		current = s
		if err := pollOnce(ctx, users, state, false); err != nil {
			t.Fatal(err)
		}
	}
	if !csrfSeen {
		t.Fatal("no se manejó el token CSRF")
	}
	want := []string{"se conectó", "entró a jugar", "cambió de juego", "salió del juego", "abrió Roblox Studio", "se desconectó"}
	if len(titles) != len(want) {
		t.Fatalf("eventos: %v", titles)
	}
	for i, w := range want {
		if !strings.Contains(titles[i], w) {
			t.Errorf("evento %d: %q no contiene %q", i, titles[i], w)
		}
	}
	if !strings.Contains(last.Description, "(UTC-03:00)") {
		t.Errorf("la fecha no incluye zona horaria: %s", last.Description)
	}

	sum := buildSummaryEmbed(users, state, "resumen", nowLocal())
	if !strings.Contains(sum.Description, "offline (última vez: 15/11/2026 07:00:00)") {
		t.Errorf("resumen: %s", sum.Description)
	}

	// Reinicio: la cuenta estaba jugando según el estado guardado y ahora está offline.
	state["1"] = &Record{Status: InGame, UniverseID: 111, PlaceID: 1, Game: "Blox Fruits"}
	titles = nil
	current = Presence{UserID: 1, Type: 0}
	if err := pollOnce(ctx, users, state, true); err != nil {
		t.Fatal(err)
	}
	if len(titles) != 1 || !strings.Contains(last.Description, "al (re)iniciar") {
		t.Errorf("reinicio: %v / %s", titles, last.Description)
	}
}

func TestAuthError(t *testing.T) {
	cfg = &Config{Loc: time.UTC, PollInterval: 30 * time.Second}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	presenceURL = srv.URL
	if _, err := fetchPresences(context.Background(), []int64{1}); err != errAuth {
		t.Fatalf("esperaba errAuth, obtuve %v", err)
	}
}

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/.env"
	if err := os.WriteFile(path, []byte("# comentario\nROBLOX_USERNAMES=\"a, b\"\nOTRA=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loadDotEnv(path)
	if got := loadUsernames(); len(got) != 2 || got[1] != "b" {
		t.Fatalf("usernames: %v", got)
	}
}

func TestLoadConfigReadsCredentialEnvironmentVariables(t *testing.T) {
	original := cfg
	defer func() { cfg = original }()

	t.Setenv("DISCORD_WEBHOOK_URL", "https://discord.com/api/webhooks/test/token")
	t.Setenv("ROBLOSECURITY", "test-cookie")
	t.Setenv("TIMEZONE", "UTC")

	if err := loadConfig(); err != nil {
		t.Fatal(err)
	}
	if cfg.WebhookURL != "https://discord.com/api/webhooks/test/token" {
		t.Errorf("webhook URL was not loaded from DISCORD_WEBHOOK_URL")
	}
	if cfg.Roblosecurity != "test-cookie" {
		t.Errorf("Roblox cookie was not loaded from ROBLOSECURITY")
	}
}
