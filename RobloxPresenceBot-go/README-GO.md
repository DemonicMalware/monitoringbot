# Roblox Presence Monitor (Go)

Port en Go del monitor de presencia. Usa solo la biblioteca estándar, no
necesita `npm install` ni dependencias externas.

Vigila las cuentas de `accounts.json`, detecta conexión, desconexión, inicio y
cambio de juego, salida, Roblox Studio y cambios de estado, y publica los
avisos en un webhook de Discord. También mantiene un panel fijo que se edita en
cada consulta.

## Comandos

```text
presence-bot start [--dry-run] [--interval 30]
presence-bot add <usuario> [usuario2 ...]
presence-bot remove <usuario> [usuario2 ...]
presence-bot list
presence-bot check [--json]
presence-bot status
presence-bot test
presence-bot help
```

## Configuración

`config.json` contiene el intervalo, las notificaciones y el panel. El webhook
se lee de `DISCORD_WEBHOOK_URL` en `.env` (o del entorno del servidor).

Copia `.env.example` a `.env` y configura como mínimo:

```env
DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/...
```

Opcionales:

```env
ROBLOSECURITY=...            # necesaria para ver nombres de juegos y servidores
POLL_INTERVAL_SECONDS=30
TIME_ZONE=America/Argentina/Buenos_Aires
LOG_LEVEL=info
```

Nunca subas `.env` a git ni lo compartas. Si la cookie se compartió por chat,
rótala desde Roblox.

## Compilar

```powershell
$env:CGO_ENABLED = "0"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -trimpath -ldflags="-s -w" -o presence-bot .
```

El resultado es un binario estático para Linux amd64. Las pruebas se ejecutan
con:

```powershell
go test ./...
```

## Desplegar en Bot-Hosting

El runtime Go de Bot-Hosting no conserva los archivos del despliegue Node.js.
Antes de cambiar de runtime, descarga una copia de seguridad o conserva el ZIP
de Node.js.

1. En Bot-Hosting, cambia el runtime de Node.js a Go. Acepta que se borren los
   archivos actuales.
2. Sube el contenido del paquete de despliegue a `/home/container`:
   `presence-bot`, `config.json`, `accounts.json`, `data/state.json`,
   `.env.example`, `README-GO.md`.
3. En la pestaña de variables de entorno, añade `DISCORD_WEBHOOK_URL`.
   Opcionalmente añade `ROBLOSECURITY`, `TIME_ZONE`, `POLL_INTERVAL_SECONDS` y
   `LOG_LEVEL`.
4. Configura el comando de inicio:

   ```text
   cd /home/container && chmod +x ./presence-bot && ./presence-bot
   ```

5. Inicia el servidor y revisa la consola. Debe aparecer:

   ```text
   Webhook OK: ... (canal ...)
   Presencia actualizada: 2/2 cuenta(s) (estado inicial).
   Panel de estado publicado (mensaje ...).
   Monitor activo.
   ```

`data/state.json` conserva el ID del panel existente, así que el bot editará el
mismo mensaje en lugar de crear otro. Si el mensaje fue borrado, lo recrea
automáticamente.

Si rotaste el webhook (recomendado si el anterior se compartió), el webhook
nuevo no puede editar el panel creado por el anterior. El bot recibirá 404/403,
publicará un panel nuevo y seguirá funcionando; puedes borrar el mensaje viejo
a mano.

## Notas

- El plan gratuito de Bot-Hosting pide renovación manual cada 4 días.
- El binario incluido es Linux amd64. Si el servidor usa otra arquitectura,
  recompila con `GOOS`/`GOARCH` correspondientes.
- El historial de cambios se guarda en `data/events.jsonl` y los logs en
  `data/monitor.log`.
