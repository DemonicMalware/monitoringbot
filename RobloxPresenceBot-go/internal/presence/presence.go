package presence

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"roblox-presence-bot/internal/format"
	"roblox-presence-bot/internal/logger"
	"roblox-presence-bot/internal/model"
)

// RawPresence es la respuesta cruda de presence.roblox.com.
type RawPresence struct {
	UserID           int64   `json:"userId"`
	UserPresenceType int     `json:"userPresenceType"`
	LastLocation     string  `json:"lastLocation"`
	PlaceID          *int64  `json:"placeId"`
	RootPlaceID      *int64  `json:"rootPlaceId"`
	UniverseID       *int64  `json:"universeId"`
	GameID           *string `json:"gameId"`
	LastOnline       *string `json:"lastOnline"`
	InStudio         bool    `json:"inStudio"`
	IsOnline         *bool   `json:"isOnline"`
}

func NormalizePresence(raw RawPresence) model.Presence {
	presenceType := raw.UserPresenceType
	if presenceType < 0 || presenceType > 3 {
		presenceType = 0
	}
	isOnline := presenceType != 0
	if raw.IsOnline != nil {
		isOnline = *raw.IsOnline
	}
	lastOnline := raw.LastOnline
	if lastOnline != nil {
		if _, ok := format.ParseTime(*lastOnline); !ok {
			lastOnline = nil
		}
	}
	gameID := raw.GameID
	if gameID != nil && strings.TrimSpace(*gameID) == "" {
		gameID = nil
	}
	return model.Presence{
		PresenceType: presenceType,
		LastLocation: strings.TrimSpace(raw.LastLocation),
		PlaceID:      positiveInt64(raw.PlaceID),
		RootPlaceID:  positiveInt64(raw.RootPlaceID),
		UniverseID:   positiveInt64(raw.UniverseID),
		GameID:       gameID,
		LastOnline:   lastOnline,
		InStudio:     raw.InStudio || presenceType == 3,
		IsOnline:     isOnline,
		Game:         nil,
	}
}

func positiveInt64(value *int64) *int64 {
	if value == nil || *value <= 0 {
		return nil
	}
	copy := *value
	return &copy
}

func gameKeyFromParts(rootPlaceID, placeID, universeID *int64, game *model.Game, lastLocation string) *string {
	if rootPlaceID == nil && game != nil {
		rootPlaceID = game.RootPlaceID
	}
	if rootPlaceID != nil {
		key := fmt.Sprintf("place:%d", *rootPlaceID)
		return &key
	}
	if placeID != nil {
		key := fmt.Sprintf("place:%d", *placeID)
		return &key
	}
	if universeID == nil && game != nil {
		universeID = game.UniverseID
	}
	if universeID != nil {
		key := fmt.Sprintf("universe:%d", *universeID)
		return &key
	}
	name := ""
	if game != nil {
		name = game.Name
	}
	if name == "" {
		name = lastLocation
	}
	if name == "" {
		return nil
	}
	key := "name:" + strings.ToLower(name)
	return &key
}

func GameKeyPresence(value *model.Presence) *string {
	if value == nil {
		return nil
	}
	return gameKeyFromParts(value.RootPlaceID, value.PlaceID, value.UniverseID, value.Game, value.LastLocation)
}

func GameKeyState(value *model.UserState) *string {
	if value == nil {
		return nil
	}
	return gameKeyFromParts(value.RootPlaceID, value.PlaceID, value.UniverseID, value.Game, value.LastLocation)
}

func timestampMs(value *string) *int64 {
	if value == nil || *value == "" {
		return nil
	}
	parsed, ok := format.ParseTime(*value)
	if !ok {
		return nil
	}
	ms := parsed.UnixMilli()
	return &ms
}

// DetectEvent decide qué evento ocurrió entre dos estados.
func DetectEvent(previous *model.UserState, next model.Presence, now time.Time) *model.Event {
	if previous == nil {
		return nil
	}

	previousType := previous.PresenceType
	nextType := next.PresenceType
	previousGameKey := GameKeyState(previous)
	nextGameKey := GameKeyPresence(&next)

	sessionStart := timestampMs(previous.SessionStart)
	if sessionStart == nil {
		sessionStart = timestampMs(&previous.Since)
	}
	activityStart := timestampMs(&previous.Since)

	event := &model.Event{
		At:         now,
		Prev:       previous,
		Next:       &next,
		SessionMs:  nil,
		ActivityMs: nil,
		LastOnline: next.LastOnline,
	}
	if event.LastOnline == nil {
		event.LastOnline = previous.LastOnline
	}
	if sessionStart != nil {
		ms := now.UnixMilli() - *sessionStart
		if ms < 0 {
			ms = 0
		}
		event.SessionMs = &ms
	}
	if activityStart != nil {
		ms := now.UnixMilli() - *activityStart
		if ms < 0 {
			ms = 0
		}
		event.ActivityMs = &ms
	}

	if previousType == 0 && nextType != 0 {
		event.Type = "connected"
		return event
	}
	if previousType != 0 && nextType == 0 {
		event.Type = "disconnected"
		return event
	}
	if previousType == nextType {
		if nextType == 2 && previousGameKey != nil && nextGameKey != nil && *previousGameKey != *nextGameKey {
			event.Type = "game_changed"
			return event
		}
		return nil
	}

	switch {
	case nextType == 2:
		event.Type = "game_started"
	case nextType == 3:
		event.Type = "studio_opened"
	case previousType == 2:
		event.Type = "left_game"
	case previousType == 3:
		event.Type = "studio_closed"
	default:
		event.Type = "status_changed"
	}
	return event
}

func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func firstInt64(primary, fallback *int64) *int64 {
	if primary != nil {
		return copyInt64(primary)
	}
	return copyInt64(fallback)
}

// UpdateState guarda la línea de tiempo necesaria para duraciones y cambios.
func UpdateState(account model.Account, previous *model.UserState, next model.Presence, now time.Time) *model.UserState {
	nowISO := format.ISOMillis(now)
	nextOnline := next.PresenceType != 0
	previousOnline := previous != nil && previous.PresenceType != 0

	previousGameKey := GameKeyState(previous)
	nextGameKey := GameKeyPresence(&next)
	previousType := 0
	if previous != nil {
		previousType = previous.PresenceType
	}
	nextType := next.PresenceType

	gameChanged := previous != nil &&
		previousType == 2 && nextType == 2 &&
		previousGameKey != nil && nextGameKey != nil &&
		*previousGameKey != *nextGameKey
	activityChanged := previous == nil || previousType != nextType || gameChanged

	var sessionStart *string
	if previous != nil {
		sessionStart = copyString(previous.SessionStart)
	}
	if nextOnline && (!previousOnline || sessionStart == nil) {
		sessionStart = &nowISO
	}

	isGame := next.PresenceType == 2
	state := &model.UserState{
		UserID:        account.ID(),
		Username:      account.Username,
		DisplayName:   account.DisplayName,
		PresenceType:  next.PresenceType,
		IsOnline:      nextOnline,
		GameID:        nil,
		Since:         nowISO,
		SessionStart:  sessionStart,
		LastSeenAt:    nil,
		LastOnline:    nil,
		LastSessionMs: nil,
		LastPollAt:    nowISO,
	}

	if state.DisplayName == nil && previous != nil {
		state.DisplayName = copyString(previous.DisplayName)
	}

	if isGame {
		state.LastLocation = next.LastLocation
		if state.LastLocation == "" && previous != nil {
			state.LastLocation = previous.LastLocation
		}
		var previousPlace, previousRoot, previousUniverse *int64
		var previousGameID *string
		var previousGame *model.Game
		if previous != nil {
			previousPlace = previous.PlaceID
			previousRoot = previous.RootPlaceID
			previousUniverse = previous.UniverseID
			previousGameID = previous.GameID
			previousGame = previous.Game
		}
		state.PlaceID = firstInt64(next.PlaceID, previousPlace)
		state.RootPlaceID = firstInt64(next.RootPlaceID, previousRoot)
		state.UniverseID = firstInt64(next.UniverseID, previousUniverse)
		state.GameID = firstString(next.GameID, previousGameID)
		if next.Game != nil {
			state.Game = next.Game
		} else {
			state.Game = previousGame
		}
	} else {
		state.LastLocation = ""
	}

	if activityChanged {
		state.Since = nowISO
	} else if previous != nil && previous.Since != "" {
		state.Since = previous.Since
	}

	if nextOnline {
		state.LastSeenAt = &nowISO
	} else if previous != nil {
		state.LastSeenAt = copyString(previous.LastSeenAt)
	}

	if next.LastOnline != nil {
		state.LastOnline = copyString(next.LastOnline)
	} else if previous != nil {
		state.LastOnline = copyString(previous.LastOnline)
	}

	if previous != nil {
		state.LastSessionMs = copyInt64(previous.LastSessionMs)
	}

	if !nextOnline && previousOnline {
		startMs := timestampMs(sessionStart)
		if startMs == nil && previous != nil {
			startMs = timestampMs(&previous.Since)
		}
		if startMs != nil {
			ms := now.UnixMilli() - *startMs
			if ms < 0 {
				ms = 0
			}
			state.LastSessionMs = &ms
		}
		if previous != nil {
			if previous.LastSeenAt != nil {
				state.LastSeenAt = copyString(previous.LastSeenAt)
			} else {
				state.LastSeenAt = copyString(&previous.LastPollAt)
			}
		}
	}

	return state
}

func firstString(primary, fallback *string) *string {
	if primary != nil {
		return copyString(primary)
	}
	return copyString(fallback)
}

type GameResolver interface {
	GetGameByUniverse(universeID int64, fallbackPlaceID *int64) (*model.Game, error)
	GetPlaceDetails(placeID int64) (*model.Game, error)
}

// ResolveGameDetails completa la presencia con datos del juego.
func ResolveGameDetails(roblox GameResolver, next model.Presence, log *logger.Logger) *model.Game {
	if next.PresenceType != 2 {
		return nil
	}
	placeID := next.PlaceID
	if placeID == nil {
		placeID = next.RootPlaceID
	}
	if placeID == nil && next.LastLocation == "" {
		return nil
	}

	var details *model.Game
	var err error
	if next.UniverseID != nil {
		details, err = roblox.GetGameByUniverse(*next.UniverseID, placeID)
	} else if placeID != nil {
		details, err = roblox.GetPlaceDetails(*placeID)
	}
	if err != nil {
		log.Warnf("No se pudo obtener la información del juego: %v", err)
	}

	rootPlaceID := next.RootPlaceID
	if rootPlaceID == nil && details != nil {
		rootPlaceID = details.RootPlaceID
	}
	if rootPlaceID == nil {
		rootPlaceID = placeID
	}

	name := ""
	if details != nil {
		name = details.Name
	}
	if name == "" {
		name = next.LastLocation
	}
	if name == "" && placeID == nil {
		return nil
	}

	game := &model.Game{
		Name:        name,
		PlaceID:     copyInt64(placeID),
		RootPlaceID: copyInt64(rootPlaceID),
		UniverseID:  copyInt64(next.UniverseID),
		GameID:      "",
		IconURL:     "",
	}
	if game.UniverseID == nil && details != nil {
		game.UniverseID = copyInt64(details.UniverseID)
	}
	if next.GameID != nil {
		game.GameID = *next.GameID
	}
	if details != nil {
		game.IconURL = details.IconURL
	}
	if rootPlaceID != nil {
		game.URL = fmt.Sprintf("https://www.roblox.com/games/%d", *rootPlaceID)
	} else if details != nil {
		game.URL = details.URL
	}
	if placeID != nil {
		game.JoinURL = fmt.Sprintf("https://www.roblox.com/games/start?placeId=%d", *placeID)
		if game.GameID != "" {
			game.JoinURL += "&gameInstanceId=" + url.QueryEscape(game.GameID)
		}
	}
	return game
}

func Online(value *model.Presence) bool {
	return value != nil && value.PresenceType != 0
}
