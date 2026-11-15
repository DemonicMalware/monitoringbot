package events

import (
	"fmt"
	"strings"
	"time"

	"roblox-presence-bot/internal/config"
	"roblox-presence-bot/internal/format"
	"roblox-presence-bot/internal/model"
)

const (
	colorGreen   = 0x57f287
	colorRed     = 0xed4245
	colorBlurple = 0x5865f2
	colorYellow  = 0xfee75c
	colorGray    = 0x99aab5
	colorOrange  = 0xe67e22
)

// Entry asocia una cuenta con su último estado guardado.
type Entry struct {
	Account model.Account
	State   *model.UserState
}

func field(name, value string, inline bool) map[string]any {
	return map[string]any{"name": name, "value": value, "inline": inline}
}

func baseEmbed(account model.Account, at time.Time, avatarURL string) map[string]any {
	author := map[string]any{"name": format.Truncate(account.Display(), 250)}
	if avatarURL != "" {
		author["icon_url"] = avatarURL
	}
	return map[string]any{
		"author":    author,
		"footer":    map[string]any{"text": format.Truncate("Roblox Presence Monitor · "+format.UTCString(at), 2048)},
		"timestamp": format.ISOMillis(at),
	}
}

func accountField(account model.Account) map[string]any {
	return field(
		"👤 Cuenta",
		fmt.Sprintf("`%s`\nID: %d", account.Username, account.ID()),
		true,
	)
}

func timeField(at time.Time, label string) map[string]any {
	return field(label, format.DiscordTime(at, "F")+"\n"+format.DiscordTime(at, "R"), false)
}

func gameField(game *model.Game, label string) map[string]any {
	if game == nil || game.Name == "" {
		value := "Detalles no disponibles (falta la cookie de Roblox o la cuenta es privada)"
		if game != nil && game.PlaceID != nil {
			value = fmt.Sprintf("Detalles no disponibles\nLugar: `%d`", *game.PlaceID)
		}
		return field(label, value, false)
	}

	name := format.EscapeMarkdown(game.Name)
	if game.URL != "" {
		name = fmt.Sprintf("[%s](%s)", name, game.URL)
	}
	lines := []string{name}
	placeID := game.RootPlaceID
	if placeID == nil {
		placeID = game.PlaceID
	}
	if placeID != nil {
		lines = append(lines, fmt.Sprintf("Lugar: `%d`", *placeID))
	}
	if game.GameID != "" {
		lines = append(lines, fmt.Sprintf("Servidor: `%s`", game.GameID))
	}
	return field(label, format.Truncate(strings.Join(lines, "\n"), 1024), true)
}

func gameButtons(game *model.Game) []any {
	if game == nil || game.URL == "" {
		return nil
	}
	buttons := []any{
		map[string]any{"type": 2, "style": 5, "label": "Ver juego", "url": game.URL},
	}
	if game.JoinURL != "" {
		buttons = append(buttons, map[string]any{"type": 2, "style": 5, "label": "Unirse", "url": game.JoinURL})
	}
	return []any{map[string]any{"type": 1, "components": buttons}}
}

func addThumbnail(embed map[string]any, game *model.Game) {
	if game != nil && game.IconURL != "" {
		embed["thumbnail"] = map[string]any{"url": game.IconURL}
	}
}

func withMention(payload map[string]any, cfg *config.Config) map[string]any {
	if cfg != nil && cfg.Mention != "" {
		payload["content"] = cfg.Mention
	}
	return payload
}

func parsePtrTime(value *string) (time.Time, bool) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return time.Time{}, false
	}
	return format.ParseTime(*value)
}

func plausibleOfflineTime(raw *string, now time.Time) *time.Time {
	parsed, ok := parsePtrTime(raw)
	if !ok {
		return nil
	}
	// Solo se usa si parece la hora real de esta desconexión.
	if parsed.After(now.Add(5 * time.Second)) {
		return nil
	}
	if parsed.Before(now.Add(-15 * time.Minute)) {
		return nil
	}
	return &parsed
}

func durationValue(ms *int64) string {
	return format.FormatDuration(ms)
}

// BuildEventPayload construye el mensaje de Discord para un cambio detectado.
func BuildEventPayload(event model.Event, avatarURL string, cfg *config.Config) map[string]any {
	account := event.Account
	name := "**" + format.EscapeMarkdown(account.Display()) + "**"
	embed := baseEmbed(account, event.At, avatarURL)

	switch event.Type {
	case "connected":
		next := event.Next
		if next == nil {
			return nil
		}
		title := "🟢 Se conectó a Roblox"
		switch next.PresenceType {
		case 2:
			title = "🟢 Se conectó y está jugando"
		case 3:
			title = "🟢 Se conectó a Roblox Studio"
		}

		fields := []any{
			accountField(account),
			field("📌 Estado", format.PresenceLabel(next), true),
		}
		if next.Game != nil {
			fields = append(fields, gameField(next.Game, "🎮 Juego"))
		}
		fields = append(fields, timeField(event.At, "🕒 Fecha y hora"))

		embed["title"] = title
		embed["description"] = name + " se conectó a Roblox."
		embed["color"] = colorGreen
		embed["fields"] = fields
		addThumbnail(embed, next.Game)

	case "disconnected":
		prev := event.Prev
		if prev == nil {
			return nil
		}
		var lastOnline *string
		if event.Next != nil {
			lastOnline = event.Next.LastOnline
		}
		offlineAt := plausibleOfflineTime(lastOnline, event.At)

		sessionStart, hasSessionStart := parsePtrTime(prev.SessionStart)
		var sessionMs *int64
		if offlineAt != nil && hasSessionStart && !offlineAt.Before(sessionStart) {
			ms := offlineAt.Sub(sessionStart).Milliseconds()
			sessionMs = &ms
		} else if hasSessionStart {
			ms := event.At.Sub(sessionStart).Milliseconds()
			if ms < 0 {
				ms = 0
			}
			sessionMs = &ms
		} else if since, ok := parsePtrTime(&prev.Since); ok {
			ms := event.At.Sub(since).Milliseconds()
			if ms < 0 {
				ms = 0
			}
			sessionMs = &ms
		}

		fields := []any{
			accountField(account),
			field("📌 Última actividad", format.PresenceLabel(prev), true),
			field("⏱️ Tiempo en línea", durationValue(sessionMs), true),
		}
		if offlineAt != nil {
			fields = append(fields, field(
				"🕒 Hora de desconexión (Roblox)",
				format.DiscordTime(*offlineAt, "F")+"\n"+format.DiscordTime(*offlineAt, "R"),
				false,
			))
		}
		fields = append(fields, timeField(event.At, "🕒 Detectado por el bot"))

		embed["title"] = "🔴 Se desconectó de Roblox"
		embed["description"] = name + " se desconectó."
		embed["color"] = colorRed
		embed["fields"] = fields

	case "game_started":
		next := event.Next
		if next == nil {
			return nil
		}
		game := next.Game
		title := "🎮 Entró a un juego"
		description := name + " entró a un juego, pero Roblox no compartió el nombre (configura la cookie para verlo)."
		if game != nil && game.Name != "" {
			title = "🎮 Empezó a jugar"
			description = fmt.Sprintf("%s empezó a jugar **%s**.", name, format.EscapeMarkdown(game.Name))
		}

		embed["title"] = title
		embed["description"] = description
		embed["color"] = colorBlurple
		embed["fields"] = []any{
			accountField(account),
			gameField(game, "🎮 Juego"),
			timeField(event.At, "🕒 Fecha y hora"),
		}
		addThumbnail(embed, game)

	case "game_changed":
		prev := event.Prev
		next := event.Next
		if prev == nil || next == nil {
			return nil
		}
		embed["title"] = "🔄 Cambió de juego"
		embed["description"] = name + " cambió de juego."
		embed["color"] = colorYellow
		embed["fields"] = []any{
			accountField(account),
			gameField(prev.Game, "🎮 Juego anterior"),
			gameField(next.Game, "🎮 Juego nuevo"),
			timeField(event.At, "🕒 Fecha y hora"),
		}
		addThumbnail(embed, next.Game)

	case "left_game":
		prev := event.Prev
		if prev == nil {
			return nil
		}
		game := prev.Game
		description := name + " dejó de jugar"
		if game != nil && game.Name != "" {
			description += " **" + format.EscapeMarkdown(game.Name) + "**"
		}
		description += "."

		fields := []any{accountField(account)}
		if game != nil {
			fields = append(fields, gameField(game, "🎮 Juego"))
		}
		fields = append(fields,
			field("⏱️ Tiempo en el juego", durationValue(event.ActivityMs), true),
			timeField(event.At, "🕒 Fecha y hora"),
		)

		embed["title"] = "🚪 Salió del juego"
		embed["description"] = description
		embed["color"] = colorGray
		embed["fields"] = fields

	case "studio_opened":
		embed["title"] = "🛠️ Abrió Roblox Studio"
		embed["description"] = name + " abrió Roblox Studio."
		embed["color"] = colorOrange
		embed["fields"] = []any{
			accountField(account),
			timeField(event.At, "🕒 Fecha y hora"),
		}

	case "studio_closed":
		embed["title"] = "🛠️ Cerró Roblox Studio"
		embed["description"] = name + " cerró Roblox Studio."
		embed["color"] = colorGray
		embed["fields"] = []any{
			accountField(account),
			field("⏱️ Tiempo en Studio", durationValue(event.ActivityMs), true),
			timeField(event.At, "🕒 Fecha y hora"),
		}

	case "status_changed":
		prev := event.Prev
		next := event.Next
		if prev == nil || next == nil {
			return nil
		}
		embed["title"] = "🔄 Cambió de estado"
		embed["description"] = name + " cambió de estado."
		embed["color"] = colorYellow
		embed["fields"] = []any{
			accountField(account),
			field("📌 Estado anterior", format.PresenceLabel(prev), true),
			field("📌 Estado nuevo", format.PresenceLabel(next), true),
			timeField(event.At, "🕒 Fecha y hora"),
		}

	default:
		return nil
	}

	payload := map[string]any{"embeds": []any{embed}}
	switch event.Type {
	case "connected", "game_started", "game_changed":
		next := event.Next
		if next != nil {
			if components := gameButtons(next.Game); components != nil {
				payload["components"] = components
			}
		}
	}
	return withMention(payload, cfg)
}

func statusEmoji(state *model.UserState) string {
	if state == nil {
		return "⚪"
	}
	switch state.PresenceType {
	case 2:
		return "🎮"
	case 3:
		return "🛠️"
	case 1:
		return "🟢"
	default:
		return "⚪"
	}
}

func statusTimeLine(label, raw string) string {
	parsed, ok := format.ParseTime(raw)
	if !ok {
		return label + ": fecha desconocida"
	}
	return fmt.Sprintf("%s: %s (%s)", label, format.DiscordTime(parsed, "F"), format.DiscordTime(parsed, "R"))
}

func statusAccountField(account model.Account, state *model.UserState) map[string]any {
	lines := []string{}

	if state == nil {
		lines = append(lines, "Sin datos todavía (esperando la primera consulta).")
	} else {
		presenceType := state.PresenceType
		lines = append(lines, format.PresenceLabel(state))

		if presenceType == 2 && state.Game != nil && state.Game.URL != "" {
			lines = append(lines, fmt.Sprintf("🔗 [Abrir juego](%s)", state.Game.URL))
		}

		if presenceType != 0 {
			onlineSince := state.Since
			if state.SessionStart != nil && *state.SessionStart != "" {
				onlineSince = *state.SessionStart
			}
			if onlineSince != "" {
				lines = append(lines, statusTimeLine("🕒 En línea desde", onlineSince))
			}
			if presenceType == 2 && state.Since != "" {
				lines = append(lines, statusTimeLine("🎮 Jugando desde", state.Since))
			}
			if presenceType == 3 && state.Since != "" {
				lines = append(lines, statusTimeLine("🛠️ Studio desde", state.Since))
			}
		} else {
			var lastConnection time.Time
			hasLastConnection := false
			for _, raw := range []*string{state.LastOnline, state.LastSeenAt} {
				parsed, ok := parsePtrTime(raw)
				if !ok {
					continue
				}
				if !hasLastConnection || parsed.After(lastConnection) {
					lastConnection = parsed
					hasLastConnection = true
				}
			}
			if hasLastConnection {
				lines = append(lines, statusTimeLine("🕒 Última conexión", format.ISOMillis(lastConnection)))
			}
			if state.LastSessionMs != nil && *state.LastSessionMs > 0 {
				lines = append(lines, "⏳ Última sesión: "+format.FormatDuration(state.LastSessionMs))
			}
		}
	}

	return field(
		format.Truncate(fmt.Sprintf("%s @%s", statusEmoji(state), account.Username), 256),
		format.Truncate(strings.Join(lines, "\n"), 1024),
		false,
	)
}

// BuildStatusPayload construye el panel persistente. Discord admite 25 campos
// por embed, así que se reparten en varios embeds.
func BuildStatusPayload(entries []Entry, cfg *config.Config, now time.Time) map[string]any {
	fields := make([]any, 0, len(entries))
	for _, entry := range entries {
		fields = append(fields, statusAccountField(entry.Account, entry.State))
	}

	chunks := make([][]any, 0)
	for start := 0; start < len(fields); start += 25 {
		end := start + 25
		if end > len(fields) {
			end = len(fields)
		}
		chunks = append(chunks, fields[start:end])
	}
	if len(chunks) == 0 {
		chunks = append(chunks, []any{})
	}

	interval := 30
	if cfg != nil {
		interval = cfg.PollInterval()
	}
	updatedLabel := format.UTCString(now)
	embeds := make([]any, 0, len(chunks))

	for index, chunk := range chunks {
		embed := map[string]any{
			"color":  colorBlurple,
			"fields": chunk,
		}
		if index == 0 {
			embed["title"] = "📡 Estado de presencia en Roblox"
			if len(entries) == 0 {
				embed["description"] = "No hay cuentas configuradas. Añádelas con `presence-bot add <usuario>`."
			} else {
				embed["description"] = fmt.Sprintf(
					"Estado actual de las cuentas vigiladas. Este mensaje se actualiza automáticamente cada **%d s**. Cada persona ve las horas en su propia zona horaria.",
					interval,
				)
			}
		}
		if index == len(chunks)-1 {
			embed["footer"] = map[string]any{
				"text": format.Truncate("Roblox Presence Monitor · Actualizado: "+updatedLabel, 2048),
			}
			embed["timestamp"] = format.ISOMillis(now)
		}
		embeds = append(embeds, embed)
	}

	return map[string]any{"embeds": embeds}
}

func BuildStartupPayload(entries []Entry, cfg *config.Config, now time.Time) map[string]any {
	total := len(entries)
	fields := make([]any, 0, 25)
	limit := total
	if limit > 24 {
		limit = 24
	}
	for _, entry := range entries[:limit] {
		value := "Sin datos todavía"
		if entry.State != nil {
			value = format.PresenceLabel(entry.State)
		}
		fields = append(fields, field(format.Truncate("@"+entry.Account.Username, 256), value, true))
	}
	if total > 24 {
		fields = append(fields, field("…", fmt.Sprintf("y %d cuenta(s) más", total-24), false))
	}

	interval := 30
	if cfg != nil {
		interval = cfg.PollInterval()
	}
	description := fmt.Sprintf(
		"Vigilando **%d** cuenta(s) cada **%d s**. Los cambios se notificarán en este canal.",
		total,
		interval,
	)
	if total == 0 {
		description = "No hay cuentas configuradas todavía. Añade cuentas con `presence-bot add <usuario>`."
	}

	return map[string]any{
		"embeds": []any{
			map[string]any{
				"title":       "📡 Monitor de presencia iniciado",
				"description": description,
				"color":       colorBlurple,
				"fields":      fields,
				"footer":      map[string]any{"text": "Roblox Presence Monitor · " + format.UTCString(now)},
				"timestamp":   format.ISOMillis(now),
			},
		},
	}
}

func BuildTestPayload(cfg *config.Config, channelID string, accountCount int, cookieUserName string) map[string]any {
	interval := 30
	if cfg != nil {
		interval = cfg.PollInterval()
	}
	if channelID == "" {
		channelID = "desconocido"
	}
	cookieValue := "No configurada\nSolo se verá conectado/desconectado/en juego."
	if cookieUserName != "" {
		cookieValue = fmt.Sprintf("Válida: `@%s`\nSe verán nombres de juegos y servidores.", cookieUserName)
	}

	return map[string]any{
		"embeds": []any{
			map[string]any{
				"title":       "✅ Webhook conectado",
				"description": "El monitor puede enviar mensajes a este canal. Si ves este mensaje, la configuración del webhook es correcta.",
				"color":       colorGreen,
				"fields": []any{
					field("📡 Canal", fmt.Sprintf("`%s`", channelID), true),
					field("👥 Cuentas vigiladas", fmt.Sprintf("%d", accountCount), true),
					field("⏱️ Intervalo", fmt.Sprintf("%d s", interval), true),
					field("🍪 Cookie de Roblox", cookieValue, false),
				},
				"footer":    map[string]any{"text": "Roblox Presence Monitor · " + format.UTCString(time.Now())},
				"timestamp": format.ISOMillis(time.Now()),
			},
		},
	}
}
