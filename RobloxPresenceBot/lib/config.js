import fs from "node:fs";
import path from "node:path";
import { readJson } from "./store.js";

const DEFAULTS = {
  webhookUrl: "",
  webhookUsername: "",
  webhookAvatarUrl: "",
  pollIntervalSeconds: 30,
  requestTimeoutMs: 15000,
  webhookMinIntervalMs: 600,
  mention: "",
  robloxCookie: "",
  timeZone: "",
  logLevel: "info",
  dryRun: false,
  statusPanel: {
    enabled: true,
  },
  notify: {
    connect: true,
    disconnect: true,
    gameStart: true,
    gameChange: true,
    leaveGame: true,
    studio: true,
    statusChange: true,
    startupSummary: false,
  },
};

function isPlainObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function deepMerge(...objects) {
  const result = {};
  for (const object of objects) {
    if (!isPlainObject(object)) continue;
    for (const [key, value] of Object.entries(object)) {
      if (isPlainObject(value) && isPlainObject(result[key])) {
        result[key] = deepMerge(result[key], value);
      } else if (isPlainObject(value)) {
        result[key] = deepMerge({}, value);
      } else {
        result[key] = value;
      }
    }
  }
  return result;
}

export function loadEnvFile(file) {
  if (!fs.existsSync(file)) return;
  const content = fs.readFileSync(file, "utf8");
  for (const line of content.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const separator = trimmed.indexOf("=");
    if (separator === -1) continue;

    const key = trimmed.slice(0, separator).trim();
    let value = trimmed.slice(separator + 1).trim();
    if (!key) continue;
    if (
      (value.startsWith('"') && value.endsWith('"')) ||
      (value.startsWith("'") && value.endsWith("'"))
    ) {
      value = value.slice(1, -1);
    }
    // Las variables reales del sistema tienen prioridad sobre .env
    if (process.env[key] === undefined) process.env[key] = value;
  }
}

function clamp(value, min, max, fallback) {
  const number = Number(value);
  if (!Number.isFinite(number)) return fallback;
  return Math.min(max, Math.max(min, number));
}

function normalizeCookie(value) {
  let cookie = String(value ?? "").trim();
  if (!cookie) return "";
  cookie = cookie.replace(/^\.ROBLOSECURITY=/i, "");
  if (
    (cookie.startsWith('"') && cookie.endsWith('"')) ||
    (cookie.startsWith("'") && cookie.endsWith("'"))
  ) {
    cookie = cookie.slice(1, -1);
  }
  return cookie.trim();
}

export function loadConfig(rootDir) {
  loadEnvFile(path.join(rootDir, ".env"));

  const fileConfig = readJson(path.join(rootDir, "config.json"), {});
  const localConfig = readJson(path.join(rootDir, "config.local.json"), {});
  const config = deepMerge(DEFAULTS, fileConfig, localConfig);

  if (process.env.DISCORD_WEBHOOK_URL) config.webhookUrl = process.env.DISCORD_WEBHOOK_URL;
  if (process.env.ROBLOSECURITY) config.robloxCookie = process.env.ROBLOSECURITY;
  if (process.env.POLL_INTERVAL_SECONDS) config.pollIntervalSeconds = process.env.POLL_INTERVAL_SECONDS;
  if (process.env.TIME_ZONE) config.timeZone = process.env.TIME_ZONE;
  if (process.env.LOG_LEVEL) config.logLevel = process.env.LOG_LEVEL;
  if (process.env.DRY_RUN === "1") config.dryRun = true;

  config.webhookUrl = String(config.webhookUrl ?? "").trim();
  config.webhookUsername = String(config.webhookUsername ?? "").trim();
  config.webhookAvatarUrl = String(config.webhookAvatarUrl ?? "").trim();
  config.mention = String(config.mention ?? "").trim();
  config.robloxCookie = normalizeCookie(config.robloxCookie);
  config.timeZone = String(config.timeZone ?? "").trim();
  config.pollIntervalSeconds = clamp(config.pollIntervalSeconds, 10, 3600, 30);
  config.requestTimeoutMs = clamp(config.requestTimeoutMs, 3000, 60000, 15000);
  config.webhookMinIntervalMs = clamp(config.webhookMinIntervalMs, 250, 5000, 600);

  if (!["debug", "info", "warn", "error"].includes(config.logLevel)) {
    config.logLevel = "info";
  }

  config.notify = {
    ...DEFAULTS.notify,
    ...(isPlainObject(config.notify) ? config.notify : {}),
  };

  config.statusPanel = {
    ...DEFAULTS.statusPanel,
    ...(isPlainObject(config.statusPanel) ? config.statusPanel : {}),
  };
  config.statusPanel.enabled = config.statusPanel.enabled !== false;

  return config;
}

export function validateWebhookUrl(url) {
  let parsed;
  try {
    parsed = new URL(url);
  } catch {
    throw new Error("El webhookUrl no es una URL válida.");
  }
  if (!["http:", "https:"].includes(parsed.protocol)) {
    throw new Error("El webhookUrl debe usar http o https.");
  }
  if (!["discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com"].includes(parsed.hostname)) {
    throw new Error("El webhookUrl no parece ser un webhook de Discord.");
  }
  return url;
}
