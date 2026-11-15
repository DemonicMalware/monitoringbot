package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

const stateVersion = 1

// AccountState guarda lo último que se sabe de una cuenta.
type AccountState struct {
	UserID        int64      `json:"userId"`
	Username      string     `json:"username"`
	DisplayName   string     `json:"displayName,omitempty"`
	Online        bool       `json:"online"`
	PresenceType  int        `json:"presenceType"`
	LastLocation  string     `json:"lastLocation,omitempty"`
	PlaceID       int64      `json:"placeId,omitempty"`
	RootPlaceID   int64      `json:"rootPlaceId,omitempty"`
	UniverseID    int64      `json:"universeId,omitempty"`
	GameID        string     `json:"gameId,omitempty"`
	SessionStart  *time.Time `json:"sessionStart,omitempty"`
	PlayingSince  *time.Time `json:"playingSince,omitempty"`
	LastSeenAt    *time.Time `json:"lastSeenAt,omitempty"`
	LastChangedAt *time.Time `json:"lastChangedAt,omitempty"`

	// StatusMessageID permite editar el mismo mensaje de estado en Discord.
	StatusMessageID string `json:"statusMessageId,omitempty"`
}

// State es el archivo que conserva las sesiones entre reinicios.
type State struct {
	Version   int                      `json:"version"`
	UpdatedAt time.Time                `json:"updatedAt"`
	Accounts  map[string]*AccountState `json:"accounts"`
}

func NewState() *State {
	return &State{
		Version:  stateVersion,
		Accounts: make(map[string]*AccountState),
	}
}

// LoadState carga el estado. Si está dañado, guarda una copia y empieza de cero.
func LoadState(path string, logger *log.Logger) *State {
	state := NewState()
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.Printf("Aviso: no se pudo leer el estado en %s: %v. Se empieza de cero.", path, err)
		}
		return state
	}
	if err := json.Unmarshal(data, state); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
		if renameErr := os.Rename(path, backup); renameErr == nil {
			logger.Printf("Aviso: el estado estaba dañado. Se guardó una copia en %s y se empieza de cero.", backup)
		} else {
			logger.Printf("Aviso: el estado estaba dañado y no se pudo respaldar: %v. Se empieza de cero.", err)
		}
		return NewState()
	}
	state.Version = stateVersion
	if state.Accounts == nil {
		state.Accounts = make(map[string]*AccountState)
	}
	for key, account := range state.Accounts {
		if account == nil {
			state.Accounts[key] = &AccountState{}
		}
	}
	return state
}

// SaveState escribe el estado de forma atómica.
func SaveState(path string, state *State) error {
	if state == nil {
		return fmt.Errorf("estado vacío")
	}
	state.Version = stateVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("no se pudo preparar el estado: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("no se pudo crear la carpeta %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".simple-state-*.tmp")
	if err != nil {
		return fmt.Errorf("no se pudo crear el archivo temporal del estado: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("no se pudo ajustar el estado: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("no se pudo escribir el estado: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("no se pudo sincronizar el estado: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("no se pudo cerrar el estado: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		// En algunos sistemas Windows no se puede reemplazar directamente.
		if removeErr := os.Remove(path); removeErr == nil {
			err = os.Rename(tmpName, path)
		}
		if err != nil {
			return fmt.Errorf("no se pudo guardar el estado en %s: %w", path, err)
		}
	}
	return nil
}
