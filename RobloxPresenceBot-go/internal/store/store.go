package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"roblox-presence-bot/internal/logger"
	"roblox-presence-bot/internal/model"
)

var ErrNotExist = errors.New("archivo inexistente")

// ReadJSON lee un JSON. Devuelve ErrNotExist si el archivo no existe.
func ReadJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("no se pudo leer %s: %w", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("no se pudo interpretar %s: %w", path, err)
	}
	return nil
}

// WriteJSON escribe de forma atómica para no dejar archivos a medias.
func WriteJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		// En Windows el rename sobre un archivo existente puede fallar.
		_ = os.Remove(path)
		if err := os.Rename(tmp, path); err != nil {
			if copyErr := os.WriteFile(path, data, 0o644); copyErr != nil {
				_ = os.Remove(tmp)
				return copyErr
			}
		}
	}
	_ = os.Remove(tmp)
	return nil
}

func FileMtime(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().UnixNano()
}

// flexID acepta 123, "123" o valores inválidos (que se tratan como nulos).
type flexID int64

func (f *flexID) UnmarshalJSON(data []byte) error {
	raw := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if raw == "" || raw == "null" {
		*f = 0
		return nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexID(value)
	return nil
}

type rawAccount struct {
	Username    string  `json:"username"`
	Name        string  `json:"name"`
	DisplayName *string `json:"displayName"`
	UserID      *flexID `json:"userId"`
	ID          *flexID `json:"id"`
	AddedAt     *string `json:"addedAt"`
}

// LoadAccounts acepta ["usuario"], [{ "username": "...", "userId": 123 }]
// o { "accounts": [...] }.
func LoadAccounts(path string) ([]model.Account, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []model.Account{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer %s: %w", path, err)
	}

	var raw []json.RawMessage
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		var wrapper struct {
			Accounts []json.RawMessage `json:"accounts"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return nil, fmt.Errorf("no se pudo interpretar %s: %w", path, err)
		}
		raw = wrapper.Accounts
	} else if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("no se pudo interpretar %s: %w", path, err)
	}

	accounts := make([]model.Account, 0, len(raw))
	seen := make(map[string]struct{})
	for _, item := range raw {
		text := strings.TrimSpace(string(item))
		if text == "" {
			continue
		}

		var username string
		var displayName *string
		var userID *int64
		var addedAt *string

		if strings.HasPrefix(text, `"`) {
			if err := json.Unmarshal(item, &username); err != nil {
				continue
			}
		} else {
			var entry rawAccount
			if err := json.Unmarshal(item, &entry); err != nil {
				continue
			}
			username = entry.Username
			if username == "" {
				username = entry.Name
			}
			displayName = entry.DisplayName
			addedAt = entry.AddedAt
			rawID := entry.UserID
			if rawID == nil {
				rawID = entry.ID
			}
			if rawID != nil && int64(*rawID) > 0 {
				value := int64(*rawID)
				userID = &value
			}
		}

		username = CleanUsername(username)
		if username == "" {
			continue
		}
		key := strings.ToLower(username)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		if userID != nil && *userID <= 0 {
			userID = nil
		}
		accounts = append(accounts, model.Account{
			Username:    username,
			DisplayName: displayName,
			UserID:      userID,
			AddedAt:     addedAt,
		})
	}
	return accounts, nil
}

func SaveAccounts(path string, accounts []model.Account) error {
	type saved struct {
		Username    string  `json:"username"`
		DisplayName *string `json:"displayName"`
		UserID      *int64  `json:"userId"`
		AddedAt     *string `json:"addedAt"`
	}
	list := make([]saved, 0, len(accounts))
	for _, account := range accounts {
		list = append(list, saved{
			Username:    account.Username,
			DisplayName: account.DisplayName,
			UserID:      account.UserID,
			AddedAt:     account.AddedAt,
		})
	}
	return WriteJSON(path, list)
}

func CleanUsername(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimLeft(value, "@")
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
}

// LoadState devuelve un estado vacío si el archivo no existe o está corrupto.
func LoadState(path string, log *logger.Logger) *model.State {
	var state model.State
	if err := ReadJSON(path, &state); err != nil {
		if !errors.Is(err, ErrNotExist) {
			backup := fmt.Sprintf("%s.corrupt-%d", path, os.Getpid())
			if renameErr := os.Rename(path, backup); renameErr == nil {
				log.Warnf("Estado corrupto en %s. Se guardó una copia en %s y se empieza de cero.", path, backup)
			} else {
				log.Warnf("Estado corrupto en %s. Se empieza de cero.", path)
			}
		}
		return &model.State{Version: 1, Users: map[string]*model.UserState{}}
	}
	if state.Users == nil {
		state.Users = map[string]*model.UserState{}
	}
	state.Version = 1
	if state.Panel == nil {
		state.Panel = &model.Panel{}
	}
	return &state
}
