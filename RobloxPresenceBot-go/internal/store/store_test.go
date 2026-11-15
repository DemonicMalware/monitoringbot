package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"roblox-presence-bot/internal/model"
)

func TestLoadAccountsFormats(t *testing.T) {
	file := filepath.Join(t.TempDir(), "accounts.json")
	content := `{"accounts":["@Cuenta Uno",{"name":"Otra","userId":"123","displayName":"Otra Display"},"cuenta uno"]}`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	accounts, err := LoadAccounts(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("se esperaban 2 cuentas, hay %d: %+v", len(accounts), accounts)
	}
	if accounts[0].Username != "CuentaUno" {
		t.Fatalf("nombre inesperado: %q", accounts[0].Username)
	}
	if accounts[1].ID() != 123 {
		t.Fatalf("el ID en texto debe convertirse: %d", accounts[1].ID())
	}
	if accounts[1].DisplayName == nil || *accounts[1].DisplayName != "Otra Display" {
		t.Fatalf("displayName inesperado: %+v", accounts[1].DisplayName)
	}
}

func TestLoadAccountsMissingFile(t *testing.T) {
	accounts, err := LoadAccounts(filepath.Join(t.TempDir(), "no-existe.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 {
		t.Fatalf("se esperaba una lista vacía, hay %d", len(accounts))
	}
}

func TestSaveAndLoadState(t *testing.T) {
	file := filepath.Join(t.TempDir(), "data", "state.json")
	state := &model.State{
		Version: 1,
		Users: map[string]*model.UserState{
			"12345": {
				UserID:       12345,
				Username:     "Cuenta_Prueba",
				PresenceType: 2,
			},
		},
		Panel: &model.Panel{MessageID: "999", ChannelID: "1"},
	}
	if err := WriteJSON(file, state); err != nil {
		t.Fatal(err)
	}

	loaded := LoadState(file, nil)
	if loaded.Users["12345"] == nil || loaded.Users["12345"].PresenceType != 2 {
		t.Fatalf("estado cargado inesperado: %+v", loaded.Users)
	}
	if loaded.Panel == nil || loaded.Panel.MessageID != "999" {
		t.Fatalf("panel cargado inesperado: %+v", loaded.Panel)
	}
}

func TestLoadStateCorrupt(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "state.json")
	if err := os.WriteFile(file, []byte("no es json"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := LoadState(file, nil)
	if len(state.Users) != 0 || state.Version != 1 {
		t.Fatalf("se esperaba un estado vacío: %+v", state)
	}
	matches, err := filepath.Glob(file + ".corrupt-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("se esperaba una copia corrupta, hay %d", len(matches))
	}
}

func TestSaveAccountsWritesNulls(t *testing.T) {
	file := filepath.Join(t.TempDir(), "accounts.json")
	accounts := []model.Account{{Username: "Sin_ID"}}
	if err := SaveAccounts(file, accounts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var saved []map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 {
		t.Fatalf("se esperaba 1 cuenta, hay %d", len(saved))
	}
	if saved[0]["userId"] != nil || saved[0]["displayName"] != nil {
		t.Fatalf("los campos vacíos deben guardarse como null: %+v", saved[0])
	}
}
