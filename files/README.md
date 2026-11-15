# monitoringbot (Go)

Monitorea cuentas de Roblox por username y avisa por webhook de Discord cuando se conectan,
se desconectan, entran a jugar (con el juego), cambian de juego o abren Roblox Studio,
siempre con fecha y hora exacta. Usa solo la librería estándar de Go (sin dependencias).

## Archivos
- `main.go`, `go.mod` — código fuente
- `main_test.go` — tests con Roblox y Discord simulados (`go test ./...`)
- `monitoringbot` — binario ya compilado para Linux x86-64 (no va a GitHub)
- `.env.example` — plantilla de configuración (copiar a `.env`)

## Configuración
Crear `.env` junto al programa a partir de `.env.example`:
`DISCORD_WEBHOOK_URL`, `ROBLOX_USERNAMES`, `ROBLOSECURITY`, y opcionalmente
`POLL_INTERVAL`, `SUMMARY_INTERVAL_MIN`, `TIMEZONE`.

## Ejecutar
Con el binario:      `chmod +x monitoringbot && ./monitoringbot`
Desde el código:     `go run .`
Compilar vos mismo:  `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o monitoringbot .`
(para ARM usar `GOARCH=arm64`)
