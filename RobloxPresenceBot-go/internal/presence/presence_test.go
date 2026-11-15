package presence

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"roblox-presence-bot/internal/config"
	"roblox-presence-bot/internal/events"
	"roblox-presence-bot/internal/format"
	"roblox-presence-bot/internal/model"
)

type fakeResolver struct{}

func (fakeResolver) GetGameByUniverse(universeID int64, fallbackPlaceID *int64) (*model.Game, error) {
	if universeID != 111 {
		return nil, nil
	}
	game := &model.Game{
		Name:        "Juego Uno",
		RootPlaceID: fallbackPlaceID,
		UniverseID:  model.Ptr(universeID),
		IconURL:     "https://example.com/icon.png",
	}
	if fallbackPlaceID != nil {
		game.PlaceID = model.Ptr(*fallbackPlaceID)
		game.URL = "https://www.roblox.com/games/" + strconv.FormatInt(*fallbackPlaceID, 10)
	}
	return game, nil
}

func (fakeResolver) GetPlaceDetails(placeID int64) (*model.Game, error) {
	return fakeResolver{}.GetGameByUniverse(111, model.Ptr(placeID))
}

func embedField(payload map[string]any, fieldIndex int) map[string]any {
	embeds := payload["embeds"].([]any)
	embed := embeds[0].(map[string]any)
	fields := embed["fields"].([]any)
	return fields[fieldIndex].(map[string]any)
}

func hasFieldContaining(payload map[string]any, text string) bool {
	embeds := payload["embeds"].([]any)
	embed := embeds[0].(map[string]any)
	fields, _ := embed["fields"].([]any)
	for _, raw := range fields {
		field := raw.(map[string]any)
		value, _ := field["value"].(string)
		if strings.Contains(value, text) {
			return true
		}
	}
	return false
}

func TestPresenceTransitionsAndEmbeds(t *testing.T) {
	account := model.Account{
		Username:    "Tester_123",
		DisplayName: model.Ptr("Tester"),
		UserID:      model.Ptr(int64(999)),
	}
	cfg := &config.Config{PollIntervalSeconds: 30}
	start := time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC)

	state := UpdateState(account, nil, NormalizePresence(RawPresence{
		UserPresenceType: 0,
		UserID:           account.ID(),
	}), start)

	step := func(seconds int, raw RawPresence, game *model.Game) (*model.Event, map[string]any) {
		now := start.Add(time.Duration(seconds) * time.Second)
		next := NormalizePresence(raw)
		if next.PresenceType == 2 {
			next.Game = game
		}
		event := DetectEvent(state, next, now)
		var payload map[string]any
		if event != nil {
			event.Account = account
			payload = events.BuildEventPayload(*event, "", cfg)
		}
		state = UpdateState(account, state, next, now)
		return event, payload
	}

	event, _ := step(30, RawPresence{
		UserPresenceType: 1,
		UserID:           account.ID(),
		LastLocation:     "Website",
	}, nil)
	if event == nil || event.Type != "connected" {
		t.Fatalf("se esperaba connected, se obtuvo %+v", event)
	}

	gameOne := NormalizePresence(RawPresence{
		UserPresenceType: 2,
		UserID:           account.ID(),
		PlaceID:          model.Ptr(int64(1001)),
		RootPlaceID:      model.Ptr(int64(1001)),
		UniverseID:       model.Ptr(int64(111)),
		GameID:           model.Ptr("server-aaa"),
		LastLocation:     "Juego Uno",
	})
	gameOne.Game = ResolveGameDetails(fakeResolver{}, gameOne, nil)

	event, payload := step(90, RawPresence{
		UserPresenceType: 2,
		UserID:           account.ID(),
		PlaceID:          model.Ptr(int64(1001)),
		RootPlaceID:      model.Ptr(int64(1001)),
		UniverseID:       model.Ptr(int64(111)),
		GameID:           model.Ptr("server-aaa"),
		LastLocation:     "Juego Uno",
	}, gameOne.Game)
	if event == nil || event.Type != "game_started" {
		t.Fatalf("se esperaba game_started, se obtuvo %+v", event)
	}
	if components, ok := payload["components"].([]any); !ok || len(components) == 0 {
		t.Fatal("el aviso de inicio de juego debe incluir botones")
	}

	gameTwo := NormalizePresence(RawPresence{
		UserPresenceType: 2,
		UserID:           account.ID(),
		PlaceID:          model.Ptr(int64(2002)),
		RootPlaceID:      model.Ptr(int64(2002)),
		UniverseID:       model.Ptr(int64(222)),
		LastLocation:     "Juego Dos",
	})
	gameTwo.Game = ResolveGameDetails(fakeResolver{}, gameTwo, nil)

	event, _ = step(150, RawPresence{
		UserPresenceType: 2,
		UserID:           account.ID(),
		PlaceID:          model.Ptr(int64(2002)),
		RootPlaceID:      model.Ptr(int64(2002)),
		UniverseID:       model.Ptr(int64(222)),
		LastLocation:     "Juego Dos",
	}, gameTwo.Game)
	if event == nil || event.Type != "game_changed" {
		t.Fatalf("se esperaba game_changed, se obtuvo %+v", event)
	}

	event, payload = step(210, RawPresence{
		UserPresenceType: 1,
		UserID:           account.ID(),
		LastLocation:     "Website",
	}, nil)
	if event == nil || event.Type != "left_game" {
		t.Fatalf("se esperaba left_game, se obtuvo %+v", event)
	}
	if !hasFieldContaining(payload, "1 min") {
		t.Fatal("el aviso de salida debe calcular el tiempo en el juego (1 min)")
	}

	event, payload = step(270, RawPresence{
		UserPresenceType: 0,
		UserID:           account.ID(),
	}, nil)
	if event == nil || event.Type != "disconnected" {
		t.Fatalf("se esperaba disconnected, se obtuvo %+v", event)
	}
	if !hasFieldContaining(payload, "4 min") {
		t.Fatal("el aviso de desconexión debe calcular el tiempo en línea (4 min)")
	}

	startup := events.BuildStartupPayload([]events.Entry{{Account: account, State: state}}, cfg, start.Add(270*time.Second))
	embeds := startup["embeds"].([]any)
	if len(embeds) != 1 {
		t.Fatalf("el resumen inicial debe tener 1 embed, tiene %d", len(embeds))
	}

	panel := events.BuildStatusPayload([]events.Entry{{Account: account, State: state}}, cfg, start.Add(270*time.Second))
	panelEmbeds := panel["embeds"].([]any)
	if len(panelEmbeds) != 1 {
		t.Fatalf("el panel debe tener 1 embed, tiene %d", len(panelEmbeds))
	}
	field := embedField(panel, 0)
	name, _ := field["name"].(string)
	value, _ := field["value"].(string)
	if !strings.Contains(name, "@Tester_123") {
		t.Fatalf("el panel debe mostrar al usuario: %q", name)
	}
	if !strings.Contains(value, "Última conexión") {
		t.Fatalf("el panel debe mostrar la última conexión: %q", value)
	}
	if !strings.Contains(value, "<t:") {
		t.Fatalf("el panel debe usar horas de Discord: %q", value)
	}

	if got := format.FormatDuration(model.Ptr(int64(3661000))); got != "1 h 1 min" {
		t.Fatalf("duración inesperada: %s", got)
	}

	emptyGame := NormalizePresence(RawPresence{UserPresenceType: 2, UserID: 1})
	if game := ResolveGameDetails(fakeResolver{}, emptyGame, nil); game != nil {
		t.Fatalf("sin cookie no debe inventar un juego: %+v", game)
	}
}

func TestDetectEventIgnoresSameGame(t *testing.T) {
	previous := &model.UserState{
		PresenceType: 2,
		RootPlaceID:  model.Ptr(int64(1001)),
		Game:         &model.Game{Name: "Juego Uno"},
		SessionStart: model.Ptr("2026-11-14T18:00:00.000Z"),
		Since:        "2026-11-14T18:00:00.000Z",
	}
	next := NormalizePresence(RawPresence{
		UserPresenceType: 2,
		RootPlaceID:      model.Ptr(int64(1001)),
	})
	event := DetectEvent(previous, next, time.Date(2026, 11, 14, 18, 5, 0, 0, time.UTC))
	if event != nil {
		t.Fatalf("no debe haber evento si sigue en el mismo juego: %+v", event)
	}
}

func TestNormalizePresence(t *testing.T) {
	presence := NormalizePresence(RawPresence{
		UserPresenceType: 9,
		LastLocation:     "  Sitio  ",
		IsOnline:         model.Ptr(false),
	})
	if presence.PresenceType != 0 {
		t.Fatalf("un tipo desconocido debe normalizarse a 0: %d", presence.PresenceType)
	}
	if presence.LastLocation != "Sitio" {
		t.Fatalf("lastLocation debe recortarse: %q", presence.LastLocation)
	}
	if presence.IsOnline {
		t.Fatal("isOnline debe respetar el valor explícito")
	}
}
