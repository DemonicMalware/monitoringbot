package format

import (
	"fmt"
	"math"
	"strings"
	"time"

	"roblox-presence-bot/internal/model"
)

func UnixSeconds(value time.Time) int64 {
	return value.Unix()
}

// DiscordTime genera <t:...:F> o <t:...:R>.
func DiscordTime(value time.Time, style string) string {
	if style == "" {
		style = "F"
	}
	return fmt.Sprintf("<t:%d:%s>", UnixSeconds(value), style)
}

func DiscordTimeString(value string, style string) string {
	parsed, ok := ParseTime(value)
	if !ok {
		return ""
	}
	return DiscordTime(parsed, style)
}

func UTCString(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}

func UTCStringString(value string) string {
	parsed, ok := ParseTime(value)
	if !ok {
		return "fecha desconocida"
	}
	return UTCString(parsed)
}

func ISOMillis(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func LocalString(value string, timeZone string) string {
	parsed, ok := ParseTime(value)
	if !ok {
		return "fecha desconocida"
	}
	loc := time.Local
	if strings.TrimSpace(timeZone) != "" {
		if loaded, err := time.LoadLocation(strings.TrimSpace(timeZone)); err == nil {
			loc = loaded
		}
	}
	return parsed.In(loc).Format("02/01/2006, 15:04:05")
}

func ParseTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func FormatDuration(milliseconds *int64) string {
	if milliseconds == nil || *milliseconds < 0 {
		return "desconocido"
	}
	totalSeconds := *milliseconds / 1000
	if totalSeconds < 1 {
		return "menos de 1 s"
	}

	days := totalSeconds / 86400
	hours := (totalSeconds % 86400) / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60

	parts := make([]string, 0, 4)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d d", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d h", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%d min", minutes))
	}
	if days == 0 && hours == 0 && seconds > 0 {
		parts = append(parts, fmt.Sprintf("%d s", seconds))
	}
	if len(parts) == 0 {
		return "menos de 1 s"
	}
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return strings.Join(parts, " ")
}

var markdownReplacer = strings.NewReplacer(
	`\`, `\\`,
	"`", "\\`",
	"*", "\\*",
	"_", "\\_",
	"~", "\\~",
	"|", "\\|",
	">", "\\>",
	"[", "\\[",
	"]", "\\]",
)

func EscapeMarkdown(text string) string {
	return markdownReplacer.Replace(text)
}

func Truncate(text string, maxLength int) string {
	runes := []rune(text)
	if len(runes) <= maxLength {
		return text
	}
	if maxLength <= 0 {
		return ""
	}
	return string(runes[:maxLength-1]) + "…"
}

func GameLabel(game *model.Game) string {
	if game == nil || game.Name == "" {
		return ""
	}
	return EscapeMarkdown(game.Name)
}

func presenceLabelParts(presenceType int, game *model.Game) string {
	switch presenceType {
	case 0:
		return "Desconectado"
	case 1:
		return "En línea (sitio web)"
	case 2:
		if name := GameLabel(game); name != "" {
			return "Jugando **" + name + "**"
		}
		return "Jugando (detalles no disponibles)"
	case 3:
		return "En Roblox Studio"
	default:
		return "Estado desconocido"
	}
}

// PresenceLabel acepta *model.UserState o *model.Presence.
func PresenceLabel(value any) string {
	switch state := value.(type) {
	case *model.UserState:
		if state == nil {
			return "Sin datos"
		}
		return presenceLabelParts(state.PresenceType, state.Game)
	case *model.Presence:
		if state == nil {
			return "Sin datos"
		}
		return presenceLabelParts(state.PresenceType, state.Game)
	default:
		return "Sin datos"
	}
}

func PresenceTypeLabel(presenceType int) string {
	return presenceLabelParts(presenceType, nil)
}

func MaxInt64(values ...int64) (int64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	max := values[0]
	for _, value := range values[1:] {
		if value > max {
			max = value
		}
	}
	return max, true
}

func FloatToInt64(value float64) (int64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return int64(value), true
}
