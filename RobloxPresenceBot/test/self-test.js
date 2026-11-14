import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import {
  detectEvent,
  normalizePresence,
  resolveGameDetails,
  updateState,
} from "../lib/presence.js";
import { buildEventPayload, buildStartupPayload, buildStatusPayload } from "../lib/events.js";
import { formatDuration } from "../lib/format.js";
import { Monitor } from "../lib/monitor.js";
import { writeJson } from "../lib/store.js";

const config = { pollIntervalSeconds: 30, mention: "", notify: {} };
const account = { username: "Tester_123", displayName: "Tester", userId: 999 };

const fakeRoblox = {
  async getGameByUniverse(universeId, placeId) {
    if (universeId !== 111) return null;
    return {
      universeId,
      name: "Juego Uno",
      rootPlaceId: placeId,
      iconUrl: "https://example.com/icon.png",
      url: `https://www.roblox.com/games/${placeId}`,
    };
  },
  async getPlaceDetails(placeId) {
    return this.getGameByUniverse(111, placeId);
  },
};

function check(label, condition) {
  assert.ok(condition, label);
  console.log(`OK  ${label}`);
}

// --- Detección de transiciones y embeds -------------------------------------
const t0 = new Date("2026-11-14T18:00:00.000Z").getTime();
let state = updateState(
  account,
  null,
  normalizePresence({ userPresenceType: 0, userId: account.userId }),
  new Date(t0),
);

function step(seconds, raw, game) {
  const now = new Date(t0 + seconds * 1000);
  const next = normalizePresence(raw);
  if (next.presenceType === 2) next.game = game ?? null;

  const event = detectEvent(state, next, now);
  const payload = event
    ? buildEventPayload({ ...event, account, prev: state, next, avatarUrl: null, config })
    : null;

  state = updateState(account, state, next, now);
  return { event, payload };
}

let result = step(30, { userPresenceType: 1, userId: account.userId, lastLocation: "Website" });
check("detecta conexión", result.event?.type === "connected");

const gameOne = normalizePresence({
  userPresenceType: 2,
  userId: account.userId,
  placeId: 1001,
  rootPlaceId: 1001,
  universeId: 111,
  gameId: "server-aaa",
  lastLocation: "Juego Uno",
});
gameOne.game = await resolveGameDetails(fakeRoblox, gameOne);

result = step(
  90,
  {
    userPresenceType: 2,
    userId: account.userId,
    placeId: 1001,
    rootPlaceId: 1001,
    universeId: 111,
    gameId: "server-aaa",
    lastLocation: "Juego Uno",
  },
  gameOne.game,
);
check("detecta inicio de juego", result.event?.type === "game_started");
check("incluye botones del juego", result.payload?.components?.length > 0);

const gameTwo = normalizePresence({
  userPresenceType: 2,
  userId: account.userId,
  placeId: 2002,
  rootPlaceId: 2002,
  universeId: 222,
  lastLocation: "Juego Dos",
});
gameTwo.game = await resolveGameDetails(fakeRoblox, gameTwo);

result = step(
  150,
  {
    userPresenceType: 2,
    userId: account.userId,
    placeId: 2002,
    rootPlaceId: 2002,
    universeId: 222,
    lastLocation: "Juego Dos",
  },
  gameTwo.game,
);
check("detecta cambio de juego", result.event?.type === "game_changed");

result = step(210, { userPresenceType: 1, userId: account.userId, lastLocation: "Website" });
check("detecta salida del juego", result.event?.type === "left_game");
check(
  "calcula tiempo en el juego",
  result.payload?.embeds[0].fields.some((field) => field.value.includes("1 min")),
);

result = step(270, { userPresenceType: 0, userId: account.userId });
check("detecta desconexión", result.event?.type === "disconnected");
check(
  "calcula tiempo en línea",
  result.payload?.embeds[0].fields.some((field) => field.value.includes("4 min")),
);

const startup = buildStartupPayload({ entries: [{ account, state }], config });
check("construye resumen inicial", startup.embeds.length === 1);

const panel = buildStatusPayload({
  entries: [{ account, state }],
  config,
  now: new Date(t0 + 270 * 1000),
});
check("construye el panel de estado", panel.embeds.length === 1);
check("el panel muestra al usuario", panel.embeds[0].fields[0].name.includes("@Tester_123"));
check("el panel muestra la última conexión", panel.embeds[0].fields[0].value.includes("Última conexión"));
check("el panel usa horas de Discord", panel.embeds[0].fields[0].value.includes("<t:"));

check("formatea duraciones", formatDuration(3661000) === "1 h 1 min");
check("sin cookie no inventa juego", (await resolveGameDetails(fakeRoblox, normalizePresence({ userPresenceType: 2, userId: 1 }))) === null);

// --- Monitor con APIs simuladas ---------------------------------------------
const rootDir = fs.mkdtempSync(path.join(os.tmpdir(), "roblox-monitor-test-"));
writeJson(path.join(rootDir, "accounts.json"), [
  { username: "Cuenta_Prueba", userId: 12345, displayName: "Cuenta Prueba" },
]);

const queue = [];
const logger = { debug() {}, info() {}, warn() {}, error() {} };
const roblox = {
  async resolveUsernames(names) {
    const map = new Map();
    for (const name of names) {
      map.set(name.toLowerCase(), { id: 12345, name: "Cuenta_Prueba", displayName: "Cuenta Prueba" });
    }
    return map;
  },
  async getPresences() {
    return [queue.shift()];
  },
  async getGameByUniverse(universeId, placeId) {
    return {
      universeId,
      name: "Juego Simulado",
      rootPlaceId: placeId,
      iconUrl: null,
      url: `https://www.roblox.com/games/${placeId}`,
    };
  },
  async getPlaceDetails(placeId) {
    return this.getGameByUniverse(777, placeId);
  },
  async getAvatarUrl() {
    return null;
  },
};

const sent = [];
const panelSends = [];
const panelEdits = [];
let nextMessageId = 1;
let failNextEdit = false;

const discord = {
  async send(payload, options = {}) {
    if (options.wait) {
      const id = String(nextMessageId++);
      panelSends.push({ id, payload });
      return { id, channel_id: "test-channel" };
    }
    sent.push(payload);
    return { id: String(nextMessageId++) };
  },
  async edit(messageId, payload) {
    if (failNextEdit) {
      failNextEdit = false;
      const error = new Error("Unknown Message");
      error.status = 404;
      throw error;
    }
    panelEdits.push({ messageId, payload });
    return { id: messageId };
  },
  async flush() {},
};

const monitor = new Monitor({
  rootDir,
  config: {
    ...config,
    statusPanel: { enabled: true },
    notify: { connect: true, disconnect: true, gameStart: true, gameChange: true, leaveGame: true, studio: true, statusChange: true, startupSummary: true },
  },
  logger,
  roblox,
  discord,
});

await monitor.reloadAccounts();
check("carga cuentas", monitor.accounts.length === 1);

queue.push({ userId: 12345, userPresenceType: 0 });
await monitor.poll({ initial: true });
check("el estado inicial no notifica", sent.length === 0);
check("el estado inicial crea el panel", panelSends.length === 1);
check("el panel guarda el ID del mensaje", monitor.state.panel.messageId === panelSends[0].id);
check(
  "el panel muestra la cuenta",
  panelSends[0].payload.embeds[0].fields[0].name.includes("@Cuenta_Prueba"),
);

queue.push({ userId: 12345, userPresenceType: 1, lastLocation: "Website" });
await monitor.poll();
check("el monitor notifica conexión", sent.length === 1);
check(
  "el panel se edita con el nuevo estado",
  panelEdits.length === 1 && panelEdits[0].messageId === panelSends[0].id,
);
check(
  "el panel muestra la hora de conexión",
  panelEdits[0].payload.embeds[0].fields[0].value.includes("En línea desde"),
);

queue.push({
  userId: 12345,
  userPresenceType: 2,
  placeId: 555,
  rootPlaceId: 555,
  universeId: 777,
  gameId: "job-123",
  lastLocation: "Juego Simulado",
});
await monitor.poll();
check("el monitor notifica inicio de juego", sent.length === 2);
check("el panel muestra la hora de inicio del juego", panelEdits.at(-1).payload.embeds[0].fields[0].value.includes("Jugando desde"));

queue.push({ userId: 12345, userPresenceType: 0 });
await monitor.poll();
check("el monitor notifica desconexión", sent.length === 3);

failNextEdit = true;
queue.push({ userId: 12345, userPresenceType: 0 });
await monitor.poll();
check("recrea el panel si el mensaje fue borrado", panelSends.length === 2);
check("guarda el nuevo ID del panel", monitor.state.panel.messageId === panelSends[1].id);

const saved = JSON.parse(fs.readFileSync(path.join(rootDir, "data", "state.json"), "utf8"));
check("guarda el estado en disco", saved.users["12345"].presenceType === 0);
check("guarda la duración de la sesión", saved.users["12345"].lastSessionMs > 0);
check("persiste el panel de estado", saved.panel?.messageId === panelSends[1].id);

await monitor.stop();
fs.rmSync(rootDir, { recursive: true, force: true });

console.log("\nTodas las pruebas locales pasaron.");
