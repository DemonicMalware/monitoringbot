package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Account es una cuenta ya resuelta a un ID de Roblox.
type Account struct {
	UserID      int64
	Username    string
	DisplayName string
}

type presenceSource interface {
	FetchPresence(ctx context.Context, userIDs []int64) (map[int64]Presence, error)
}

type messageSender interface {
	Send(ctx context.Context, content string) error
	SendWait(ctx context.Context, content string) (string, error)
	Edit(ctx context.Context, messageID, content string) error
}

// Monitor consulta Roblox, compara estados y envía los avisos.
type Monitor struct {
	cfg       *Config
	loc       *time.Location
	source    presenceSource
	sender    messageSender
	state     *State
	statePath string
	accounts  []Account
	logger    *log.Logger
	now       func() time.Time

	firstPoll bool
	startedAt time.Time
	missed    map[int64]int
	statusKey map[int64]string

	mu                sync.RWMutex
	lastPollAt        time.Time
	lastSuccessAt     time.Time
	lastError         string
	consecutiveErrors int
	messagesSent      int
}

func NewMonitor(cfg *Config, source presenceSource, sender messageSender, state *State, accounts []Account, logger *log.Logger) *Monitor {
	now := time.Now
	return &Monitor{
		cfg:       cfg,
		loc:       cfg.Location(),
		source:    source,
		sender:    sender,
		state:     state,
		statePath: cfg.StatePath(),
		accounts:  accounts,
		logger:    logger,
		now:       now,
		firstPoll: true,
		startedAt: now().UTC(),
		missed:    make(map[int64]int),
		statusKey: make(map[int64]string),
	}
}

// Run consulta inmediatamente y luego cada intervalo hasta que se cancele el contexto.
func (m *Monitor) Run(ctx context.Context) {
	m.logger.Printf("Consultando la presencia cada %s.", m.cfg.PollInterval())
	_ = m.poll(ctx)

	ticker := time.NewTicker(m.cfg.PollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.poll(ctx)
		}
	}
}

func (m *Monitor) poll(ctx context.Context) error {
	ids := make([]int64, len(m.accounts))
	for i, account := range m.accounts {
		ids[i] = account.UserID
	}

	timeout := m.cfg.PollInterval()
	if timeout < 15*time.Second {
		timeout = 15 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	presences, err := m.source.FetchPresence(requestCtx, ids)
	if err != nil {
		m.recordError(err)
		m.logger.Printf("Error consultando Roblox: %v", err)
		return err
	}
	now := m.now().UTC()
	m.recordSuccess(now)
	m.apply(ctx, now, presences)
	return nil
}

func (m *Monitor) apply(ctx context.Context, now time.Time, presences map[int64]Presence) {
	var messages []string
	var statuses []statusUpdate
	changed := false

	m.mu.Lock()
	first := m.firstPoll

	for _, account := range m.accounts {
		key := strconv.FormatInt(account.UserID, 10)
		state := m.state.Accounts[key]
		if state == nil {
			state = &AccountState{UserID: account.UserID}
			m.state.Accounts[key] = state
			changed = true
		}

		presence, ok := presences[account.UserID]
		if !ok {
			if !state.Online && !first {
				continue
			}
			if state.Online {
				m.missed[account.UserID]++
				if m.missed[account.UserID] < 3 {
					m.logger.Printf("Aviso: Roblox no devolvió la presencia de %s; se conserva su estado anterior.", account.Username)
					continue
				}
				m.logger.Printf("Aviso: %s no aparece en la API tras %d consultas; se marca como desconectado.", account.Username, m.missed[account.UserID])
			}
			presence = Presence{UserID: account.UserID, UserPresenceType: 0}
		} else {
			m.missed[account.UserID] = 0
		}

		state.Username = account.Username
		state.DisplayName = account.DisplayName

		online := presence.UserPresenceType > 0
		playing := presence.UserPresenceType == 2
		previousOnline := state.Online
		previousType := state.PresenceType
		previousPlace := state.PlaceID

		if first {
			if online {
				if state.SessionStart == nil {
					state.SessionStart = pointerTime(now)
				}
				if playing {
					if state.PlayingSince == nil || previousPlace != presence.PlaceID {
						state.PlayingSince = pointerTime(now)
					}
				} else {
					state.PlayingSince = nil
				}
			} else {
				state.SessionStart = nil
				state.PlayingSince = nil
			}
			state.Online = online
			state.PresenceType = presence.UserPresenceType
			state.LastLocation = cleanText(presence.LastLocation)
			state.PlaceID = presence.PlaceID
			state.RootPlaceID = presence.RootPlaceID
			state.UniverseID = presence.UniverseID
			state.GameID = presence.GameID
			state.LastSeenAt = pointerTime(now)
			if update, ok := m.statusUpdateFor(account, state, now, true); ok {
				statuses = append(statuses, update)
			}
			changed = true
			continue
		}

		if online && state.SessionStart == nil {
			state.SessionStart = pointerTime(now)
		}

		switch {
		case !previousOnline && online:
			state.SessionStart = pointerTime(now)
			state.PlayingSince = nil
			if m.cfg.Notify.Connect {
				messages = append(messages, m.connectMessage(account.Username, presence, now))
			}
			if playing {
				state.PlayingSince = pointerTime(now)
			}
			state.LastChangedAt = pointerTime(now)
			changed = true

		case previousOnline && !online:
			if m.cfg.Notify.Disconnect {
				messages = append(messages, m.disconnectMessage(account.Username, state.SessionStart, now))
			}
			state.SessionStart = nil
			state.PlayingSince = nil
			state.LastChangedAt = pointerTime(now)
			changed = true

		case previousOnline && online:
			if playing {
				switch {
				case previousType != 2:
					state.PlayingSince = pointerTime(now)
					if m.cfg.Notify.GameStart {
						messages = append(messages, m.gameStartMessage(account.Username, presence, now))
					}
					state.LastChangedAt = pointerTime(now)
					changed = true
				case presence.PlaceID != 0 && presence.PlaceID != previousPlace:
					state.PlayingSince = pointerTime(now)
					if m.cfg.Notify.GameChange {
						messages = append(messages, m.gameChangeMessage(account.Username, presence, now))
					}
					state.LastChangedAt = pointerTime(now)
					changed = true
				}
			} else if previousType == 2 {
				if m.cfg.Notify.LeaveGame {
					messages = append(messages, m.leaveGameMessage(account.Username, state.LastLocation, state.PlayingSince, now))
				}
				state.PlayingSince = nil
				state.LastChangedAt = pointerTime(now)
				changed = true
			}
		}

		state.Online = online
		state.PresenceType = presence.UserPresenceType
		state.LastLocation = cleanText(presence.LastLocation)
		state.PlaceID = presence.PlaceID
		state.RootPlaceID = presence.RootPlaceID
		state.UniverseID = presence.UniverseID
		state.GameID = presence.GameID
		state.LastSeenAt = pointerTime(now)

		if update, ok := m.statusUpdateFor(account, state, now, false); ok {
			statuses = append(statuses, update)
		}
	}

	if first && m.cfg.Notify.StartupSummary {
		messages = append(messages, m.summaryMessage(now))
	}
	m.firstPoll = false
	if changed {
		m.state.UpdatedAt = now
	}
	m.mu.Unlock()

	if changed {
		if err := SaveState(m.statePath, m.state); err != nil {
			m.logger.Printf("No se pudo guardar el estado: %v", err)
		}
	}

	for _, message := range messages {
		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := m.sender.Send(sendCtx, message)
		cancel()
		if err != nil {
			m.logger.Printf("No se pudo enviar el aviso a Discord: %v", err)
			continue
		}
		m.mu.Lock()
		m.messagesSent++
		m.mu.Unlock()
	}

	m.publishStatuses(ctx, statuses)
}

// statusUpdate es un cambio pendiente del tablero de un usuario.
type statusUpdate struct {
	userID    int64
	username  string
	content   string
	messageID string
}

func (m *Monitor) statusUpdateFor(account Account, state *AccountState, now time.Time, first bool) (statusUpdate, bool) {
	signature := statusSignature(state)
	if !first && m.statusKey[account.UserID] == signature {
		return statusUpdate{}, false
	}
	m.statusKey[account.UserID] = signature
	return statusUpdate{
		userID:    account.UserID,
		username:  account.Username,
		content:   m.statusMessage(account.Username, state, now),
		messageID: state.StatusMessageID,
	}, true
}

// statusSignature detecta cambios visibles sin depender de la hora.
func statusSignature(state *AccountState) string {
	return fmt.Sprintf("%t|%d|%d|%s", state.Online, state.PresenceType, state.PlaceID, cleanText(state.LastLocation))
}

func (m *Monitor) statusMessage(username string, state *AccountState, now time.Time) string {
	lines := []string{"📊 Estado de " + username}
	switch {
	case !state.Online:
		lines = append(lines, "⚪ Desconectado")
	case state.PresenceType == 2:
		lines = append(lines, "🎮 Jugando: "+gameName(Presence{LastLocation: state.LastLocation}))
		if link := gameLink(Presence{PlaceID: state.PlaceID, RootPlaceID: state.RootPlaceID}); link != "" {
			lines = append(lines, "🔗 "+link)
		}
	case state.PresenceType == 3:
		lines = append(lines, "🛠️ Activo en Roblox Studio")
	default:
		lines = append(lines, "🟢 Activo (en línea)")
	}
	lines = append(lines, "📅 Actualizado: "+m.formatMoment(now))
	return strings.Join(lines, "\n")
}

// publishStatuses crea o edita el mensaje de estado de cada usuario.
func (m *Monitor) publishStatuses(ctx context.Context, updates []statusUpdate) {
	if len(updates) == 0 {
		return
	}
	dirty := false

	for _, update := range updates {
		messageID := strings.TrimSpace(update.messageID)
		if messageID != "" {
			editCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := m.sender.Edit(editCtx, messageID, update.content)
			cancel()
			if err == nil {
				continue
			}
			var httpErr *DiscordHTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
				m.logger.Printf("No se pudo actualizar el estado de %s: %v", update.username, err)
				m.forgetStatusKey(update.userID)
				continue
			}
			m.logger.Printf("El mensaje de estado de %s ya no existe; se crea uno nuevo.", update.username)
		}

		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		newID, err := m.sender.SendWait(sendCtx, update.content)
		cancel()
		if err != nil {
			m.logger.Printf("No se pudo crear el mensaje de estado de %s: %v", update.username, err)
			m.forgetStatusKey(update.userID)
			continue
		}
		if newID == "" {
			continue // dry-run
		}

		m.mu.Lock()
		if state := m.state.Accounts[strconv.FormatInt(update.userID, 10)]; state != nil {
			state.StatusMessageID = newID
			dirty = true
		}
		m.mu.Unlock()
	}

	if dirty {
		m.mu.Lock()
		m.state.UpdatedAt = m.now().UTC()
		err := SaveState(m.statePath, m.state)
		m.mu.Unlock()
		if err != nil {
			m.logger.Printf("No se pudo guardar el estado: %v", err)
		}
	}
}

func (m *Monitor) forgetStatusKey(userID int64) {
	m.mu.Lock()
	delete(m.statusKey, userID)
	m.mu.Unlock()
}

func (m *Monitor) recordError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastPollAt = m.now().UTC()
	m.consecutiveErrors++
	if err != nil {
		m.lastError = err.Error()
	}
}

func (m *Monitor) recordSuccess(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastPollAt = now
	m.lastSuccessAt = now
	m.lastError = ""
	m.consecutiveErrors = 0
}

// Save guarda el estado actual. Se usa al detener el bot.
func (m *Monitor) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.UpdatedAt = m.now().UTC()
	return SaveState(m.statePath, m.state)
}

type HealthAccount struct {
	Username string `json:"username"`
	UserID   int64  `json:"userId"`
	Online   bool   `json:"online"`
	Status   string `json:"status"`
}

type HealthSnapshot struct {
	Status            string          `json:"status"`
	UptimeSeconds     int64           `json:"uptimeSeconds"`
	LastPollAt        string          `json:"lastPollAt,omitempty"`
	LastSuccessAt     string          `json:"lastSuccessAt,omitempty"`
	LastError         string          `json:"lastError,omitempty"`
	ConsecutiveErrors int             `json:"consecutiveErrors"`
	MessagesSent      int             `json:"messagesSent"`
	Accounts          []HealthAccount `json:"accounts"`
}

func (m *Monitor) Snapshot() HealthSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshot := HealthSnapshot{
		UptimeSeconds:     int64(m.now().Sub(m.startedAt).Seconds()),
		LastError:         m.lastError,
		ConsecutiveErrors: m.consecutiveErrors,
		MessagesSent:      m.messagesSent,
		Accounts:          make([]HealthAccount, 0, len(m.accounts)),
	}
	if !m.lastPollAt.IsZero() {
		snapshot.LastPollAt = m.lastPollAt.Format(time.RFC3339)
	}
	if !m.lastSuccessAt.IsZero() {
		snapshot.LastSuccessAt = m.lastSuccessAt.Format(time.RFC3339)
	}

	switch {
	case m.lastSuccessAt.IsZero():
		snapshot.Status = "starting"
	case m.consecutiveErrors > 0:
		snapshot.Status = "degraded"
	case m.now().Sub(m.lastSuccessAt) > 3*m.cfg.PollInterval():
		snapshot.Status = "degraded"
	default:
		snapshot.Status = "ok"
	}

	for _, account := range m.accounts {
		state := m.state.Accounts[strconv.FormatInt(account.UserID, 10)]
		item := HealthAccount{
			Username: account.Username,
			UserID:   account.UserID,
			Status:   "sin datos",
		}
		if state != nil && state.LastSeenAt != nil {
			item.Online = state.Online
			item.Status = presenceLabel(state.PresenceType, state.LastLocation)
		}
		snapshot.Accounts = append(snapshot.Accounts, item)
	}
	return snapshot
}

func (m *Monitor) connectMessage(username string, presence Presence, now time.Time) string {
	lines := []string{fmt.Sprintf("🟢 %s se conectó a Roblox", username)}
	switch presence.UserPresenceType {
	case 2:
		lines = append(lines, "🎮 Jugando: "+gameName(presence))
		if link := gameLink(presence); link != "" {
			lines = append(lines, "🔗 "+link)
		}
	case 3:
		lines = append(lines, "🛠️ En Roblox Studio")
	default:
		lines = append(lines, "🌐 En línea en el sitio web")
	}
	lines = append(lines, "📅 Fecha y hora: "+m.formatMoment(now))
	return strings.Join(lines, "\n")
}

func (m *Monitor) gameStartMessage(username string, presence Presence, now time.Time) string {
	lines := []string{fmt.Sprintf("🎮 %s está jugando: %s", username, gameName(presence))}
	if link := gameLink(presence); link != "" {
		lines = append(lines, "🔗 "+link)
	}
	lines = append(lines, "📅 Fecha y hora: "+m.formatMoment(now))
	return strings.Join(lines, "\n")
}

func (m *Monitor) gameChangeMessage(username string, presence Presence, now time.Time) string {
	lines := []string{fmt.Sprintf("🎮 %s cambió de juego: %s", username, gameName(presence))}
	if link := gameLink(presence); link != "" {
		lines = append(lines, "🔗 "+link)
	}
	lines = append(lines, "📅 Fecha y hora: "+m.formatMoment(now))
	return strings.Join(lines, "\n")
}

func (m *Monitor) leaveGameMessage(username string, location string, playingSince *time.Time, now time.Time) string {
	lines := []string{fmt.Sprintf("🚪 %s salió del juego: %s", username, gameName(Presence{LastLocation: location}))}
	if playingSince != nil {
		lines = append(lines, "⏱️ Tiempo en el juego: "+formatDuration(now.Sub(*playingSince)))
	}
	lines = append(lines, "📅 Fecha y hora: "+m.formatMoment(now))
	return strings.Join(lines, "\n")
}

func (m *Monitor) disconnectMessage(username string, sessionStart *time.Time, now time.Time) string {
	lines := []string{fmt.Sprintf("🔴 %s se desconectó de Roblox", username)}
	lines = append(lines, "📅 Fecha y hora: "+m.formatMoment(now))
	if sessionStart != nil {
		lines = append(lines, "⏱️ Duración de la sesión: "+formatDuration(now.Sub(*sessionStart)))
	}
	return strings.Join(lines, "\n")
}

func (m *Monitor) summaryMessage(now time.Time) string {
	lines := []string{
		"🤖 Monitor de presencia iniciado",
		"👥 Usuarios: " + m.accountNames(),
	}
	for _, account := range m.accounts {
		state := m.state.Accounts[strconv.FormatInt(account.UserID, 10)]
		status := "sin datos"
		online := false
		if state != nil && state.LastSeenAt != nil {
			online = state.Online
			status = presenceLabel(state.PresenceType, state.LastLocation)
		}
		emoji := "⚪"
		if online {
			emoji = "🟢"
		}
		lines = append(lines, fmt.Sprintf("%s %s: %s", emoji, account.Username, status))
	}
	lines = append(lines, "📅 Fecha y hora: "+m.formatMoment(now))
	return strings.Join(lines, "\n")
}

func (m *Monitor) accountNames() string {
	names := make([]string, 0, len(m.accounts))
	for _, account := range m.accounts {
		names = append(names, account.Username)
	}
	return strings.Join(names, ", ")
}

func (m *Monitor) formatMoment(value time.Time) string {
	return formatMomentFor(m.cfg, value)
}

func formatMomentFor(cfg *Config, value time.Time) string {
	location := cfg.Location()
	value = value.In(location)
	label := strings.TrimSpace(cfg.TimeZoneLabel)
	if label == "" {
		label = location.String()
	}
	return value.Format("02/01/2006 15:04:05") + " (" + label + ")"
}

func pointerTime(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func cleanText(value string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(value), " "))
}

func gameName(presence Presence) string {
	name := cleanText(presence.LastLocation)
	if name == "" {
		return "Juego desconocido"
	}
	return limitRunes(name, 100)
}

func gameLink(presence Presence) string {
	placeID := presence.RootPlaceID
	if placeID == 0 {
		placeID = presence.PlaceID
	}
	if placeID == 0 {
		return ""
	}
	return fmt.Sprintf("https://www.roblox.com/games/%d", placeID)
}

func presenceLabel(presenceType int, location string) string {
	switch presenceType {
	case 0:
		return "desconectado"
	case 1:
		return "en línea (sitio web)"
	case 2:
		name := cleanText(location)
		if name == "" {
			return "jugando"
		}
		return "jugando: " + limitRunes(name, 80)
	case 3:
		return "en Roblox Studio"
	default:
		return fmt.Sprintf("en línea (tipo %d)", presenceType)
	}
}

func formatDuration(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	if value < time.Second {
		return "menos de 1s"
	}
	value = value.Round(time.Second)
	days := int(value / (24 * time.Hour))
	hours := int(value/time.Hour) % 24
	minutes := int(value/time.Minute) % 60
	seconds := int(value/time.Second) % 60

	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	} else if seconds > 0 && days == 0 && len(parts) < 3 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	return strings.Join(parts, " ")
}
