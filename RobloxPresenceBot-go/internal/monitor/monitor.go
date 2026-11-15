package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"roblox-presence-bot/internal/config"
	"roblox-presence-bot/internal/discord"
	"roblox-presence-bot/internal/events"
	"roblox-presence-bot/internal/format"
	"roblox-presence-bot/internal/logger"
	"roblox-presence-bot/internal/model"
	"roblox-presence-bot/internal/presence"
	"roblox-presence-bot/internal/store"
)

// Roblox agrupa las consultas que necesita el monitor.
type Roblox interface {
	ResolveUsernames(usernames []string) (map[string]model.ResolvedUser, error)
	GetPresences(userIDs []int64) ([]presence.RawPresence, error)
	GetGameByUniverse(universeID int64, fallbackPlaceID *int64) (*model.Game, error)
	GetPlaceDetails(placeID int64) (*model.Game, error)
	GetAvatarURL(userID int64) (string, error)
}

// Discord envía y edita los mensajes del webhook.
type Discord interface {
	Send(payload map[string]any, wait bool) (map[string]any, error)
	Edit(messageID string, payload map[string]any) (map[string]any, error)
}

// Monitor consulta la presencia periódicamente y mantiene el panel fijo.
type Monitor struct {
	rootDir string
	config  *config.Config
	logger  *logger.Logger
	roblox  Roblox
	discord Discord

	accountsFile string
	stateFile    string
	eventsFile   string

	state         *model.State
	accounts      []model.Account
	accountsMtime int64
	stopped       bool
	polling       bool
}

func New(rootDir string, cfg *config.Config, log *logger.Logger, robloxClient Roblox, discordClient Discord) *Monitor {
	stateFile := filepath.Join(rootDir, "data", "state.json")
	monitor := &Monitor{
		rootDir:       rootDir,
		config:        cfg,
		logger:        log,
		roblox:        robloxClient,
		discord:       discordClient,
		accountsFile:  filepath.Join(rootDir, "accounts.json"),
		stateFile:     stateFile,
		eventsFile:    filepath.Join(rootDir, "data", "events.jsonl"),
		state:         store.LoadState(stateFile, log),
		accountsMtime: -1,
	}
	if monitor.state.Panel == nil {
		monitor.state.Panel = &model.Panel{}
	}
	return monitor
}

func (m *Monitor) State() *model.State {
	return m.state
}

func (m *Monitor) Accounts() []model.Account {
	return m.accounts
}

// Run carga las cuentas, hace la consulta inicial y programa los ciclos.
func (m *Monitor) Run(ctx context.Context) {
	_ = m.ReloadAccounts()
	m.logger.Infof("Cuentas a vigilar: %d", len(m.accounts))

	initialOK := true
	if err := m.Poll(true); err != nil {
		initialOK = false
		m.logger.Errorf("No se pudo obtener el estado inicial; se reintentará en el siguiente ciclo: %v", err)
	}
	if initialOK && m.config.Notify.StartupSummary {
		m.SendStartupSummary()
	}

	delay := time.Duration(max(5, m.config.PollInterval())) * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		if m.stopped {
			return
		}

		startedAt := time.Now()
		if err := m.Poll(false); err != nil {
			m.logger.Errorf("Error durante el ciclo de monitoreo: %v", err)
		}
		elapsed := time.Since(startedAt)
		delay = time.Duration(m.config.PollInterval())*time.Second - elapsed
		if delay < 2*time.Second {
			delay = 2 * time.Second
		}
	}
}

func (m *Monitor) Stop() {
	if m.stopped {
		return
	}
	m.stopped = true
	m.SaveState()
}

// ReloadAccounts carga accounts.json y resuelve los IDs que falten.
func (m *Monitor) ReloadAccounts() error {
	mtime := store.FileMtime(m.accountsFile)
	if mtime == m.accountsMtime {
		return nil
	}
	m.accountsMtime = mtime

	accounts, err := store.LoadAccounts(m.accountsFile)
	if err != nil {
		m.logger.Errorf("No se pudo leer %s: %v", m.accountsFile, err)
		return err
	}

	unresolved := make([]string, 0)
	for _, account := range accounts {
		if account.ID() == 0 {
			unresolved = append(unresolved, account.Username)
		}
	}
	if len(unresolved) > 0 {
		resolved, resolveErr := m.roblox.ResolveUsernames(unresolved)
		if resolveErr != nil {
			m.logger.Errorf("No se pudieron resolver las cuentas: %v", resolveErr)
			// Reintentar en el siguiente ciclo.
			m.accountsMtime = -1
		} else {
			for index := range accounts {
				if accounts[index].ID() != 0 {
					continue
				}
				found, ok := resolved[strings.ToLower(accounts[index].Username)]
				if !ok || found.ID <= 0 {
					m.logger.Warnf("No se encontró la cuenta %q en Roblox. Se ignorará.", accounts[index].Username)
					continue
				}
				accounts[index].UserID = model.Ptr(found.ID)
				accounts[index].Username = found.Name
				display := found.DisplayName
				if display == "" {
					display = found.Name
				}
				accounts[index].DisplayName = model.Ptr(display)
				m.logger.Infof("Cuenta resuelta: @%s (ID %d)", found.Name, found.ID)
			}

			// Se guardan los IDs resueltos para no repetir la consulta al reiniciar.
			// Las cuentas no encontradas se conservan para poder corregirlas.
			if len(accounts) > 0 {
				if saveErr := store.SaveAccounts(m.accountsFile, accounts); saveErr != nil {
					m.logger.Warnf("No se pudo guardar accounts.json con los IDs resueltos: %v", saveErr)
				} else {
					m.accountsMtime = store.FileMtime(m.accountsFile)
				}
			}
		}
	}

	byID := make(map[int64]int)
	filtered := make([]model.Account, 0, len(accounts))
	for _, account := range accounts {
		id := account.ID()
		if id <= 0 {
			continue
		}
		if index, exists := byID[id]; exists {
			filtered[index] = account
			continue
		}
		byID[id] = len(filtered)
		filtered = append(filtered, account)
	}
	m.accounts = filtered

	if len(m.accounts) > 0 && len(m.accounts) <= 12 {
		names := make([]string, 0, len(m.accounts))
		for _, account := range m.accounts {
			names = append(names, "@"+account.Username)
		}
		m.logger.Infof("Vigilando: %s", strings.Join(names, ", "))
	}
	return nil
}

// Poll consulta todas las cuentas, guarda el estado y envía los avisos.
func (m *Monitor) Poll(initial bool) error {
	if m.polling {
		return nil
	}
	m.polling = true
	defer func() { m.polling = false }()

	if !initial {
		_ = m.ReloadAccounts()
	}

	if len(m.accounts) == 0 {
		m.logger.Warnf("No hay cuentas con ID válido. Usa: presence-bot add <usuario>")
		m.SaveState()
		m.UpdateStatusPanel()
		return nil
	}

	now := time.Now().UTC()
	userIDs := make([]int64, 0, len(m.accounts))
	for _, account := range m.accounts {
		userIDs = append(userIDs, account.ID())
	}

	presences, err := m.roblox.GetPresences(userIDs)
	if err != nil {
		return err
	}
	byUserID := make(map[int64]presence.RawPresence, len(presences))
	for _, raw := range presences {
		byUserID[raw.UserID] = raw
	}

	eventList := make([]model.Event, 0)
	processed := 0

	for _, account := range m.accounts {
		raw, ok := byUserID[account.ID()]
		if !ok {
			m.logger.Warnf("Roblox no devolvió presencia para @%s (ID %d).", account.Username, account.ID())
			continue
		}

		next := presence.NormalizePresence(raw)
		if next.PresenceType == 2 {
			next.Game = presence.ResolveGameDetails(m.roblox, next, m.logger)
		}

		key := strconv.FormatInt(account.ID(), 10)
		previous := m.state.Users[key]

		if !initial && previous != nil {
			if event := presence.DetectEvent(previous, next, now); event != nil {
				event.Account = account
				eventList = append(eventList, *event)
			}
		}

		m.state.Users[key] = presence.UpdateState(account, previous, next, now)
		processed++
	}

	m.state.UpdatedAt = model.Ptr(format.ISOMillis(now))
	m.SaveState()

	suffix := ""
	if initial {
		suffix = " (estado inicial)"
	}
	m.logger.Infof("Presencia actualizada: %d/%d cuenta(s)%s.", processed, len(m.accounts), suffix)

	if !initial {
		for _, event := range eventList {
			m.Notify(event)
		}
	}

	m.UpdateStatusPanel()
	return nil
}

// UpdateStatusPanel crea o edita el mensaje fijo con el estado de las cuentas.
func (m *Monitor) UpdateStatusPanel() {
	if !m.config.StatusPanel.Enabled {
		return
	}
	// Sin cuentas no se crea un panel nuevo, pero sí se actualiza el existente.
	if len(m.accounts) == 0 && (m.state.Panel == nil || m.state.Panel.MessageID == "") {
		return
	}

	now := time.Now().UTC()
	entries := make([]events.Entry, 0, len(m.accounts))
	for _, account := range m.accounts {
		entries = append(entries, events.Entry{
			Account: account,
			State:   m.state.Users[strconv.FormatInt(account.ID(), 10)],
		})
	}

	payload := events.BuildStatusPayload(entries, m.config, now)
	if m.state.Panel == nil {
		m.state.Panel = &model.Panel{}
	}
	panel := m.state.Panel

	if panel.MessageID != "" {
		if _, err := m.discord.Edit(panel.MessageID, payload); err == nil {
			panel.UpdatedAt = format.ISOMillis(now)
			return
		} else {
			var discordErr *discord.Error
			if !errors.As(err, &discordErr) || (discordErr.Status != 404 && discordErr.Status != 403) {
				m.logger.Errorf("No se pudo actualizar el panel de estado: %v", err)
				return
			}
			m.logger.Warnf("El panel de estado ya no existe o pertenece a otro webhook. Se publicará uno nuevo.")
			panel.MessageID = ""
			panel.ChannelID = ""
		}
	}

	message, err := m.discord.Send(payload, true)
	if err != nil {
		m.logger.Errorf("No se pudo publicar el panel de estado: %v", err)
		return
	}

	messageID := stringValue(message, "id")
	channelID := stringValue(message, "channel_id")
	dryRun, _ := message["dryRun"].(bool)
	if messageID != "" && !dryRun {
		panel.MessageID = messageID
		panel.ChannelID = channelID
		panel.CreatedAt = format.ISOMillis(now)
		panel.UpdatedAt = format.ISOMillis(now)
		m.logger.Infof("Panel de estado publicado (mensaje %s).", panel.MessageID)
		m.SaveState()
	}
}

// Notify envía el aviso de un evento y lo deja en el historial.
func (m *Monitor) Notify(event model.Event) {
	m.AppendEvent(event)

	if !m.notifyEnabled(event.Type) {
		return
	}

	avatarURL := ""
	if value, err := m.roblox.GetAvatarURL(event.Account.ID()); err == nil {
		avatarURL = value
	}

	payload := events.BuildEventPayload(event, avatarURL, m.config)
	if payload == nil {
		return
	}

	if _, err := m.discord.Send(payload, false); err != nil {
		m.logger.Errorf(
			"No se pudo enviar el aviso %s de @%s: %v",
			event.Type,
			event.Account.Username,
			err,
		)
		return
	}
	m.logger.Infof(
		"Aviso enviado: %s · @%s (%s)",
		event.Type,
		event.Account.Username,
		format.ISOMillis(event.At),
	)
}

func (m *Monitor) notifyEnabled(eventType string) bool {
	flags := map[string]string{
		"connected":      "connect",
		"disconnected":   "disconnect",
		"game_started":   "gameStart",
		"game_changed":   "gameChange",
		"left_game":      "leaveGame",
		"studio_opened":  "studio",
		"studio_closed":  "studio",
		"status_changed": "statusChange",
	}
	flag, ok := flags[eventType]
	if !ok {
		return true
	}
	notify := m.config.Notify
	switch flag {
	case "connect":
		return notify.Connect
	case "disconnect":
		return notify.Disconnect
	case "gameStart":
		return notify.GameStart
	case "gameChange":
		return notify.GameChange
	case "leaveGame":
		return notify.LeaveGame
	case "studio":
		return notify.Studio
	case "statusChange":
		return notify.StatusChange
	}
	return true
}

func (m *Monitor) SendStartupSummary() {
	entries := make([]events.Entry, 0, len(m.accounts))
	for _, account := range m.accounts {
		entries = append(entries, events.Entry{
			Account: account,
			State:   m.state.Users[strconv.FormatInt(account.ID(), 10)],
		})
	}

	if _, err := m.discord.Send(events.BuildStartupPayload(entries, m.config, time.Now().UTC()), false); err != nil {
		m.logger.Errorf("No se pudo enviar el resumen inicial: %v", err)
		return
	}
	if m.config.IsDryRun() {
		m.logger.Infof("Resumen inicial generado en modo dry-run (no enviado).")
	} else {
		m.logger.Infof("Resumen inicial enviado a Discord.")
	}
}

// AppendEvent guarda un historial append-only de los cambios detectados.
func (m *Monitor) AppendEvent(event model.Event) {
	record := map[string]any{
		"detectedAt":   format.ISOMillis(event.At),
		"type":         event.Type,
		"userId":       event.Account.ID(),
		"username":     event.Account.Username,
		"displayName":  event.Account.DisplayName,
		"previousType": nil,
		"presenceType": nil,
		"game":         nil,
		"sessionMs":    nil,
		"activityMs":   nil,
		"lastOnline":   nil,
	}
	if event.Prev != nil {
		record["previousType"] = event.Prev.PresenceType
	}
	if event.Next != nil {
		record["presenceType"] = event.Next.PresenceType
	}
	if event.Next != nil && event.Next.Game != nil {
		record["game"] = event.Next.Game
	} else if event.Prev != nil && event.Prev.Game != nil {
		record["game"] = event.Prev.Game
	}
	if event.SessionMs != nil {
		record["sessionMs"] = *event.SessionMs
	}
	if event.ActivityMs != nil {
		record["activityMs"] = *event.ActivityMs
	}
	if event.LastOnline != nil {
		if parsed, ok := format.ParseTime(*event.LastOnline); ok {
			record["lastOnline"] = format.ISOMillis(parsed)
		}
	}

	data, err := json.Marshal(record)
	if err != nil {
		m.logger.Warnf("No se pudo escribir el historial de eventos: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.eventsFile), 0o755); err != nil {
		m.logger.Warnf("No se pudo escribir el historial de eventos: %v", err)
		return
	}
	file, err := os.OpenFile(m.eventsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		m.logger.Warnf("No se pudo escribir el historial de eventos: %v", err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		m.logger.Warnf("No se pudo escribir el historial de eventos: %v", err)
	}
}

func (m *Monitor) SaveState() {
	if err := store.WriteJSON(m.stateFile, m.state); err != nil {
		m.logger.Errorf("No se pudo guardar el estado: %v", err)
	}
}

func stringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	switch value := values[key].(type) {
	case string:
		return value
	case float64:
		return strconv.FormatInt(int64(value), 10)
	case json.Number:
		return value.String()
	default:
		return ""
	}
}
