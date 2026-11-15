package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"roblox-presence-bot/internal/config"
	"roblox-presence-bot/internal/discord"
	"roblox-presence-bot/internal/events"
	"roblox-presence-bot/internal/format"
	"roblox-presence-bot/internal/logger"
	"roblox-presence-bot/internal/model"
	"roblox-presence-bot/internal/monitor"
	"roblox-presence-bot/internal/presence"
	"roblox-presence-bot/internal/roblox"
	"roblox-presence-bot/internal/store"
)

type appContext struct {
	root    string
	config  *config.Config
	logger  *logger.Logger
	roblox  *roblox.Client
	discord *discord.Webhook
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	command := "start"
	if len(args) > 0 {
		command = strings.ToLower(args[0])
		args = args[1:]
	}

	switch command {
	case "start":
		return commandStart(args)
	case "add":
		return commandAdd(args)
	case "remove":
		return commandRemove(args)
	case "list":
		return commandList()
	case "check":
		return commandCheck(args)
	case "status":
		return commandStatus(args)
	case "test":
		return commandTest()
	case "help", "--help", "-h":
		printHelp()
		return nil
	default:
		fmt.Fprintf(os.Stderr, "Comando desconocido: %s\n", command)
		printHelp()
		return fmt.Errorf("comando desconocido: %s", command)
	}
}

func resolveRoot() string {
	if value := strings.TrimSpace(os.Getenv("BOT_ROOT")); value != "" {
		if absolute, err := filepath.Abs(value); err == nil {
			return absolute
		}
		return value
	}
	if executable, err := os.Executable(); err == nil {
		directory := filepath.Dir(executable)
		if _, err := os.Stat(filepath.Join(directory, "config.json")); err == nil {
			return directory
		}
	}
	if workingDirectory, err := os.Getwd(); err == nil {
		return workingDirectory
	}
	return "."
}

func buildContext(dryRun bool) (*appContext, error) {
	root := resolveRoot()
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	if dryRun {
		cfg.DryRun = config.FlexBool(true)
	}

	log := logger.New(cfg.LogLevel, cfg.TimeZone, filepath.Join(root, "data", "monitor.log"))
	timeout := time.Duration(cfg.RequestTimeout()) * time.Millisecond
	robloxClient := roblox.New(cfg.RobloxCookie, timeout, log)
	discordClient := discord.New(
		cfg.WebhookURL,
		cfg.WebhookUsername,
		cfg.WebhookAvatarURL,
		time.Duration(cfg.WebhookMinInterval())*time.Millisecond,
		timeout,
		log,
		cfg.IsDryRun(),
	)

	return &appContext{
		root:    root,
		config:  cfg,
		logger:  log,
		roblox:  robloxClient,
		discord: discordClient,
	}, nil
}

func requireWebhook(cfg *config.Config) error {
	if cfg.WebhookURL == "" {
		return fmt.Errorf("Falta el webhook. Configura webhookUrl en config.json o DISCORD_WEBHOOK_URL en .env.")
	}
	return config.ValidateWebhookURL(cfg.WebhookURL)
}

func validateRobloxCookie(client *roblox.Client, log *logger.Logger) *model.ResolvedUser {
	if !client.HasCookie() {
		log.Warnf("Sin cookie de Roblox: se detectará conexión/desconexión y 'en juego', pero no el nombre del juego. Configura ROBLOSECURITY en .env.")
		return nil
	}

	user, err := client.ValidateCookie()
	if err != nil {
		log.Warnf("No se pudo validar la cookie de Roblox: %v", err)
		return nil
	}
	if user == nil {
		log.Warnf("La cookie de Roblox no es válida o caducó. Se continuará sin cookie (sin nombres de juegos).")
		client.ClearCookie()
		return nil
	}
	log.Infof("Cookie de Roblox válida: @%s (%d).", user.Name, user.ID)
	return user
}

func mapString(values map[string]any, key, fallback string) string {
	if values == nil {
		return fallback
	}
	switch value := values[key].(type) {
	case string:
		if value != "" {
			return value
		}
	case float64:
		return strconv.FormatInt(int64(value), 10)
	}
	return fallback
}

func commandStart(args []string) error {
	dryRun := contains(args, "--dry-run")
	app, err := buildContext(dryRun)
	if err != nil {
		return err
	}

	for index := 0; index < len(args); index++ {
		if args[index] == "--interval" && index+1 < len(args) {
			if value, err := strconv.Atoi(args[index+1]); err == nil {
				if value < 10 {
					value = 10
				}
				if value > 3600 {
					value = 3600
				}
				app.config.PollIntervalSeconds = config.FlexInt(value)
			}
		}
	}

	if err := requireWebhook(app.config); err != nil {
		return err
	}

	app.logger.Infof("==========================================")
	app.logger.Infof("  ROBLOX PRESENCE MONITOR")
	app.logger.Infof("==========================================")
	app.logger.Infof("Intervalo de consulta: %d s", app.config.PollInterval())
	if app.config.IsDryRun() {
		app.logger.Warnf("Modo DRY-RUN activo: los mensajes se muestran en consola, no se envían.")
	}

	webhookInfo, err := app.discord.Validate()
	if err != nil {
		return err
	}
	app.logger.Infof(
		"Webhook OK: %s (canal %s).",
		mapString(webhookInfo, "name", "sin nombre"),
		mapString(webhookInfo, "channel_id", "desconocido"),
	)

	validateRobloxCookie(app.roblox, app.logger)

	monitorClient := monitor.New(app.root, app.config, app.logger, app.roblox, app.discord)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app.logger.Infof("Monitor activo. Ctrl+C para detener.")
	monitorClient.Run(ctx)

	app.logger.Infof("Guardando estado y saliendo...")
	monitorClient.Stop()
	app.discord.Flush(15 * time.Second)
	app.logger.Close()
	return nil
}

func commandStatus(args []string) error {
	app, err := buildContext(contains(args, "--dry-run"))
	if err != nil {
		return err
	}
	if err := requireWebhook(app.config); err != nil {
		return err
	}

	if !app.config.StatusPanel.Enabled {
		fmt.Println(`El panel de estado está desactivado ("statusPanel.enabled": false en config.json).`)
		return nil
	}

	webhookInfo, err := app.discord.Validate()
	if err != nil {
		return err
	}
	app.logger.Infof(
		"Webhook OK: %s (canal %s).",
		mapString(webhookInfo, "name", "sin nombre"),
		mapString(webhookInfo, "channel_id", "desconocido"),
	)
	validateRobloxCookie(app.roblox, app.logger)

	monitorClient := monitor.New(app.root, app.config, app.logger, app.roblox, app.discord)
	_ = monitorClient.ReloadAccounts()

	if len(monitorClient.Accounts()) == 0 {
		fmt.Println("No hay cuentas configuradas. Usa: presence-bot add <usuario>")
		return nil
	}

	defer monitorClient.Stop()

	if err := monitorClient.Poll(true); err != nil {
		return err
	}
	panel := monitorClient.State().Panel
	if app.config.IsDryRun() {
		fmt.Println("Panel generado en modo dry-run (no enviado).")
	} else if panel != nil && panel.MessageID != "" {
		fmt.Printf("Panel de estado publicado o actualizado. Mensaje: %s\n", panel.MessageID)
	} else {
		fmt.Println("No se pudo publicar el panel. Revisa data/monitor.log.")
	}
	return nil
}

func commandAdd(args []string) error {
	names := cleanNames(args)
	if len(names) == 0 {
		return fmt.Errorf("Indica al menos un nombre de usuario. Ejemplo: presence-bot add Roblox builderman")
	}

	app, err := buildContext(false)
	if err != nil {
		return err
	}

	accounts, err := store.LoadAccounts(filepath.Join(app.root, "accounts.json"))
	if err != nil {
		return err
	}
	indexByName := make(map[string]int, len(accounts))
	for index, account := range accounts {
		indexByName[strings.ToLower(account.Username)] = index
	}

	toResolve := make([]string, 0)
	for _, name := range names {
		index, exists := indexByName[strings.ToLower(name)]
		if !exists || accounts[index].ID() == 0 {
			toResolve = append(toResolve, name)
		}
	}

	resolved := map[string]model.ResolvedUser{}
	if len(toResolve) > 0 {
		resolved, err = app.roblox.ResolveUsernames(toResolve)
		if err != nil {
			return err
		}
	}

	added := 0
	updated := 0
	for _, name := range names {
		key := strings.ToLower(name)
		index, exists := indexByName[key]
		found, foundOK := resolved[key]

		if exists && accounts[index].ID() != 0 && !foundOK {
			app.logger.Infof("@%s ya estaba en la lista (ID %d).", accounts[index].Username, accounts[index].ID())
			continue
		}
		if !foundOK {
			app.logger.Warnf("No se encontró la cuenta %q en Roblox. No se añadió.", name)
			continue
		}

		display := found.DisplayName
		if display == "" {
			display = found.Name
		}

		if exists {
			accounts[index].UserID = model.Ptr(found.ID)
			accounts[index].Username = found.Name
			accounts[index].DisplayName = model.Ptr(display)
			updated++
			app.logger.Infof("Actualizado: @%s (ID %d).", found.Name, found.ID)
			continue
		}

		accounts = append(accounts, model.Account{
			Username:    found.Name,
			DisplayName: model.Ptr(display),
			UserID:      model.Ptr(found.ID),
			AddedAt:     model.Ptr(format.ISOMillis(time.Now())),
		})
		indexByName[strings.ToLower(found.Name)] = len(accounts) - 1
		indexByName[key] = len(accounts) - 1
		added++
		app.logger.Infof("Añadido: @%s (ID %d).", found.Name, found.ID)
	}

	if added > 0 || updated > 0 {
		if err := store.SaveAccounts(filepath.Join(app.root, "accounts.json"), accounts); err != nil {
			return err
		}
		app.logger.Infof("accounts.json actualizado. Total de cuentas: %d.", len(accounts))
	}
	return nil
}

func commandRemove(args []string) error {
	names := make(map[string]struct{}, len(args))
	for _, name := range cleanNames(args) {
		names[strings.ToLower(name)] = struct{}{}
	}
	if len(names) == 0 {
		return fmt.Errorf("Indica al menos un nombre de usuario. Ejemplo: presence-bot remove Roblox")
	}

	app, err := buildContext(false)
	if err != nil {
		return err
	}
	accounts, err := store.LoadAccounts(filepath.Join(app.root, "accounts.json"))
	if err != nil {
		return err
	}

	remaining := make([]model.Account, 0, len(accounts))
	for _, account := range accounts {
		if _, remove := names[strings.ToLower(account.Username)]; remove {
			continue
		}
		remaining = append(remaining, account)
	}
	removed := len(accounts) - len(remaining)
	if removed == 0 {
		app.logger.Warnf("Ninguna de esas cuentas estaba en la lista.")
		return nil
	}

	if err := store.SaveAccounts(filepath.Join(app.root, "accounts.json"), remaining); err != nil {
		return err
	}
	app.logger.Infof("Cuenta(s) eliminada(s): %d. Quedan %d.", removed, len(remaining))
	return nil
}

func commandList() error {
	root := resolveRoot()
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	accounts, err := store.LoadAccounts(filepath.Join(root, "accounts.json"))
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		fmt.Println("No hay cuentas configuradas. Usa: presence-bot add <usuario>")
		return nil
	}
	state := store.LoadState(filepath.Join(root, "data", "state.json"), nil)

	fmt.Printf("%-20s %-12s %-42s %-24s %-24s\n", "Usuario", "ID", "Estado", "Desde", "Última señal")
	for _, account := range accounts {
		saved := state.Users[strconv.FormatInt(account.ID(), 10)]
		id := "sin resolver"
		if account.ID() > 0 {
			id = strconv.FormatInt(account.ID(), 10)
		}
		status := "Sin datos (ejecuta start/check)"
		since := "—"
		lastSeen := "—"
		if saved != nil {
			status = format.PresenceLabel(saved)
			if saved.Since != "" {
				since = format.LocalString(saved.Since, cfg.TimeZone)
			}
			if saved.LastSeenAt != nil && *saved.LastSeenAt != "" {
				lastSeen = format.LocalString(*saved.LastSeenAt, cfg.TimeZone)
			}
		}
		fmt.Printf("%-20s %-12s %-42s %-24s %-24s\n", "@"+account.Username, id, status, since, lastSeen)
	}
	fmt.Printf("Total: %d cuenta(s). Intervalo: %d s.\n", len(accounts), cfg.PollInterval())
	return nil
}

func commandCheck(args []string) error {
	app, err := buildContext(false)
	if err != nil {
		return err
	}
	asJSON := contains(args, "--json")

	accounts, err := store.LoadAccounts(filepath.Join(app.root, "accounts.json"))
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		fmt.Println("No hay cuentas configuradas. Usa: presence-bot add <usuario>")
		return nil
	}

	unresolved := make([]string, 0)
	for _, account := range accounts {
		if account.ID() == 0 {
			unresolved = append(unresolved, account.Username)
		}
	}
	if len(unresolved) > 0 {
		resolved, resolveErr := app.roblox.ResolveUsernames(unresolved)
		if resolveErr != nil {
			app.logger.Errorf("No se pudieron resolver las cuentas: %v", resolveErr)
		} else {
			for index := range accounts {
				if accounts[index].ID() != 0 {
					continue
				}
				if found, ok := resolved[strings.ToLower(accounts[index].Username)]; ok {
					accounts[index].UserID = model.Ptr(found.ID)
					accounts[index].Username = found.Name
					display := found.DisplayName
					if display == "" {
						display = found.Name
					}
					accounts[index].DisplayName = model.Ptr(display)
				}
			}
		}
	}

	valid := make([]model.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.ID() > 0 {
			valid = append(valid, account)
		}
	}
	if len(valid) == 0 {
		fmt.Println("No hay cuentas válidas para consultar.")
		return nil
	}

	cookieUser := validateRobloxCookie(app.roblox, app.logger)
	userIDs := make([]int64, 0, len(valid))
	for _, account := range valid {
		userIDs = append(userIDs, account.ID())
	}
	presences, err := app.roblox.GetPresences(userIDs)
	if err != nil {
		return err
	}
	byID := make(map[int64]presence.RawPresence, len(presences))
	for _, raw := range presences {
		byID[raw.UserID] = raw
	}

	type checkResult struct {
		account  model.Account
		presence *model.Presence
		err      string
	}
	results := make([]checkResult, 0, len(valid))
	for _, account := range valid {
		raw, ok := byID[account.ID()]
		if !ok {
			results = append(results, checkResult{
				account: account,
				err:     "Roblox no devolvió presencia para esta cuenta.",
			})
			continue
		}
		next := presence.NormalizePresence(raw)
		if next.PresenceType == 2 {
			next.Game = presence.ResolveGameDetails(app.roblox, next, app.logger)
		}
		results = append(results, checkResult{account: account, presence: &next})
	}

	now := time.Now().UTC()
	if asJSON {
		outputResults := make([]any, 0, len(results))
		for _, result := range results {
			entry := map[string]any{
				"username":     result.account.Username,
				"userId":       result.account.ID(),
				"state":        nil,
				"presenceType": nil,
				"game":         nil,
				"lastOnline":   nil,
				"error":        nil,
			}
			if result.err != "" {
				entry["error"] = result.err
			}
			if result.presence != nil {
				entry["state"] = format.PresenceLabel(result.presence)
				entry["presenceType"] = result.presence.PresenceType
				if result.presence.Game != nil {
					entry["game"] = result.presence.Game
				}
				if result.presence.LastOnline != nil {
					entry["lastOnline"] = *result.presence.LastOnline
				}
			}
			outputResults = append(outputResults, entry)
		}

		output := map[string]any{
			"checkedAt":  format.ISOMillis(now),
			"cookieUser": nil,
			"results":    outputResults,
		}
		if cookieUser != nil {
			output["cookieUser"] = map[string]any{"id": cookieUser.ID, "name": cookieUser.Name}
		}
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	for _, result := range results {
		fmt.Println()
		fmt.Printf("@%s (ID %d)\n", result.account.Username, result.account.ID())
		if result.err != "" {
			fmt.Printf("  Error: %s\n", result.err)
			continue
		}
		label := strings.ReplaceAll(format.PresenceLabel(result.presence), "**", "")
		fmt.Printf("  Estado: %s\n", label)
		fmt.Printf("  Consultado: %s (%s)\n", format.LocalString(format.ISOMillis(now), app.config.TimeZone), format.UTCString(now))
		if result.presence.LastOnline != nil {
			fmt.Printf(
				"  Última conexión conocida: %s (%s)\n",
				format.LocalString(*result.presence.LastOnline, app.config.TimeZone),
				format.UTCStringString(*result.presence.LastOnline),
			)
		}
		if game := result.presence.Game; game != nil {
			name := game.Name
			if name == "" {
				name = "no disponible"
			}
			fmt.Printf("  Juego: %s\n", name)
			placeID := game.RootPlaceID
			if placeID == nil {
				placeID = game.PlaceID
			}
			if placeID != nil {
				fmt.Printf("  Lugar: %d\n", *placeID)
			}
			if game.GameID != "" {
				fmt.Printf("  Servidor: %s\n", game.GameID)
			}
			if game.URL != "" {
				fmt.Printf("  Enlace: %s\n", game.URL)
			}
		}
	}

	if cookieUser == nil {
		fmt.Println("\nNota: sin cookie de Roblox no se puede ver el nombre del juego. Configura ROBLOSECURITY en .env.")
	}
	return nil
}

func commandTest() error {
	app, err := buildContext(false)
	if err != nil {
		return err
	}
	if err := requireWebhook(app.config); err != nil {
		return err
	}

	webhookInfo, err := app.discord.Validate()
	if err != nil {
		return err
	}
	cookieUser := validateRobloxCookie(app.roblox, app.logger)
	accounts, err := store.LoadAccounts(filepath.Join(app.root, "accounts.json"))
	if err != nil {
		return err
	}

	cookieName := ""
	if cookieUser != nil {
		cookieName = cookieUser.Name
	}
	payload := events.BuildTestPayload(
		app.config,
		mapString(webhookInfo, "channel_id", "desconocido"),
		len(accounts),
		cookieName,
	)
	if _, err := app.discord.Send(payload, false); err != nil {
		return err
	}

	if app.config.IsDryRun() {
		app.logger.Infof("Mensaje de prueba generado en modo dry-run (no enviado).")
	} else {
		app.logger.Infof("Mensaje de prueba enviado al canal %s.", mapString(webhookInfo, "channel_id", "desconocido"))
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cleanNames(values []string) []string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		name := store.CleanUsername(value)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func printHelp() {
	fmt.Print(`
Roblox Presence Monitor
=======================

Uso:
  presence-bot start [--dry-run] [--interval 30]
      Inicia el monitor y envía los cambios al webhook.

  presence-bot add <usuario> [usuario2 ...]
      Añade cuentas por nombre de usuario (sin @).

  presence-bot remove <usuario> [usuario2 ...]
      Deja de vigilar esas cuentas.

  presence-bot list
      Muestra las cuentas configuradas y su último estado guardado.

  presence-bot check [--json]
      Consulta el estado actual sin enviar nada a Discord.

  presence-bot status
      Publica o actualiza el panel de estado en Discord (sin dejar el monitor activo).

  presence-bot test
      Envía un mensaje de prueba al webhook.

  presence-bot help
      Muestra esta ayuda.

Archivos:
  accounts.json   Cuentas vigiladas
  config.json     Webhook, intervalo y opciones
  .env            Cookie de Roblox y webhook (opcional)
  data/state.json Estado interno y duraciones

Notas:
  - Para ver el NOMBRE del juego hace falta una cookie .ROBLOSECURITY.
  - La fecha se muestra con la hora local de cada persona en Discord y
    también en UTC. La detección tiene un margen igual al intervalo.
  - El panel de estado es un mensaje fijo que se edita en cada consulta.
    Desactívalo con "statusPanel": { "enabled": false } en config.json.
`)
}
