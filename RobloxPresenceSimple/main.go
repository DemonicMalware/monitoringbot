package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "config.json", "archivo de configuración")
	checkOnly := flag.Bool("check", false, "valida configuración, usuarios, presencia y webhook sin iniciar el monitor")
	dryRun := flag.Bool("dry-run", false, "muestra los mensajes en consola sin enviarlos a Discord")
	testMessage := flag.Bool("test-message", false, "envía un mensaje de prueba al webhook")
	once := flag.Bool("once", false, "hace una sola consulta, envía los avisos y termina")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.Ldate|log.Ltime)

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		logger.Fatalf("Configuración inválida: %v", err)
	}
	if *dryRun {
		cfg.DryRun = true
	}

	if *checkOnly || *testMessage {
		if err := runCommand(*configPath, cfg, *checkOnly, *testMessage, logger); err != nil {
			logger.Fatalf("%v", err)
		}
		return
	}

	if cfg.WebhookURL == "" && !cfg.DryRun {
		logger.Fatalf("Falta DISCORD_WEBHOOK_URL. Configúrala en las variables de entorno del panel o crea un archivo .env con:\nDISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/...")
	}

	statePath := cfg.StatePath()
	state := LoadState(statePath, logger)
	roblox := NewRobloxClient(cfg.RobloxCookie)
	discord := NewDiscordClient(cfg.WebhookURL, cfg.DryRun, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	resolveCtx, cancelResolve := context.WithTimeout(ctx, 30*time.Second)
	accounts, err := resolveAccounts(resolveCtx, roblox, cfg.Usernames, state, logger)
	cancelResolve()
	if err != nil {
		logger.Fatalf("No se pudieron preparar las cuentas: %v", err)
	}
	for _, account := range accounts {
		logger.Printf("Cuenta: %s (ID %d)", account.Username, account.UserID)
	}
	if cfg.RobloxCookie != "" {
		logger.Printf("Cookie de Roblox configurada (no se muestra por seguridad).")
	}

	if cfg.DryRun {
		logger.Printf("Modo dry-run: los mensajes se muestran en consola y no se envían a Discord.")
	} else {
		verifyCtx, cancelVerify := context.WithTimeout(ctx, 10*time.Second)
		info, verifyErr := discord.Info(verifyCtx)
		cancelVerify()
		if verifyErr != nil {
			var httpErr *DiscordHTTPError
			if errors.As(verifyErr, &httpErr) && (httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusNotFound) {
				logger.Fatalf("El webhook de Discord no es válido: %v", verifyErr)
			}
			logger.Printf("Aviso: no se pudo verificar el webhook ahora (%v). El monitor intentará enviar los avisos igualmente.", verifyErr)
		} else {
			logger.Printf("Webhook OK: %s (canal %s).", info.Name, info.ChannelID)
		}
	}

	monitor := NewMonitor(cfg, roblox, discord, state, accounts, logger)
	if *once {
		if err := monitor.poll(ctx); err != nil {
			logger.Fatalf("No se pudo consultar Roblox: %v", err)
		}
		if err := monitor.Save(); err != nil {
			logger.Printf("No se pudo guardar el estado: %v", err)
		}
		logger.Printf("Consulta única completada.")
		return
	}
	server := startHealthServer(cfg, monitor, logger)

	logger.Printf("Monitor activo. Usuarios: %d. Intervalo: %s. Zona horaria: %s.", len(accounts), cfg.PollInterval(), cfg.TimeZone)
	logger.Printf("Estado: %s", statePath)
	logger.Printf("Ctrl+C para detener.")

	monitor.Run(ctx)

	logger.Printf("Deteniendo monitor...")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	if server != nil {
		_ = server.Shutdown(shutdownCtx)
	}
	cancelShutdown()
	if err := monitor.Save(); err != nil {
		logger.Printf("No se pudo guardar el estado final: %v", err)
	} else {
		logger.Printf("Estado guardado en %s.", statePath)
	}
}

func runCommand(configPath string, cfg *Config, checkOnly bool, testMessage bool, logger *log.Logger) error {
	if cfg.WebhookURL == "" && !cfg.DryRun {
		return fmt.Errorf("falta DISCORD_WEBHOOK_URL. Configúrala en .env o en las variables de entorno del panel")
	}

	state := LoadState(cfg.StatePath(), logger)
	roblox := NewRobloxClient(cfg.RobloxCookie)
	discord := NewDiscordClient(cfg.WebhookURL, cfg.DryRun, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	accounts, err := resolveAccounts(ctx, roblox, cfg.Usernames, state, logger)
	if err != nil {
		return fmt.Errorf("no se pudieron preparar las cuentas: %w", err)
	}
	for _, account := range accounts {
		logger.Printf("Cuenta: %s (ID %d)", account.Username, account.UserID)
	}

	if cfg.WebhookURL != "" {
		info, err := discord.Info(ctx)
		if err != nil {
			return fmt.Errorf("el webhook no es válido: %w", err)
		}
		logger.Printf("Webhook OK: %s (canal %s).", info.Name, info.ChannelID)
	}

	if checkOnly {
		ids := make([]int64, len(accounts))
		for i, account := range accounts {
			ids[i] = account.UserID
		}
		presences, err := roblox.FetchPresence(ctx, ids)
		if err != nil {
			return fmt.Errorf("no se pudo consultar la presencia: %w", err)
		}
		fmt.Println()
		fmt.Printf("Archivo de configuración: %s\n", configPath)
		fmt.Printf("Intervalo: %s\n", cfg.PollInterval())
		fmt.Printf("Zona horaria: %s\n", cfg.TimeZone)
		fmt.Println("Estado actual:")
		for _, account := range accounts {
			status := "sin datos"
			if presence, ok := presences[account.UserID]; ok {
				status = presenceLabel(presence.UserPresenceType, presence.LastLocation)
			}
			fmt.Printf("  %-20s ID %-12d %s\n", account.Username, account.UserID, status)
		}
		fmt.Println("\nTodo listo. Puedes iniciar el monitor.")
		return nil
	}

	if testMessage {
		lines := []string{
			"✅ Prueba del monitor de presencia",
			"👥 Usuarios: " + accountNames(accounts),
			"📅 Fecha y hora: " + formatMomentFor(cfg, time.Now()),
		}
		if err := discord.Send(ctx, strings.Join(lines, "\n")); err != nil {
			return fmt.Errorf("no se pudo enviar el mensaje de prueba: %w", err)
		}
		if cfg.DryRun {
			logger.Printf("Mensaje de prueba mostrado en consola (dry-run).")
		} else {
			logger.Printf("Mensaje de prueba enviado correctamente.")
		}
	}
	return nil
}

func resolveAccounts(ctx context.Context, client *RobloxClient, usernames []string, state *State, logger *log.Logger) ([]Account, error) {
	resolved, resolveErr := client.Resolve(ctx, usernames)
	byName := make(map[string]ResolvedUser, len(resolved)*2)
	if resolveErr == nil {
		for _, user := range resolved {
			if user.ID == 0 {
				continue
			}
			if user.RequestedUsername != "" {
				byName[strings.ToLower(user.RequestedUsername)] = user
			}
			if user.Name != "" {
				byName[strings.ToLower(user.Name)] = user
			}
		}
	} else {
		logger.Printf("Aviso: no se pudieron resolver los nombres en Roblox: %v", resolveErr)
	}

	accounts := make([]Account, 0, len(usernames))
	var missing []string
	for _, name := range usernames {
		if user, ok := byName[strings.ToLower(name)]; ok && user.ID != 0 {
			username := user.Name
			if username == "" {
				username = name
			}
			displayName := user.DisplayName
			if displayName == "" {
				displayName = username
			}
			accounts = append(accounts, Account{
				UserID:      user.ID,
				Username:    username,
				DisplayName: displayName,
			})
			continue
		}
		if cached := findStateByUsername(state, name); cached != nil && cached.UserID != 0 {
			username := cached.Username
			if username == "" {
				username = name
			}
			logger.Printf("Aviso: se usa el ID guardado para %s (%d).", name, cached.UserID)
			accounts = append(accounts, Account{
				UserID:      cached.UserID,
				Username:    username,
				DisplayName: cached.DisplayName,
			})
			continue
		}
		missing = append(missing, name)
	}

	if len(missing) > 0 {
		if resolveErr != nil {
			return nil, fmt.Errorf("no se encontraron %s y falló la consulta a Roblox: %w", strings.Join(missing, ", "), resolveErr)
		}
		return nil, fmt.Errorf("Roblox no encontró estos usuarios: %s", strings.Join(missing, ", "))
	}
	return accounts, nil
}

func findStateByUsername(state *State, username string) *AccountState {
	if state == nil {
		return nil
	}
	for _, account := range state.Accounts {
		if account != nil && strings.EqualFold(account.Username, username) {
			return account
		}
	}
	return nil
}

func accountNames(accounts []Account) string {
	names := make([]string, 0, len(accounts))
	for _, account := range accounts {
		names = append(names, account.Username)
	}
	return strings.Join(names, ", ")
}

func startHealthServer(cfg *Config, monitor *Monitor, logger *log.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(monitor.Snapshot()); err != nil {
			logger.Printf("No se pudo escribir /health: %v", err)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "Roblox Presence Simple activo.\nUsuarios: %d\nEstado: /health\n", len(monitor.accounts))
	})

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Printf("Servidor HTTP de salud: http://0.0.0.0:%s/health", cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("Aviso: no se pudo iniciar el servidor HTTP en el puerto %s: %v. El monitor sigue funcionando.", cfg.Port, err)
		}
	}()
	return server
}
