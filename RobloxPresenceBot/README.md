# 🎮 Roblox Presence Monitor

Bot de monitoreo de cuentas de Roblox por **nombre de usuario**. Consulta la
presencia cada X segundos y publica los cambios en un canal de Discord mediante
webhook, además de mantener un **panel de estado fijo** con la situación actual
de cada cuenta.

No necesita token de bot de Discord. Solo requiere Node.js 18+ y la dependencia
`sillydev` (SDK de monitoreo de SillyDev).
Está pensado para ejecutarse 24/7 en el hosting gratuito de **SillyDev**.

## 📡 Panel de estado visible

Además de los avisos de cada cambio, el bot crea **un único mensaje** en el canal
que se va editando en cada consulta:

```text
📡 Estado de presencia en Roblox
Estado actual de las cuentas vigiladas. Este mensaje se actualiza
automáticamente cada 30 s. Cada persona ve las horas en su propia zona horaria.

🟢 @agustin563554
Jugando **Adopt Me!**
🔗 Abrir juego
🕒 En línea desde: 14/11/2026, 18:30:12 (hace 12 minutos)
🎮 Jugando desde: 14/11/2026, 18:31:40 (hace 10 minutos)

⚪ @agusstinn2801
Desconectado
🕒 Última conexión: 14/11/2026, 17:05:03 (hace 1 hora)
⏳ Última sesión: 2 h 14 min
```

- Se publica una sola vez y se **edita** siempre el mismo mensaje, así que no
  llena el canal.
- Muestra `En línea desde`, `Jugando desde`, `Última conexión` y `Última sesión`.
- Las horas usan marcas de Discord (`<t:...>`): cada persona las ve en **su**
  zona horaria. El pie incluye la hora UTC exacta de la última actualización.
- Si alguien borra el mensaje del panel, el bot lo detecta (HTTP 404) y publica
  uno nuevo automáticamente.
- Se puede desactivar con `"statusPanel": { "enabled": false }` en `config.json`.

## Qué notifica

| Evento | Ejemplo |
|---|---|
| Se conecta | 🟢 Se conectó a Roblox |
| Se conecta y entra directo a un juego | 🟢 Se conectó y está jugando **Adopt Me!** |
| Empieza a jugar | 🎮 Empezó a jugar **Blox Fruits** |
| Cambia de juego | 🔄 Cambió de juego |
| Sale del juego | 🚪 Salió del juego (con tiempo jugado) |
| Se desconecta | 🔴 Se desconectó (con tiempo en línea) |
| Abre/cierra Roblox Studio | 🛠️ Abrió / cerró Roblox Studio |

Cada aviso incluye:

- Fecha y hora exactas con marca de tiempo de Discord (`<t:...:F>`), que se muestra
  en la zona horaria de cada persona, más la fecha UTC en el pie.
- Hora relativa (“hace 2 minutos”).
- Usuario, ID, estado, juego, lugar y servidor cuando están disponibles.
- Duración de la sesión o del juego al desconectarse / salir.
- Botones para abrir el juego o unirse al servidor.

## Requisitos

- **Node.js 18.17 o superior** (recomendado Node 20).
- Un webhook de Discord (ya viene configurado en `config.json`).
- Opcional pero recomendado: una cookie `.ROBLOSECURITY` para ver el **nombre del
  juego**, el lugar y el servidor.

## Inicio rápido

Las cuentas ya están configuradas en `accounts.json`:

- `agustin563554` → ID `1850652816`
- `agusstinn2801` → ID `5513160828`

```powershell
cd C:\Users\Usuario\Documents\RobloxPresenceBot

# 1. Comprueba que se ven sus estados (no envía nada a Discord)
node bot.js check

# 2. Publica el panel de estado en Discord sin dejar el monitor activo
node bot.js status

# 3. Envía un mensaje de prueba al webhook
node bot.js test

# 4. Inicia el monitor (queda activo y actualiza el panel cada 30 s)
node bot.js start
```

También puedes hacer doble clic en **`iniciar.bat`**.

Deja la ventana abierta mientras quieras monitorear. `Ctrl+C` detiene el bot y
guarda el estado.

## Comandos

```text
node bot.js start [--dry-run] [--interval 30]   Inicia el monitor
node bot.js add <usuario> [usuario2 ...]        Añade cuentas
node bot.js remove <usuario> [...]              Quita cuentas
node bot.js list                                Lista y muestra último estado
node bot.js check [--json]                      Consulta en vivo sin enviar nada
node bot.js status [--dry-run]                  Publica/actualiza el panel de estado
node bot.js test                                Mensaje de prueba al webhook
node bot.js help                                Ayuda
```

`add` resuelve el nombre de usuario a ID y lo guarda en `accounts.json`. Si luego
cambia el nombre visible de la cuenta, el ID sigue siendo el mismo.

Puedes editar `accounts.json` a mano:

```json
[
  { "username": "agustin563554", "userId": 1850652816 },
  { "username": "agusstinn2801", "userId": 5513160828 }
]
```

## Ver el nombre del juego (cookie de Roblox)

La API pública de presencia de Roblox devuelve `userPresenceType = 2` (“en juego”)
pero **oculta el nombre del juego, el lugar y el servidor** si la petición no va
autenticada. Para obtenerlos, el bot puede usar la cookie de una cuenta de Roblox.

> ⚠️ **Seguridad:** una cookie `.ROBLOSECURITY` da acceso completo a la cuenta.
> Usa una **cuenta secundaria** para esto, nunca tu cuenta principal. No compartas
> la cookie con nadie. El archivo `.env` está excluido de git.
> El uso de cookies para automatización puede infringir los Términos de Roblox;
> úsalo bajo tu responsabilidad.

Cómo configurarla:

1. Inicia sesión en `https://www.roblox.com` con la cuenta secundaria.
2. Abre las herramientas de desarrollo (F12).
3. Ve a **Application** (Chrome/Edge) o **Storage** (Firefox) → **Cookies** →
   `https://www.roblox.com`.
4. Copia el valor de `.ROBLOSECURITY` (es largo; incluye el texto
   `_|WARNING:-DO-NOT-SHARE...` si aparece).
5. Copia `.env.example` a `.env` y pega el valor:

```env
ROBLOSECURITY=_|WARNING:-DO-NOT-SHARE-THIS...
```

6. Reinicia el bot. En el arranque debe decir:
   `Cookie de Roblox válida: @tu_cuenta`.

También puedes ponerla en `config.json` como `"robloxCookie": "..."`, pero `.env`
es más seguro.

### Privacidad de presencia en Roblox

Para que la cuenta sea visible, en Roblox ve a **Configuración → Privacidad →
¿Quién puede ver mi estado en línea?** y elige **Todos**. Si está en **Amigos**,
la cuenta de la cookie debe ser amiga de la cuenta vigilada. Si está en **Nadie**,
la API la devolverá como desconectada y no hay forma de monitorearla.

## Configuración

`config.json` (o `config.local.json` para no modificarlo, o variables de entorno):

| Opción | Por defecto | Descripción |
|---|---:|---|
| `webhookUrl` | *(ya configurado)* | URL del webhook de Discord. |
| `pollIntervalSeconds` | `30` | Cada cuántos segundos consultar (mínimo 10). |
| `statusPanel.enabled` | `true` | Publica y edita el panel de estado fijo. |
| `notify.connect` | `true` | Avisar cuando se conecta. |
| `notify.disconnect` | `true` | Avisar cuando se desconecta. |
| `notify.gameStart` | `true` | Avisar cuando empieza a jugar. |
| `notify.gameChange` | `true` | Avisar cuando cambia de juego. |
| `notify.leaveGame` | `true` | Avisar cuando sale del juego. |
| `notify.studio` | `true` | Avisar cuando abre/cierra Studio. |
| `notify.statusChange` | `true` | Otros cambios de estado. |
| `notify.startupSummary` | `false` | Resumen extra al iniciar (el panel ya muestra el estado). |
| `mention` | `""` | Ej. `"<@&ID_DEL_ROL>"` o `"@here"` para mencionar en cada aviso. |
| `webhookUsername` | `""` | Cambia el nombre visible del webhook. |
| `webhookAvatarUrl` | `""` | Cambia el avatar del webhook. |
| `timeZone` | sistema | Zona horaria de los logs, ej. `America/Argentina/Buenos_Aires`. |
| `logLevel` | `info` | `debug`, `info`, `warn` o `error`. |
| `dryRun` | `false` | Muestra los mensajes en consola sin enviarlos. |

Variables de entorno equivalentes: `DISCORD_WEBHOOK_URL`, `ROBLOSECURITY`,
`POLL_INTERVAL_SECONDS`, `TIME_ZONE`, `LOG_LEVEL`, `DRY_RUN=1`.

Para probar sin enviar nada al canal:

```powershell
node bot.js start --dry-run
node bot.js status --dry-run
```

## Cómo funciona

1. Cada `pollIntervalSeconds` consulta
   `POST https://presence.roblox.com/v1/presence/users` con los IDs de las cuentas
   (en lotes de 50).
2. Compara el estado nuevo con el guardado en `data/state.json` y detecta
   transiciones: desconectado → en línea, en línea → jugando, cambio de juego,
   etc.
3. Calcula duraciones con las marcas de tiempo guardadas.
4. Envía un embed al webhook con una cola que respeta el límite de Discord
   (429 / `Retry-After`).
5. Crea o edita el **panel de estado** con el estado actual y las horas.
6. Guarda el estado después de cada consulta. Si reinicias el bot, no repite
   avisos de estados que ya conocía y sigue editando el mismo panel.

### Sobre “tiempo real”

Roblox no ofrece un websocket público de presencia. El bot usa **polling**: el
estado se consulta cada 30 segundos por defecto (puedes bajarlo a 10). Por eso:

- La hora que se muestra es la **hora exacta en que el bot detectó el cambio**,
  con un margen máximo igual al intervalo. Al arrancar por primera vez, “En línea
  desde” será la hora de esa primera detección.
- Si el bot está apagado, los cambios ocurridos mientras tanto no se notifican.
- Si cambias de juego y vuelves al mismo en menos de un intervalo, puede no
  detectarse.

No bajes el intervalo demasiado si vigilas muchas cuentas: Roblox puede responder
`429 Too Many Requests`. Con 30 s y hasta ~300 cuentas es razonable.

### Pruebas locales

```powershell
npm test                 # simulaciones (no usa red ni Discord)
npm run webhook-test     # envía un mensaje real de prueba al webhook
```

## ☁️ Despliegue 24/7 en SillyDev

SillyDev ofrece hosting gratuito de Node.js que **no se duerme** (plan gratuito
de 256 MB de RAM, suficiente para este bot). El proyecto tiene una única
dependencia (`sillydev`), así que el servidor debe instalar dependencias al
desplegar (`npm install`).

> La URL `https://sillydev.co.uk/panel/monitoring/...` que compartiste es un
> **proyecto de monitoreo de aplicaciones**, no el servidor donde se sube el
> código. Para alojar el bot crea un **servidor Node.js** en el panel.

### 1. Crear el servidor

1. Entra en `https://sillydev.co.uk` e inicia sesión.
2. En el panel, crea un **servidor nuevo** y elige **Node.js** (Node 20).
3. Selecciona el plan gratuito.

### 2. Subir el proyecto

Opción A — **File Manager**:

1. Comprime la carpeta del proyecto **sin** `.env` (puedes incluir
   `accounts.json`, `config.json` y, opcionalmente, `data/state.json`).
2. En el panel, abre el **File Manager** del servidor y sube el ZIP.
3. Extrae el contenido en la raíz del servidor. Debe quedar `bot.js` en la raíz,
   junto a `package.json`. Si el ZIP/RAR crea una carpeta `RobloxPresenceBot/`,
   mueve su contenido a `/home/container`: no debe quedar una subcarpeta.

Si incluyes `data/state.json`, el bot seguirá editando **el mismo panel** que ya
existe. Si lo omites, creará un panel nuevo al arrancar.

Opción B — **Git**:

1. Sube el proyecto a un repositorio privado.
2. En el panel usa **Git deploy** con la URL del repositorio.
3. No subas `.env`; `data/` está en `.gitignore`, así que el primer arranque
   creará un panel nuevo.

### 3. Configurar el arranque

En la configuración del servidor:

- **Main file / MAIN_FILE:** `bot.js`. SillyDev ejecuta
  `node /home/container/${MAIN_FILE}`; si lo dejas en `index.js` verás
  `Cannot find module '/home/container/index.js'`. Si el panel no permite
  cambiarlo, el proyecto incluye un `index.js` que importa `bot.js` y arranca
  igual.
- **Start command:** `npm start` (equivale a `node bot.js start`).
- **Install command:** `npm install` (si el panel no instala dependencias solo).
- **Node version:** 20 (o la que ofrezca el panel; el proyecto pide 18.17+).

### 4. Variables de entorno (recomendado)

En la pestaña **Environment** / **Variables** del servidor:

```env
ROBLOSECURITY=_|WARNING:-DO-NOT-SHARE-THIS...
TIME_ZONE=America/Argentina/Buenos_Aires
```

Si no quieres dejar la URL del webhook dentro de `config.json` (por ejemplo si el
repositorio es público), añade también:

```env
DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/...
```

La variable de entorno tiene prioridad sobre `config.json`.

### Monitorización de la aplicación (opcional)

`bot.js` llama a `monitoring.init()` del SDK `sillydev`, que lee la clave de
`SILLYDEV_MONITORING_KEY`. No hay ninguna clave escrita en el código.

- **En un servidor de SillyDev:** en **Settings → Monitoring** vincula el servidor
  al proyecto **Monitoreo** y reinicia. El panel inyecta la clave automáticamente.
- **Fuera de SillyDev:** crea una server key (empieza por `sdevm_`) en la pestaña
  **Settings** del proyecto y define `SILLYDEV_MONITORING_KEY` en el entorno o en
  `.env` (se carga antes de `init()`). Las variables reales del sistema tienen
  prioridad sobre `.env`.

Sin clave, `init()` no hace nada y el bot funciona igual.

### 5. Iniciar y comprobar

1. Pulsa **Start** en el panel.
2. En la consola deben aparecer:
   - `Webhook OK: ...`
   - `Cookie de Roblox válida: @...` (si configuraste la cookie)
   - `Panel de estado publicado (mensaje ...)`
   - `Monitor activo. Ctrl+C para detener.`
3. En el canal de Discord verás el panel y, cuando cambie algo, los avisos.

### 6. Mantener el panel entre reinicios

El bot guarda el ID del panel en `data/state.json`. Si el hosting conserva los
archivos (lo normal), tras un reinicio seguirá editando el mismo mensaje.

Si borras `data/` o migras de servidor, el bot creará un panel **nuevo**; el
mensaje anterior quedará obsoleto y puedes borrarlo a mano en Discord.

### Notas del plan gratuito

- 256 MB de RAM y almacenamiento limitado: más que suficiente para este bot.
- El bot usa ~50–80 MB y una petición cada 30 s.
- El panel de **Monitoring** de SillyDev está integrado en `bot.js`: los errores,
  la CPU y la memoria aparecen en **Overview** si el servidor está vinculado al
  proyecto o si defines `SILLYDEV_MONITORING_KEY`.
- Si el panel muestra el servidor detenido, usa **Start**; el plan gratuito no se
  apaga por inactividad, pero puede reiniciarse al desplegar cambios.

## Alternativas de ejecución 24/7

### Windows (Programador de tareas)

1. Abre **Programador de tareas** → **Crear tarea básica**.
2. Desencadenador: **Al iniciar el equipo**.
3. Acción: **Iniciar un programa**.
   - Programa: `C:\Program Files\nodejs\node.exe`
   - Argumentos: `"C:\Users\Usuario\Documents\RobloxPresenceBot\bot.js" start`
   - Iniciar en: `C:\Users\Usuario\Documents\RobloxPresenceBot`
4. Marca “Ejecutar tanto si el usuario inició sesión como si no”.

### Linux / VPS

```bash
cd /ruta/RobloxPresenceBot
node bot.js start
```

Con `systemd` o `pm2`:

```bash
pm2 start bot.js --name roblox-monitor -- start
pm2 save
```

## Registros y estado

- `data/state.json`: último estado, fechas, duraciones e ID del panel. No contiene
  la cookie.
- `data/events.jsonl`: historial de todos los cambios detectados (una línea JSON por
  evento, con fecha UTC). Útil si un aviso de Discord falla o quieres auditar.
  Puedes ver los últimos con `Get-Content data\events.jsonl -Tail 20`.
- `data/monitor.log`: registro rotativo (máx. 5 MB + copia `.1`).
- `.env`: cookie (nunca lo subas a git).

Para reiniciar de cero las detecciones, borra `data/state.json`. El siguiente
arranque no enviará avisos de estados actuales y creará un panel nuevo (borra el
anterior a mano).

## Problemas frecuentes

| Síntoma | Causa / solución |
|---|---|
| `No se encontró la cuenta` | Usa el **username** exacto, no el display name. |
| Todo aparece desconectado | La cuenta tiene la presencia privada (`Nadie`/`Amigos`). |
| Dice “Jugando (detalles no disponibles)” | Falta la cookie `.ROBLOSECURITY`. |
| `La cookie no es válida o caducó` | Vuelve a copiarla; cerrar sesión o cambiar de IP puede invalidarla. |
| `Discord respondió 401/404` | El webhook fue borrado. Crea otro y actualiza `config.json`. |
| `429 Too Many Requests` de Roblox | Sube `pollIntervalSeconds` o reduce cuentas. |
| El panel no aparece | Revisa que `statusPanel.enabled` sea `true` y que el webhook sea correcto. |
| Se crean paneles duplicados | Se borró `data/state.json` o cambió el servidor. Borra los mensajes viejos. |
| `node` no se reconoce | Instala Node.js 18+ y reinicia la terminal. |

## ⚠️ Seguridad importante

El webhook que se usó para crear este proyecto **fue publicado en el chat**.
Cualquier persona que lo tenga puede escribir en ese canal. Se recomienda
**eliminarlo y crear uno nuevo** en Discord:

1. Canal → **Editar canal** → **Integraciones** → **Webhooks**.
2. Elimina el webhook actual y crea uno nuevo.
3. Copia la nueva URL en `config.json` (`webhookUrl`) o en `DISCORD_WEBHOOK_URL`.

Nunca publiques tu cookie `.ROBLOSECURITY` ni la subas a un repositorio.
