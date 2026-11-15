# Roblox Presence Simple

Bot simple en Go para avisar por Discord cuando una cuenta de Roblox:

- se conecta;
- empieza a jugar o cambia de juego;
- se desconecta.

Los avisos son mensajes de texto planos, con fecha y hora, y se envían a un
webhook de Discord. No usa embeds, panel ni base de datos.

Además de los avisos puntuales, el bot mantiene **un mensaje de estado por cada
usuario**. Ese mensaje se edita en el mismo lugar cuando la cuenta se conecta,
empieza a jugar, cambia de juego o se desconecta. Así puedes ver el estado de
cada cuenta por separado en todo momento:

- 🟢 activo (en línea);
- 🎮 jugando (con el nombre y enlace del juego);
- 🛠️ activo en Roblox Studio;
- ⚪ desconectado.

Las cuentas configuradas por defecto son:

- `agustin563554`
- `agusstinn2801`

## Por qué es fácil de instalar

- Solo usa la biblioteca estándar de Go: no hay dependencias externas.
- Se puede compilar directamente con el runtime Go de Bot-Hosting.
- Guarda el estado en `data/simple-state.json` para no duplicar avisos ni
  tableros al reiniciar.
- Mantiene un tablero de estado por usuario, editado en el mismo mensaje.
- Incluye un servidor HTTP de salud en `/health`.
- Reintenta las consultas a Roblox y los envíos a Discord.

## Instalación rápida en Bot-Hosting (recomendada)

1. Detén el servidor en el panel.
2. Sube el archivo `RobloxPresenceSimple-install.sh` a `/home/container`.
3. En la consola ejecuta:

   ```sh
   sh /home/container/RobloxPresenceSimple-install.sh
   ```

   El webhook ya va incluido en el instalador. Si algún día quieres usar otro,
   pásalo como argumento:

   ```sh
   sh /home/container/RobloxPresenceSimple-install.sh 'https://discord.com/api/webhooks/...'
   ```

4. Pulsa **Start**. El runtime Go compila el proyecto y lo ejecuta.

El script crea todos los archivos en `/home/container`, guarda el webhook en
`.env` y conserva un `config.json` del bot simple si ya existe. Si encuentra un
`config.json` de otro bot, lo respalda en `_backup_simple_*` y crea uno nuevo.
Los archivos `.go` anteriores de la raíz también se mueven a ese respaldo para
evitar paquetes duplicados.

### Alternativa con ZIP

1. Detén el servidor.
2. Sube `RobloxPresenceSimple-deploy.zip` y extráelo en `/home/container`.
   Los archivos deben quedar en la raíz, no dentro de una subcarpeta:

   ```text
   /home/container/main.go
   /home/container/go.mod
   /home/container/config.json
   /home/container/config.go
   /home/container/roblox.go
   /home/container/discord.go
   /home/container/state.go
   /home/container/monitor.go
   /home/container/.env
   ```

3. El ZIP ya incluye el webhook en `.env`. Si quieres comprobarlo, abre `.env`:

   ```env
   DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/...
   ```

4. Pulsa **Start**.

El ZIP contiene solo los archivos necesarios para el monitoreo (no incluye
tests).

## Configuración

El archivo `config.json` trae valores listos para usar:

```json
{
  "usernames": [
    "agustin563554",
    "agusstinn2801"
  ],
  "pollIntervalSeconds": 10,
  "timeZone": "America/Puerto_Rico",
  "timeZoneLabel": "hora Puerto Rico",
  "stateFile": "data/simple-state.json",
  "port": "8080",
  "notify": {
    "connect": true,
    "disconnect": true,
    "gameStart": true,
    "gameChange": true,
    "leaveGame": false,
    "startupSummary": true
  }
}
```

### Agregar más usuarios

Añade el nombre exacto de Roblox a `usernames`:

```json
"usernames": [
  "agustin563554",
  "agusstinn2801",
  "otro_usuario"
]
```

También puedes usar la variable de entorno:

```env
USERNAMES=agustin563554,agusstinn2801,otro_usuario
```

### Opciones principales

| Opción | Descripción |
| --- | --- |
| `pollIntervalSeconds` | Segundos entre consultas. Mínimo `5`; por defecto `10`. |
| `timeZone` | Zona horaria de los mensajes. Por defecto Puerto Rico. |
| `timeZoneLabel` | Texto que aparece después de la hora. |
| `stateFile` | Archivo donde se guardan las sesiones. |
| `port` | Puerto del servidor HTTP de salud. También acepta `PORT`. |
| `notify.connect` | Avisar cuando una cuenta se conecta. |
| `notify.disconnect` | Avisar cuando se desconecta. |
| `notify.gameStart` | Avisar cuando empieza a jugar. |
| `notify.gameChange` | Avisar cuando cambia de juego. |
| `notify.leaveGame` | Avisar cuando sale del juego pero sigue en línea. |
| `notify.startupSummary` | Enviar un resumen al iniciar. |

`webhookUrl` y `robloxCookie` también se pueden poner en `config.json`, pero
es mejor usar `.env` o las variables del panel para no guardar secretos en el
código.

### Variables de entorno

| Variable | Uso |
| --- | --- |
| `DISCORD_WEBHOOK_URL` | Webhook de Discord (obligatorio para enviar). |
| `USERNAMES` | Lista separada por comas; reemplaza a `config.json`. |
| `POLL_INTERVAL_SECONDS` | Intervalo en segundos. |
| `TIME_ZONE` | Zona horaria. |
| `TIME_ZONE_LABEL` | Etiqueta de la zona horaria. |
| `STATE_FILE` | Ruta del estado. |
| `PORT` | Puerto HTTP. |
| `DRY_RUN` | `1` para no enviar mensajes a Discord. |
| `ROBLOSECURITY` | Cookie opcional de Roblox. No se muestra en los logs. |

Las variables de entorno tienen prioridad sobre `config.json`.

## Comandos útiles

Con Go instalado, desde la carpeta del proyecto:

```sh
go run . -check
go run . -dry-run -once
go run . -test-message
go run .

# Binario local
go build -o presence-simple .
```

- `-check`: resuelve los usuarios, consulta su presencia y verifica el webhook.
- `-dry-run`: muestra los mensajes en consola sin enviarlos.
- `-once`: hace una sola consulta, crea o actualiza los tableros y termina.
- `-test-message`: envía un mensaje de prueba al webhook.

Si ya tienes el binario compilado, usa los mismos argumentos con
`./presence-simple`.

En la primera consulta se crean dos mensajes de estado, uno por usuario. Luego
se editan en el mismo lugar cada vez que cambia su estado. Los IDs de esos
mensajes se guardan en `data/simple-state.json`, así que al reiniciar se siguen
editando los mismos tableros y no se duplican.

## Ejemplos de mensajes

Tablero de estado de cada usuario (se edita en el mismo mensaje):

```text
📊 Estado de agustin563554
🟢 Activo (en línea)
📅 Actualizado: 15/11/2026 03:45:12 (hora Puerto Rico)
```

```text
📊 Estado de agustin563554
🎮 Jugando: Build to Fight Zombies
🔗 https://www.roblox.com/games/83385558650869
📅 Actualizado: 15/11/2026 03:50:40 (hora Puerto Rico)
```

```text
📊 Estado de agusstinn2801
⚪ Desconectado
📅 Actualizado: 15/11/2026 05:12:40 (hora Puerto Rico)
```

Aviso de conexión:

```text
🟢 agustin563554 se conectó a Roblox
🌐 En línea en el sitio web
📅 Fecha y hora: 15/11/2026 03:45:12 (hora Puerto Rico)
```

Aviso de juego:

```text
🎮 agustin563554 está jugando: Build to Fight Zombies
🔗 https://www.roblox.com/games/83385558650869
📅 Fecha y hora: 15/11/2026 03:45:12 (hora Puerto Rico)
```

Aviso de desconexión:

```text
🔴 agustin563554 se desconectó de Roblox
📅 Fecha y hora: 15/11/2026 05:12:40 (hora Puerto Rico)
⏱️ Duración de la sesión: 1h 27m
```

Al iniciar se envía un resumen con el estado actual. El primer sondeo no
genera avisos de conexión falsos, para evitar mensajes duplicados después de
un reinicio.

## Servidor HTTP

- `/health`: estado del monitor en JSON.
- `/`: texto simple para comprobar que el proceso está vivo.

Ejemplo:

```json
{
  "status": "ok",
  "uptimeSeconds": 120,
  "lastPollAt": "2026-11-15T07:45:12Z",
  "lastSuccessAt": "2026-11-15T07:45:12Z",
  "consecutiveErrors": 0,
  "messagesSent": 1,
  "accounts": [
    {
      "username": "agustin563554",
      "userId": 1850652816,
      "online": true,
      "status": "jugando: Build to Fight Zombies"
    }
  ]
}
```

Si el puerto está ocupado, el bot avisa en la consola y sigue funcionando. El
servidor HTTP es opcional; no es necesario para enviar los avisos.

## Comportamiento ante errores

- Si Roblox falla, se conserva el último estado y se reintenta en la siguiente
  consulta.
- Si una cuenta no aparece en la respuesta de Roblox, se conserva su estado
  anterior. Tras tres consultas seguidas sin datos se marca como desconectada.
- Si Discord devuelve `429` o un error `5xx`, se reintenta con espera.
- Si el mensaje de estado fue eliminado en Discord, el bot crea uno nuevo en la
  siguiente actualización.
- Si el estado está dañado, se guarda una copia `.corrupt-*` y se empieza de
  cero.
- El estado se escribe de forma atómica para no dejar archivos a medias.

## Seguridad

- No subas `.env` ni `config.json` con secretos a un repositorio público.
- Si un webhook se publica accidentalmente, elimínalo en Discord y crea otro.
- El bot no registra la URL del webhook ni la cookie de Roblox.

## Solución de problemas

### `Falta DISCORD_WEBHOOK_URL`

Crea `.env` en la misma carpeta que el ejecutable:

```env
DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/...
```

O configura la variable en el panel de Bot-Hosting.

### `Roblox no encontró estos usuarios`

Revisa que el nombre esté escrito exactamente igual que en Roblox. También
puedes usar el ID numérico guardado en `data/simple-state.json` si la cuenta
cambió de nombre.

### `no required module provides package main.go`

Los archivos no están en la raíz de `/home/container`. Mueve `main.go`,
`go.mod` y el resto de archivos `.go` a la raíz y vuelve a iniciar.

### El bot no envía mensajes

Ejecuta:

```sh
./presence-simple -check
./presence-simple -test-message
```

Revisa que el webhook siga existiendo y que el canal permita el webhook.

### La hora no es la esperada

Cambia `timeZone` y `timeZoneLabel` en `config.json`. La zona horaria usa la
base incluida en el binario, así que no depende de los paquetes del servidor.

Revisa también que el panel no tenga `TIME_ZONE` o `TIME_ZONE_LABEL` con otro
valor: las variables de entorno tienen prioridad.

### El intervalo no es de 10 segundos

Revisa `POLL_INTERVAL_SECONDS` en `.env` o en las variables del panel. Si existe,
tiene prioridad sobre `config.json`. Para avisos casi en tiempo real usa `10` (el
mínimo permitido es `5`).

### Roblox responde 401 o 403

Si configuraste `ROBLOSECURITY`, quita esa variable o renueva la cookie. El bot
funciona sin cookie para las presencias públicas.

### Cambio desde el monitor completo

El bot simple no actualiza el panel del monitor completo: envía mensajes de
texto al canal. Ejecuta solo uno de los dos bots para no duplicar avisos. El
instalador no borra las carpetas del monitor anterior; solo mueve el código Go
que esté en la raíz a un respaldo.
