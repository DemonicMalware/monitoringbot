package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"roblox-presence-bot/internal/config"
	"roblox-presence-bot/internal/discord"
	"roblox-presence-bot/internal/logger"
	"roblox-presence-bot/internal/model"
	"roblox-presence-bot/internal/presence"
	"roblox-presence-bot/internal/store"
)

type fakeRoblox struct {
	queue []presence.RawPresence
}

func (f *fakeRoblox) ResolveUsernames(names []string) (map[string]model.ResolvedUser, error) {
	result := make(map[string]model.ResolvedUser, len(names))
	for _, name := range names {
		result[strings.ToLower(name)] = model.ResolvedUser{
			ID:          12345,
			Name:        "Cuenta_Prueba",
			DisplayName: "Cuenta Prueba",
		}
	}
	return result, nil
}

func (f *fakeRoblox) GetPresences([]int64) ([]presence.RawPresence, error) {
	if len(f.queue) == 0 {
		return nil, nil
	}
	next := f.queue[0]
	f.queue = f.queue[1:]
	return []presence.RawPresence{next}, nil
}

func (f *fakeRoblox) GetGameByUniverse(universeID int64, fallbackPlaceID *int64) (*model.Game, error) {
	game := &model.Game{
		Name:       "Juego Simulado",
		UniverseID: model.Ptr(universeID),
	}
	if fallbackPlaceID != nil {
		game.RootPlaceID = model.Ptr(*fallbackPlaceID)
		game.PlaceID = model.Ptr(*fallbackPlaceID)
		game.URL = "https://www.roblox.com/games/" + strconv.FormatInt(*fallbackPlaceID, 10)
	}
	return game, nil
}

func (f *fakeRoblox) GetPlaceDetails(placeID int64) (*model.Game, error) {
	return f.GetGameByUniverse(777, model.Ptr(placeID))
}

func (f *fakeRoblox) GetAvatarURL(int64) (string, error) {
	return "", nil
}

type panelSend struct {
	id      string
	payload map[string]any
}

type panelEdit struct {
	messageID string
	payload   map[string]any
}

type fakeDiscord struct {
	sent         []map[string]any
	panelSends   []panelSend
	panelEdits   []panelEdit
	nextID       int
	failNextEdit bool
}

func (f *fakeDiscord) Send(payload map[string]any, wait bool) (map[string]any, error) {
	f.nextID++
	id := strconv.Itoa(f.nextID)
	if wait {
		f.panelSends = append(f.panelSends, panelSend{id: id, payload: payload})
		return map[string]any{"id": id, "channel_id": "test-channel"}, nil
	}
	f.sent = append(f.sent, payload)
	return map[string]any{"id": id}, nil
}

func (f *fakeDiscord) Edit(messageID string, payload map[string]any) (map[string]any, error) {
	if f.failNextEdit {
		f.failNextEdit = false
		return nil, &discord.Error{Status: 404, Message: "Unknown Message"}
	}
	f.panelEdits = append(f.panelEdits, panelEdit{messageID: messageID, payload: payload})
	return map[string]any{"id": messageID}, nil
}

func panelStatus(payload map[string]any) string {
	embeds := payload["embeds"].([]any)
	embed := embeds[0].(map[string]any)
	fields := embed["fields"].([]any)
	field := fields[0].(map[string]any)
	value, _ := field["value"].(string)
	return value
}

func panelName(payload map[string]any) string {
	embeds := payload["embeds"].([]any)
	embed := embeds[0].(map[string]any)
	fields := embed["fields"].([]any)
	field := fields[0].(map[string]any)
	name, _ := field["name"].(string)
	return name
}

func TestMonitorLifecycle(t *testing.T) {
	rootDir := t.TempDir()
	if err := store.WriteJSON(filepath.Join(rootDir, "accounts.json"), []map[string]any{
		{"username": "Cuenta_Prueba", "userId": 12345, "displayName": "Cuenta Prueba"},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		PollIntervalSeconds: 30,
		StatusPanel:         config.StatusPanel{Enabled: true},
		Notify: config.Notify{
			Connect:        true,
			Disconnect:     true,
			GameStart:      true,
			GameChange:     true,
			LeaveGame:      true,
			Studio:         true,
			StatusChange:   true,
			StartupSummary: true,
		},
	}
	robloxClient := &fakeRoblox{}
	discordClient := &fakeDiscord{}
	log := logger.New("error", "", "")
	monitorClient := New(rootDir, cfg, log, robloxClient, discordClient)

	if err := monitorClient.ReloadAccounts(); err != nil {
		t.Fatal(err)
	}
	if len(monitorClient.Accounts()) != 1 {
		t.Fatalf("se esperaba 1 cuenta, hay %d", len(monitorClient.Accounts()))
	}

	robloxClient.queue = append(robloxClient.queue, presence.RawPresence{
		UserID:           12345,
		UserPresenceType: 0,
	})
	if err := monitorClient.Poll(true); err != nil {
		t.Fatal(err)
	}
	if len(discordClient.sent) != 0 {
		t.Fatalf("el estado inicial no debe notificar, se enviaron %d", len(discordClient.sent))
	}
	if len(discordClient.panelSends) != 1 {
		t.Fatalf("el estado inicial debe crear el panel, hay %d", len(discordClient.panelSends))
	}
	if monitorClient.State().Panel.MessageID != discordClient.panelSends[0].id {
		t.Fatalf("el panel debe guardar el ID del mensaje")
	}
	if !strings.Contains(panelName(discordClient.panelSends[0].payload), "@Cuenta_Prueba") {
		t.Fatalf("el panel debe mostrar la cuenta")
	}

	robloxClient.queue = append(robloxClient.queue, presence.RawPresence{
		UserID:           12345,
		UserPresenceType: 1,
		LastLocation:     "Website",
	})
	if err := monitorClient.Poll(false); err != nil {
		t.Fatal(err)
	}
	if len(discordClient.sent) != 1 {
		t.Fatalf("el monitor debe notificar la conexión, hay %d avisos", len(discordClient.sent))
	}
	if len(discordClient.panelEdits) != 1 || discordClient.panelEdits[0].messageID != discordClient.panelSends[0].id {
		t.Fatalf("el panel debe editarse con el nuevo estado")
	}
	if !strings.Contains(panelStatus(discordClient.panelEdits[0].payload), "En línea desde") {
		t.Fatalf("el panel debe mostrar la hora de conexión")
	}

	robloxClient.queue = append(robloxClient.queue, presence.RawPresence{
		UserID:           12345,
		UserPresenceType: 2,
		PlaceID:          model.Ptr(int64(555)),
		RootPlaceID:      model.Ptr(int64(555)),
		UniverseID:       model.Ptr(int64(777)),
		GameID:           model.Ptr("job-123"),
		LastLocation:     "Juego Simulado",
	})
	if err := monitorClient.Poll(false); err != nil {
		t.Fatal(err)
	}
	if len(discordClient.sent) != 2 {
		t.Fatalf("el monitor debe notificar el inicio de juego, hay %d avisos", len(discordClient.sent))
	}
	latest := discordClient.panelEdits[len(discordClient.panelEdits)-1].payload
	if !strings.Contains(panelStatus(latest), "Jugando desde") {
		t.Fatalf("el panel debe mostrar la hora de inicio del juego")
	}

	robloxClient.queue = append(robloxClient.queue, presence.RawPresence{
		UserID:           12345,
		UserPresenceType: 0,
	})
	if err := monitorClient.Poll(false); err != nil {
		t.Fatal(err)
	}
	if len(discordClient.sent) != 3 {
		t.Fatalf("el monitor debe notificar la desconexión, hay %d avisos", len(discordClient.sent))
	}

	discordClient.failNextEdit = true
	robloxClient.queue = append(robloxClient.queue, presence.RawPresence{
		UserID:           12345,
		UserPresenceType: 0,
	})
	if err := monitorClient.Poll(false); err != nil {
		t.Fatal(err)
	}
	if len(discordClient.panelSends) != 2 {
		t.Fatalf("debe recrear el panel si el mensaje fue borrado, hay %d envíos", len(discordClient.panelSends))
	}
	if monitorClient.State().Panel.MessageID != discordClient.panelSends[1].id {
		t.Fatalf("debe guardar el nuevo ID del panel")
	}

	data, err := os.ReadFile(filepath.Join(rootDir, "data", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved model.State
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	user := saved.Users["12345"]
	if user == nil || user.PresenceType != 0 {
		t.Fatalf("el estado debe guardarse en disco: %+v", user)
	}
	if user.LastSessionMs == nil || *user.LastSessionMs <= 0 {
		t.Fatalf("debe guardar la duración de la sesión: %+v", user.LastSessionMs)
	}
	if saved.Panel == nil || saved.Panel.MessageID != discordClient.panelSends[1].id {
		t.Fatalf("debe persistir el panel de estado")
	}

	monitorClient.Stop()
}
