package model

import "time"

// Account es una cuenta vigilada, tal como se guarda en accounts.json.
type Account struct {
	Username    string  `json:"username"`
	DisplayName *string `json:"displayName"`
	UserID      *int64  `json:"userId"`
	AddedAt     *string `json:"addedAt"`
}

func (a Account) ID() int64 {
	if a.UserID == nil {
		return 0
	}
	return *a.UserID
}

func (a Account) Display() string {
	if a.DisplayName != nil && *a.DisplayName != "" && *a.DisplayName != a.Username {
		return *a.DisplayName + " (@" + a.Username + ")"
	}
	return a.Username
}

// Game son los detalles de un juego o lugar de Roblox.
type Game struct {
	Name        string `json:"name,omitempty"`
	PlaceID     *int64 `json:"placeId,omitempty"`
	RootPlaceID *int64 `json:"rootPlaceId,omitempty"`
	UniverseID  *int64 `json:"universeId,omitempty"`
	GameID      string `json:"gameId,omitempty"`
	IconURL     string `json:"iconUrl,omitempty"`
	URL         string `json:"url,omitempty"`
	JoinURL     string `json:"joinUrl,omitempty"`
}

// Presence es la presencia normalizada de una cuenta.
type Presence struct {
	PresenceType int     `json:"presenceType"`
	LastLocation string  `json:"lastLocation"`
	PlaceID      *int64  `json:"placeId"`
	RootPlaceID  *int64  `json:"rootPlaceId"`
	UniverseID   *int64  `json:"universeId"`
	GameID       *string `json:"gameId"`
	LastOnline   *string `json:"lastOnline"`
	InStudio     bool    `json:"inStudio"`
	IsOnline     bool    `json:"isOnline"`
	Game         *Game   `json:"game"`
}

// UserState es el estado persistido de una cuenta en data/state.json.
type UserState struct {
	UserID      int64   `json:"userId"`
	Username    string  `json:"username"`
	DisplayName *string `json:"displayName"`

	PresenceType int    `json:"presenceType"`
	IsOnline     bool   `json:"isOnline"`
	LastLocation string `json:"lastLocation"`

	PlaceID     *int64  `json:"placeId"`
	RootPlaceID *int64  `json:"rootPlaceId"`
	UniverseID  *int64  `json:"universeId"`
	GameID      *string `json:"gameId"`
	Game        *Game   `json:"game"`

	Since         string  `json:"since"`
	SessionStart  *string `json:"sessionStart"`
	LastSeenAt    *string `json:"lastSeenAt"`
	LastOnline    *string `json:"lastOnline"`
	LastSessionMs *int64  `json:"lastSessionMs"`
	LastPollAt    string  `json:"lastPollAt"`
}

// Panel recuerda el mensaje fijo de Discord que se edita en cada ciclo.
type Panel struct {
	MessageID string `json:"messageId,omitempty"`
	ChannelID string `json:"channelId,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// State es el contenido completo de data/state.json.
type State struct {
	Version   int                   `json:"version"`
	Users     map[string]*UserState `json:"users"`
	UpdatedAt *string               `json:"updatedAt"`
	Panel     *Panel                `json:"panel,omitempty"`
}

// ResolvedUser es el resultado de resolver un nombre de usuario en Roblox.
type ResolvedUser struct {
	ID               int64
	Name             string
	DisplayName      string
	HasVerifiedBadge bool
}

// Ptr devuelve un puntero al valor recibido.
func Ptr[T any](value T) *T {
	return &value
}

// Event representa un cambio detectado entre dos presencias.
type Event struct {
	Type       string
	At         time.Time
	Account    Account
	Prev       *UserState
	Next       *Presence
	SessionMs  *int64
	ActivityMs *int64
	LastOnline *string
}
