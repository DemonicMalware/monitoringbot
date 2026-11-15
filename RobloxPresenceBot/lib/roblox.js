import { setTimeout as sleep } from "node:timers/promises";
import { request } from "./http.js";

const PRESENCE_URL = "https://presence.roblox.com/v1/presence/users";
const USERNAMES_URL = "https://users.roblox.com/v1/usernames/users";
const AUTHENTICATED_URL = "https://users.roblox.com/v1/users/authenticated";
const GAMES_URL = "https://games.roblox.com/v1/games";
const THUMBNAILS_GAMES_URL = "https://thumbnails.roblox.com/v1/games/icons";
const THUMBNAILS_USERS_URL = "https://thumbnails.roblox.com/v1/users/avatar-headshot";

const HOUR = 60 * 60 * 1000;

function chunkArray(array, size) {
  const chunks = [];
  for (let index = 0; index < array.length; index += size) {
    chunks.push(array.slice(index, index + size));
  }
  return chunks;
}

export class RobloxClient {
  constructor({ cookie = "", timeoutMs = 15000, logger = null } = {}) {
    this.cookie = String(cookie ?? "").replace(/^\.ROBLOSECURITY=/i, "").trim();
    this.timeoutMs = timeoutMs;
    this.logger = logger;
    this.csrfToken = null;
    this.cache = new Map();
    this.disableCookieNotified = false;
  }

  get hasCookie() {
    return Boolean(this.cookie);
  }

  cookieHeader() {
    return this.cookie ? { cookie: `.ROBLOSECURITY=${this.cookie}` } : {};
  }

  async #cached(key, ttlMs, loader) {
    const hit = this.cache.get(key);
    if (hit && hit.expiresAt > Date.now()) return hit.value;

    const value = await loader();
    const effectiveTtl = value === null || value === undefined ? Math.min(ttlMs, 5 * 60 * 1000) : ttlMs;
    this.cache.set(key, { value, expiresAt: Date.now() + effectiveTtl });
    return value;
  }

  /** Valida la cookie y devuelve la cuenta autenticada, o null si no sirve. */
  async validateCookie() {
    if (!this.cookie) return null;

    const response = await request(AUTHENTICATED_URL, {
      headers: this.cookieHeader(),
      timeoutMs: this.timeoutMs,
      retries: 1,
      logger: this.logger,
    });

    if (response.status === 401 || response.status === 403) return null;
    if (!response.ok) {
      throw new Error(`No se pudo validar la cookie (HTTP ${response.status}).`);
    }
    return response.data ?? null;
  }

  /** Convierte nombres de usuario en IDs. Devuelve un Map con clave en minúsculas. */
  async resolveUsernames(usernames) {
    const unique = [...new Set(usernames.map((name) => String(name).trim()).filter(Boolean))];
    const result = new Map();

    for (const batch of chunkArray(unique, 50)) {
      const response = await request(USERNAMES_URL, {
        method: "POST",
        body: { usernames: batch, excludeBannedUsers: false },
        timeoutMs: this.timeoutMs,
        retries: 2,
        logger: this.logger,
      });

      if (!response.ok) {
        throw new Error(`No se pudieron resolver los usuarios (HTTP ${response.status}): ${(response.text || "").slice(0, 200)}`);
      }

      for (const user of response.data?.data ?? []) {
        result.set(String(user.requestedUsername).toLowerCase(), {
          id: user.id,
          name: user.name,
          displayName: user.displayName ?? user.name,
          hasVerifiedBadge: Boolean(user.hasVerifiedBadge),
        });
      }
    }

    return result;
  }

  async getPresences(userIds) {
    const unique = [...new Set(userIds.map(Number).filter((id) => Number.isFinite(id) && id > 0))];
    const presences = [];

    for (const batch of chunkArray(unique, 50)) {
      presences.push(...(await this.#getPresenceBatch(batch)));
      // Pequeña pausa entre lotes para no abusar del endpoint.
      if (unique.length > 50) await sleep(300);
    }

    return presences;
  }

  async #getPresenceBatch(userIds) {
    if (userIds.length === 0) return [];

    for (let attempt = 0; attempt < 3; attempt += 1) {
      const headers = this.cookieHeader();
      if (this.csrfToken) headers["x-csrf-token"] = this.csrfToken;

      const response = await request(PRESENCE_URL, {
        method: "POST",
        headers,
        body: { userIds },
        timeoutMs: this.timeoutMs,
        retries: 2,
        logger: this.logger,
      });

      // El endpoint acepta un máximo de IDs; si nos pasamos, dividimos el lote.
      if (response.status === 400 && /too many user ids/i.test(response.text || "") && userIds.length > 1) {
        const middle = Math.ceil(userIds.length / 2);
        const first = await this.#getPresenceBatch(userIds.slice(0, middle));
        const second = await this.#getPresenceBatch(userIds.slice(middle));
        return [...first, ...second];
      }

      // Roblox pide resolver el token CSRF cuando se usa cookie en un POST.
      if (response.status === 403) {
        const token = response.headers.get("x-csrf-token");
        if (token && token !== this.csrfToken) {
          this.csrfToken = token;
          continue;
        }
      }

      // Cookie caducada: seguimos sin ella para no detener el monitor.
      if ((response.status === 401 || response.status === 403) && this.cookie) {
        if (!this.disableCookieNotified) {
          this.logger?.warn?.(
            "La cookie .ROBLOSECURITY no es válida o caducó. Se continuará sin cookie: no se verán nombres de juegos ni servidores.",
          );
          this.disableCookieNotified = true;
        }
        this.cookie = "";
        this.csrfToken = null;
        continue;
      }

      if (!response.ok) {
        throw new Error(`Presence API respondió HTTP ${response.status}: ${(response.text || "").slice(0, 300)}`);
      }

      return response.data?.userPresences ?? [];
    }

    throw new Error("No se pudo obtener la presencia después de varios intentos.");
  }

  /** Resuelve el universo de un place. */
  async #getUniverseId(placeId) {
    return this.#cached(`place:${placeId}`, 24 * HOUR, async () => {
      const response = await request(
        `https://apis.roblox.com/universes/v1/places/${encodeURIComponent(placeId)}/universe`,
        { timeoutMs: this.timeoutMs, retries: 1, logger: this.logger },
      );
      if (!response.ok) return null;
      return response.data?.universeId ?? null;
    });
  }

  /** Datos del juego + icono. */
  async getGameByUniverse(universeId, fallbackPlaceId = null) {
    if (!universeId) return null;

    return this.#cached(`game:${universeId}`, 6 * HOUR, async () => {
      const [detailsResult, iconResult] = await Promise.allSettled([
        request(`${GAMES_URL}?universeIds=${encodeURIComponent(universeId)}`, {
          timeoutMs: this.timeoutMs,
          retries: 1,
          logger: this.logger,
        }),
        request(`${THUMBNAILS_GAMES_URL}?universeIds=${encodeURIComponent(universeId)}&size=512x512&format=Png`, {
          timeoutMs: this.timeoutMs,
          retries: 1,
          logger: this.logger,
        }),
      ]);

      const details =
        detailsResult.status === "fulfilled" && detailsResult.value.ok
          ? detailsResult.value.data?.data?.[0] ?? null
          : null;
      const iconUrl =
        iconResult.status === "fulfilled" && iconResult.value.ok
          ? iconResult.value.data?.data?.[0]?.imageUrl ?? null
          : null;

      if (!details && !iconUrl) return null;

      const rootPlaceId = details?.rootPlaceId ?? fallbackPlaceId ?? null;
      return {
        universeId: Number(universeId),
        name: details?.name ?? null,
        rootPlaceId,
        iconUrl,
        url: rootPlaceId ? `https://www.roblox.com/games/${rootPlaceId}` : null,
      };
    });
  }

  async getPlaceDetails(placeId) {
    if (!placeId) return null;
    const universeId = await this.#getUniverseId(placeId);
    if (!universeId) {
      return {
        universeId: null,
        name: null,
        rootPlaceId: placeId,
        iconUrl: null,
        url: `https://www.roblox.com/games/${placeId}`,
      };
    }
    return this.getGameByUniverse(universeId, placeId);
  }

  async getAvatarUrl(userId) {
    if (!userId) return null;

    return this.#cached(`avatar:${userId}`, 6 * HOUR, async () => {
      const response = await request(
        `${THUMBNAILS_USERS_URL}?userIds=${encodeURIComponent(userId)}&size=420x420&format=Png`,
        { timeoutMs: this.timeoutMs, retries: 1, logger: this.logger },
      );
      if (!response.ok) return null;
      return response.data?.data?.[0]?.imageUrl ?? null;
    });
  }
}
