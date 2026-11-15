package events

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"roblox-presence-bot/internal/config"
	"roblox-presence-bot/internal/model"
)

func firstEmbed(payload map[string]any) map[string]any {
	embeds := payload["embeds"].([]any)
	return embeds[0].(map[string]any)
}

func embedFields(embed map[string]any) []any {
	fields, _ := embed["fields"].([]any)
	return fields
}

func fieldByLabel(fields []any, label string) string {
	for _, raw := range fields {
		field := raw.(map[string]any)
		if field["name"] == label {
			value, _ := field["value"].(string)
			return value
		}
	}
	return ""
}

func TestEventPayloadIncludesButtonsAndMention(t *testing.T) {
	account := model.Account{
		Username:    "Tester_123",
		DisplayName: model.Ptr("Tester"),
		UserID:      model.Ptr(int64(999)),
	}
	game := &model.Game{
		Name:        "Juego_*uno*",
		PlaceID:     model.Ptr(int64(1001)),
		RootPlaceID: model.Ptr(int64(1001)),
		GameID:      "server-aaa",
		URL:         "https://www.roblox.com/games/1001",
		JoinURL:     "https://www.roblox.com/games/start?placeId=1001",
		IconURL:     "https://example.com/icon.png",
	}
	event := model.Event{
		Type:    "connected",
		At:      time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC),
		Account: account,
		Next: &model.Presence{
			PresenceType: 2,
			Game:         game,
		},
	}
	cfg := &config.Config{PollIntervalSeconds: 30, Mention: "@here"}
	payload := BuildEventPayload(event, "https://example.com/avatar.png", cfg)

	if payload["content"] != "@here" {
		t.Fatalf("falta la mención: %v", payload["content"])
	}
	components, ok := payload["components"].([]any)
	if !ok || len(components) == 0 {
		t.Fatal("faltan los botones del juego")
	}
	embed := firstEmbed(payload)
	if embed["title"] != "🟢 Se conectó y está jugando" {
		t.Fatalf("título inesperado: %v", embed["title"])
	}
	if _, ok := embed["thumbnail"]; !ok {
		t.Fatal("falta el icono del juego")
	}
	fields := embedFields(embed)
	if got := fieldByLabel(fields, "🎮 Juego"); !strings.Contains(got, `Juego\_\*uno\*`) {
		t.Fatalf("el nombre del juego debe escaparse: %q", got)
	}
}

func TestStatusPayloadNoAccounts(t *testing.T) {
	cfg := &config.Config{PollIntervalSeconds: 45}
	now := time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC)
	payload := BuildStatusPayload(nil, cfg, now)
	embed := firstEmbed(payload)
	description, _ := embed["description"].(string)
	if !strings.Contains(description, "No hay cuentas configuradas") {
		t.Fatalf("descripción inesperada: %q", description)
	}
	if !strings.Contains(description, "presence-bot add") {
		t.Fatalf("la descripción debe indicar el comando de Go: %q", description)
	}
	if _, ok := embed["footer"]; !ok {
		t.Fatal("el último embed debe llevar footer")
	}
	if embed["timestamp"] != "2026-11-14T18:00:00.000Z" {
		t.Fatalf("timestamp inesperado: %v", embed["timestamp"])
	}
}

func TestStatusPayloadOnlineAndOffline(t *testing.T) {
	account := model.Account{Username: "Cuenta_Prueba", UserID: model.Ptr(int64(1))}
	online := &model.UserState{
		PresenceType: 2,
		SessionStart: model.Ptr("2026-11-14T17:00:00.000Z"),
		Since:        "2026-11-14T17:30:00.000Z",
		Game: &model.Game{
			Name: "Juego Simulado",
			URL:  "https://www.roblox.com/games/555",
		},
	}
	payload := BuildStatusPayload(
		[]Entry{{Account: account, State: online}},
		&config.Config{PollIntervalSeconds: 30},
		time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC),
	)
	value := fieldByLabel(embedFields(firstEmbed(payload)), "")
	if value != "" {
		t.Fatalf("el helper no debía encontrar un campo vacío: %q", value)
	}
	fields := embedFields(firstEmbed(payload))
	status := fields[0].(map[string]any)["value"].(string)
	if !strings.Contains(status, "En línea desde") || !strings.Contains(status, "Jugando desde") {
		t.Fatalf("el panel debe mostrar ambas horas: %q", status)
	}
	if !strings.Contains(status, "Abrir juego") {
		t.Fatalf("el panel debe enlazar al juego: %q", status)
	}

	lastSession := int64(3661000)
	offline := &model.UserState{
		PresenceType:  0,
		LastSeenAt:    model.Ptr("2026-11-14T17:55:00.000Z"),
		LastSessionMs: &lastSession,
	}
	payload = BuildStatusPayload(
		[]Entry{{Account: account, State: offline}},
		&config.Config{PollIntervalSeconds: 30},
		time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC),
	)
	status = embedFields(firstEmbed(payload))[0].(map[string]any)["value"].(string)
	if !strings.Contains(status, "Última conexión") {
		t.Fatalf("el panel offline debe mostrar la última conexión: %q", status)
	}
	if !strings.Contains(status, "Última sesión: 1 h 1 min") {
		t.Fatalf("el panel offline debe mostrar la última sesión: %q", status)
	}
}

func TestStatusPayloadSplitsFields(t *testing.T) {
	entries := make([]Entry, 0, 26)
	for index := 0; index < 26; index++ {
		entries = append(entries, Entry{
			Account: model.Account{
				Username: fmt.Sprintf("cuenta_%d", index),
				UserID:   model.Ptr(int64(index + 1)),
			},
		})
	}
	payload := BuildStatusPayload(entries, &config.Config{PollIntervalSeconds: 30}, time.Now())
	embeds := payload["embeds"].([]any)
	if len(embeds) != 2 {
		t.Fatalf("26 cuentas deben repartirse en 2 embeds, hay %d", len(embeds))
	}
	if len(embedFields(embeds[1].(map[string]any))) != 1 {
		t.Fatalf("el segundo embed debe tener 1 campo")
	}
}

func TestStartupPayloadLimit(t *testing.T) {
	entries := make([]Entry, 0, 25)
	for index := 0; index < 25; index++ {
		entries = append(entries, Entry{
			Account: model.Account{Username: fmt.Sprintf("cuenta_%d", index)},
		})
	}
	payload := BuildStartupPayload(entries, &config.Config{PollIntervalSeconds: 30}, time.Now())
	embed := firstEmbed(payload)
	fields := embedFields(embed)
	if len(fields) != 25 {
		t.Fatalf("se esperaban 24 cuentas + resumen, hay %d campos", len(fields))
	}
	last := fields[len(fields)-1].(map[string]any)["value"].(string)
	if !strings.Contains(last, "1 cuenta(s) más") {
		t.Fatalf("falta el resumen de cuentas restantes: %q", last)
	}
}

func TestTestPayload(t *testing.T) {
	payload := BuildTestPayload(
		&config.Config{PollIntervalSeconds: 30},
		"123456",
		2,
		"Kricko",
	)
	fields := embedFields(firstEmbed(payload))
	if got := fieldByLabel(fields, "📡 Canal"); got != "`123456`" {
		t.Fatalf("canal inesperado: %q", got)
	}
	if got := fieldByLabel(fields, "🍪 Cookie de Roblox"); !strings.Contains(got, "@Kricko") {
		t.Fatalf("cookie inesperada: %q", got)
	}
	if got := fieldByLabel(fields, "⏱️ Intervalo"); got != "30 s" {
		t.Fatalf("intervalo inesperado: %q", got)
	}
}
