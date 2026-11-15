#!/usr/bin/env node

import { monitoring } from "sillydev/monitoring";

import path from "node:path";
import { fileURLToPath } from "node:url";

import { loadConfig, loadEnvFile, validateWebhookUrl } from "./lib/config.js";
import { createLogger } from "./lib/logger.js";
import { RobloxClient } from "./lib/roblox.js";
import { DiscordWebhook } from "./lib/discord.js";
import { Monitor } from "./lib/monitor.js";
import { loadAccounts, loadState, saveAccounts, cleanUsername } from "./lib/store.js";
import {
  localString,
  presenceLabel,
  utcString,
} from "./lib/format.js";
import { normalizePresence, resolveGameDetails } from "./lib/presence.js";
import { buildTestPayload } from "./lib/events.js";

const ROOT = path.dirname(fileURLToPath(import.meta.url));

// El SDK lee SILLYDEV_MONITORING_KEY al llamar a init(); carga .env antes.
loadEnvFile(path.join(ROOT, ".env"));
monitoring.init({ service: "roblox-presence-bot" });

const ACCOUNTS_FILE = path.join(ROOT, "accounts.json");
const STATE_FILE = path.join(ROOT, "data", "state.json");

function buildContext({ dryRun = false } = {}) {
  const config = loadConfig(ROOT);
  if (dryRun) config.dryRun = true;

  const logger = createLogger({
    level: config.logLevel,
    timeZone: config.timeZone,
    logFile: path.join(ROOT, "data", "monitor.log"),
  });

  const roblox = new RobloxClient({
    cookie: config.robloxCookie,
    timeoutMs: config.requestTimeoutMs,
    logger,
  });

  const discord = new DiscordWebhook({
    url: config.webhookUrl,
    username: config.webhookUsername,
    avatarUrl: config.webhookAvatarUrl,
    minIntervalMs: config.webhookMinIntervalMs,
    timeoutMs: config.requestTimeoutMs,
    logger,
    dryRun: config.dryRun,
  });

  return { config, logger, roblox, discord };
}

function requireWebhook(config) {
  if (!config.webhookUrl) {
    throw new Error(
      "Falta el webhook. Configura webhookUrl en config.json o DISCORD_WEBHOOK_URL en .env.",
    );
  }
  validateWebhookUrl(config.webhookUrl);
}

async function validateRobloxCookie(roblox, logger) {
  if (!roblox.hasCookie) {
    logger.warn(
      "Sin cookie de Roblox: se detectará conexión/desconexión y 'en juego', pero no el nombre del juego. Configura ROBLOSECURITY en .env.",
    );
    return null;
  }

  let user = null;
  try {
    user = await roblox.validateCookie();
  } catch (error) {
    logger.warn(`No se pudo validar la cookie de Roblox: ${error.message}`);
    return null;
  }

  if (!user) {
    logger.warn(
      "La cookie de Roblox no es válida o caducó. Se continuará sin cookie (sin nombres de juegos).",
    );
    roblox.cookie = "";
    return null;
  }

  logger.info(`Cookie de Roblox válida: @${user.name} (${user.id}).`);
  return user;
}

function printHelp() {
  console.log(`
Roblox Presence Monitor
=======================

Uso:
  node bot.js start [--dry-run] [--interval 30]
      Inicia el monitor y envía los cambios al webhook.

  node bot.js add <usuario> [usuario2 ...]
      Añade cuentas por nombre de usuario (sin @).

  node bot.js remove <usuario> [usuario2 ...]
      Deja de vigilar esas cuentas.

  node bot.js list
      Muestra las cuentas configuradas y su último estado guardado.

  node bot.js check [--json]
      Consulta el estado actual sin enviar nada a Discord.

  node bot.js status
      Publica o actualiza el panel de estado en Discord (sin dejar el monitor activo).

  node bot.js test
      Envía un mensaje de prueba al webhook.

  node bot.js help
      Muestra esta ayuda.

Archivos:
  accounts.json   Cuentas vigiladas
  config.json     Webhook, intervalo y opciones
  .env            Cookie de Roblox (opcional, recomendado para ver juegos)
  data/state.json Estado interno y duraciones

Notas:
  - Para ver el NOMBRE del juego hace falta una cookie .ROBLOSECURITY.
  - La fecha se muestra con la hora local de cada persona en Discord y
    también en UTC. La detección tiene un margen igual al intervalo.
  - El panel de estado es un mensaje fijo que se edita en cada consulta.
    Desactívalo con "statusPanel": { "enabled": false } en config.json.
  - El webhook que venía en el mensaje original ya está configurado.
`);
}

async function commandStart(args) {
  const { config, logger, roblox, discord } = buildContext({
    dryRun: args.includes("--dry-run"),
  });

  const intervalIndex = args.indexOf("--interval");
  if (intervalIndex !== -1) {
    const value = Number(args[intervalIndex + 1]);
    if (Number.isFinite(value)) {
      config.pollIntervalSeconds = Math.min(3600, Math.max(10, value));
    }
  }

  requireWebhook(config);

  logger.info("==========================================");
  logger.info("  ROBLOX PRESENCE MONITOR");
  logger.info("==========================================");
  logger.info(`Intervalo de consulta: ${config.pollIntervalSeconds} s`);
  if (config.dryRun) {
    logger.warn("Modo DRY-RUN activo: los mensajes se muestran en consola, no se envían.");
  }

  const webhookInfo = await discord.validate();
  logger.info(
    `Webhook OK: ${webhookInfo.name ?? "sin nombre"} (canal ${webhookInfo.channel_id ?? "desconocido"}).`,
  );

  await validateRobloxCookie(roblox, logger);

  const monitor = new Monitor({
    rootDir: ROOT,
    config,
    logger,
    roblox,
    discord,
  });

  let stopping = false;
  const shutdown = async (signal) => {
    if (stopping) return;
    stopping = true;
    logger.info(`${signal} recibido. Guardando estado y saliendo...`);
    await monitor.stop();
    await monitoring.shutdown();
    logger.close();
    process.exit(0);
  };

  process.on("SIGINT", () => void shutdown("SIGINT"));
  process.on("SIGTERM", () => void shutdown("SIGTERM"));
  process.on("unhandledRejection", (reason) => {
    logger.error("Promesa rechazada sin manejar:", reason);
  });

  await monitor.start();
  logger.info(`Monitor activo. Ctrl+C para detener.`);
}

async function commandStatus(args = []) {
  const { config, logger, roblox, discord } = buildContext({
    dryRun: args.includes("--dry-run"),
  });
  requireWebhook(config);

  if (config.statusPanel?.enabled === false) {
    console.log('El panel de estado está desactivado ("statusPanel.enabled": false en config.json).');
    return;
  }

  const webhookInfo = await discord.validate();
  logger.info(
    `Webhook OK: ${webhookInfo.name ?? "sin nombre"} (canal ${webhookInfo.channel_id ?? "desconocido"}).`,
  );
  await validateRobloxCookie(roblox, logger);

  const monitor = new Monitor({ rootDir: ROOT, config, logger, roblox, discord });
  await monitor.reloadAccounts();

  if (monitor.accounts.length === 0) {
    console.log("No hay cuentas configuradas. Usa: node bot.js add <usuario>");
    return;
  }

  try {
    await monitor.poll({ initial: true });
    const panel = monitor.state.panel ?? {};
    if (config.dryRun) {
      console.log("Panel generado en modo dry-run (no enviado).");
    } else if (panel.messageId) {
      console.log(`Panel de estado publicado o actualizado. Mensaje: ${panel.messageId}`);
    } else {
      console.log("No se pudo publicar el panel. Revisa data/monitor.log.");
    }
  } finally {
    await monitor.stop();
  }
}

async function commandAdd(args) {
  const names = args.map(cleanUsername).filter(Boolean);
  if (names.length === 0) {
    console.error("Indica al menos un nombre de usuario. Ejemplo: node bot.js add Roblox builderman");
    process.exitCode = 1;
    return;
  }

  const { logger, roblox } = buildContext();
  const accounts = loadAccounts(ACCOUNTS_FILE);
  const byName = new Map(accounts.map((account) => [account.username.toLowerCase(), account]));

  const toResolve = names.filter((name) => {
    const existing = byName.get(name.toLowerCase());
    return !existing || !existing.userId;
  });

  let resolved = new Map();
  if (toResolve.length > 0) {
    resolved = await roblox.resolveUsernames(toResolve);
  }

  let added = 0;
  let updated = 0;

  for (const name of names) {
    const key = name.toLowerCase();
    const existing = byName.get(key);
    const found = resolved.get(key);

    if (existing?.userId && !found) {
      logger.info(`@${existing.username} ya estaba en la lista (ID ${existing.userId}).`);
      continue;
    }

    if (!found) {
      logger.warn(`No se encontró la cuenta "${name}" en Roblox. No se añadió.`);
      continue;
    }

    if (existing) {
      existing.userId = found.id;
      existing.username = found.name;
      existing.displayName = found.displayName;
      updated += 1;
      logger.info(`Actualizado: @${found.name} (ID ${found.id}).`);
    } else {
      const account = {
        username: found.name,
        displayName: found.displayName,
        userId: found.id,
        addedAt: new Date().toISOString(),
      };
      accounts.push(account);
      byName.set(found.name.toLowerCase(), account);
      added += 1;
      logger.info(`Añadido: @${found.name} (ID ${found.id}).`);
    }
  }

  if (added > 0 || updated > 0) {
    saveAccounts(ACCOUNTS_FILE, accounts);
    logger.info(`accounts.json actualizado. Total de cuentas: ${accounts.length}.`);
  }
}

async function commandRemove(args) {
  const names = new Set(args.map(cleanUsername).filter(Boolean).map((name) => name.toLowerCase()));
  if (names.size === 0) {
    console.error("Indica al menos un nombre de usuario. Ejemplo: node bot.js remove Roblox");
    process.exitCode = 1;
    return;
  }

  const { logger } = buildContext();
  const accounts = loadAccounts(ACCOUNTS_FILE);
  const remaining = accounts.filter(
    (account) => !names.has(account.username.toLowerCase()),
  );
  const removed = accounts.length - remaining.length;

  if (removed === 0) {
    logger.warn("Ninguna de esas cuentas estaba en la lista.");
    return;
  }

  saveAccounts(ACCOUNTS_FILE, remaining);
  logger.info(`Cuenta(s) eliminada(s): ${removed}. Quedan ${remaining.length}.`);
}

function commandList() {
  const config = loadConfig(ROOT);
  const accounts = loadAccounts(ACCOUNTS_FILE);
  const state = loadState(STATE_FILE, null);

  if (accounts.length === 0) {
    console.log("No hay cuentas configuradas. Usa: node bot.js add <usuario>");
    return;
  }

  const rows = accounts.map((account) => {
    const saved = state.users?.[account.userId] ?? null;
    return {
      Usuario: `@${account.username}`,
      ID: account.userId ?? "sin resolver",
      Estado: saved ? presenceLabel(saved) : "Sin datos (ejecuta start/check)",
      "Desde": saved?.since ? localString(saved.since, config.timeZone) : "—",
      "Última señal": saved?.lastSeenAt ? localString(saved.lastSeenAt, config.timeZone) : "—",
    };
  });

  console.table(rows);
  console.log(`Total: ${accounts.length} cuenta(s). Intervalo: ${config.pollIntervalSeconds} s.`);
}

async function commandCheck(args) {
  const { config, logger, roblox } = buildContext();
  const asJson = args.includes("--json");

  let accounts = loadAccounts(ACCOUNTS_FILE);
  if (accounts.length === 0) {
    console.log("No hay cuentas configuradas. Usa: node bot.js add <usuario>");
    return;
  }

  const unresolved = accounts.filter((account) => !account.userId);
  if (unresolved.length > 0) {
    try {
      const resolved = await roblox.resolveUsernames(unresolved.map((account) => account.username));
      accounts = accounts.map((account) => {
        if (account.userId) return account;
        const found = resolved.get(account.username.toLowerCase());
        return found
          ? { ...account, userId: found.id, username: found.name, displayName: found.displayName }
          : account;
      });
    } catch (error) {
      logger.error(`No se pudieron resolver las cuentas: ${error.message}`);
    }
  }

  const valid = accounts.filter((account) => account.userId);
  if (valid.length === 0) {
    console.log("No hay cuentas válidas para consultar.");
    return;
  }

  const cookieUser = await validateRobloxCookie(roblox, logger);
  const presences = await roblox.getPresences(valid.map((account) => account.userId));
  const byId = new Map(presences.map((presence) => [Number(presence.userId), presence]));

  const results = [];
  for (const account of valid) {
    const raw = byId.get(Number(account.userId));
    if (!raw) {
      results.push({
        account,
        presence: null,
        error: "Roblox no devolvió presencia para esta cuenta.",
      });
      continue;
    }

    const presence = normalizePresence(raw);
    if (presence.presenceType === 2) {
      presence.game = await resolveGameDetails(roblox, presence, logger);
    }
    results.push({ account, presence, error: null });
  }

  if (asJson) {
    console.log(
      JSON.stringify(
        {
          checkedAt: new Date().toISOString(),
          cookieUser: cookieUser ? { id: cookieUser.id, name: cookieUser.name } : null,
          results: results.map(({ account, presence, error }) => ({
            username: account.username,
            userId: account.userId,
            state: presence ? presenceLabel(presence) : null,
            presenceType: presence?.presenceType ?? null,
            game: presence?.game ?? null,
            lastOnline: presence?.lastOnline?.toISOString?.() ?? null,
            error,
          })),
        },
        null,
        2,
      ),
    );
    return;
  }

  for (const { account, presence, error } of results) {
    console.log("");
    console.log(`@${account.username} (ID ${account.userId})`);
    if (error) {
      console.log(`  Error: ${error}`);
      continue;
    }

    console.log(`  Estado: ${presenceLabel(presence).replace(/\*\*/g, "")}`);
    console.log(`  Consultado: ${localString(new Date(), config.timeZone)} (${utcString(new Date())})`);

    if (presence.lastOnline) {
      console.log(`  Última conexión conocida: ${localString(presence.lastOnline, config.timeZone)} (${utcString(presence.lastOnline)})`);
    }
    if (presence.game) {
      console.log(`  Juego: ${presence.game.name ?? "no disponible"}`);
      if (presence.game.rootPlaceId ?? presence.game.placeId) {
        console.log(`  Lugar: ${presence.game.rootPlaceId ?? presence.game.placeId}`);
      }
      if (presence.game.gameId) console.log(`  Servidor: ${presence.game.gameId}`);
      if (presence.game.url) console.log(`  Enlace: ${presence.game.url}`);
    }
  }

  if (!cookieUser) {
    console.log(
      "\nNota: sin cookie de Roblox no se puede ver el nombre del juego. Configura ROBLOSECURITY en .env.",
    );
  }
}

async function commandTest() {
  const { config, logger, roblox, discord } = buildContext();
  requireWebhook(config);

  const webhookInfo = await discord.validate();
  const cookieUser = await validateRobloxCookie(roblox, logger);
  const accounts = loadAccounts(ACCOUNTS_FILE);

  await discord.send(
    buildTestPayload({
      config,
      channelInfo: webhookInfo,
      accountCount: accounts.length,
      cookieUser,
    }),
  );

  logger.info(
    config.dryRun
      ? "Mensaje de prueba generado en modo dry-run (no enviado)."
      : `Mensaje de prueba enviado al canal ${webhookInfo.channel_id ?? "desconocido"}.`,
  );
}

async function main() {
  const args = process.argv.slice(2);
  const command = (args.shift() ?? "start").toLowerCase();

  switch (command) {
    case "start":
      await commandStart(args);
      break;
    case "add":
      await commandAdd(args);
      break;
    case "remove":
      await commandRemove(args);
      break;
    case "list":
      commandList();
      break;
    case "check":
      await commandCheck(args);
      break;
    case "status":
      await commandStatus(args);
      break;
    case "test":
      await commandTest();
      break;
    case "help":
    case "--help":
    case "-h":
      printHelp();
      break;
    default:
      console.error(`Comando desconocido: ${command}`);
      printHelp();
      process.exitCode = 1;
  }
}

main().catch((error) => {
  console.error(`\nError: ${error.message}`);
  if (process.env.DEBUG) console.error(error);
  process.exitCode = 1;
});
