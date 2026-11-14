function numberOrNull(value) {
  const number = Number(value);
  return Number.isFinite(number) && number > 0 ? number : null;
}

function dateOrNull(value) {
  if (!value) return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

/** Convierte la respuesta cruda de Roblox en un objeto estable. */
export function normalizePresence(raw) {
  const type = Number(raw?.userPresenceType ?? 0);
  const presenceType = [0, 1, 2, 3].includes(type) ? type : 0;

  return {
    presenceType,
    lastLocation: typeof raw?.lastLocation === "string" ? raw.lastLocation.trim() : "",
    placeId: numberOrNull(raw?.placeId),
    rootPlaceId: numberOrNull(raw?.rootPlaceId),
    universeId: numberOrNull(raw?.universeId),
    gameId: raw?.gameId ? String(raw.gameId) : null,
    lastOnline: dateOrNull(raw?.lastOnline),
    inStudio: raw?.inStudio === true || presenceType === 3,
    isOnline: typeof raw?.isOnline === "boolean" ? raw.isOnline : presenceType !== 0,
    game: null,
  };
}

/** Clave para saber si dos presencias corresponden al mismo juego. */
export function gameKey(presence) {
  if (!presence) return null;
  const rootPlace = presence.rootPlaceId ?? presence.game?.rootPlaceId ?? null;
  if (rootPlace) return `place:${rootPlace}`;

  const place = presence.placeId ?? null;
  if (place) return `place:${place}`;

  const universe = presence.universeId ?? presence.game?.universeId ?? null;
  if (universe) return `universe:${universe}`;

  const name = presence.game?.name ?? presence.lastLocation ?? null;
  return name ? `name:${String(name).toLowerCase()}` : null;
}

function timestampMs(value) {
  if (!value) return null;
  const ms = new Date(value).getTime();
  return Number.isNaN(ms) ? null : ms;
}

/**
 * Decide qué evento ocurrió entre dos estados. Devuelve null si no hay nada
 * que notificar (por ejemplo, sigue en el mismo juego).
 */
export function detectEvent(previous, next, now = new Date()) {
  if (!previous) return null;

  const previousType = Number(previous.presenceType ?? 0);
  const nextType = Number(next.presenceType ?? 0);
  const previousGameKey = gameKey(previous);
  const nextGameKey = gameKey(next);

  const sessionStart = timestampMs(previous.sessionStart) ?? timestampMs(previous.since);
  const activityStart = timestampMs(previous.since);

  const event = {
    at: now,
    prevGame: previous.game ?? null,
    nextGame: next.game ?? null,
    sessionMs: sessionStart ? Math.max(0, now.getTime() - sessionStart) : null,
    activityMs: activityStart ? Math.max(0, now.getTime() - activityStart) : null,
    lastOnline: next.lastOnline ?? previous.lastOnline ?? null,
  };

  if (previousType === 0 && nextType !== 0) {
    return { ...event, type: "connected" };
  }

  if (previousType !== 0 && nextType === 0) {
    return { ...event, type: "disconnected" };
  }

  if (previousType === nextType) {
    if (nextType === 2 && previousGameKey && nextGameKey && previousGameKey !== nextGameKey) {
      return { ...event, type: "game_changed" };
    }
    return null;
  }

  if (nextType === 2) return { ...event, type: "game_started" };
  if (nextType === 3) return { ...event, type: "studio_opened" };
  if (previousType === 2) return { ...event, type: "left_game" };
  if (previousType === 3) return { ...event, type: "studio_closed" };

  return { ...event, type: "status_changed" };
}

function online(presence) {
  return Number(presence?.presenceType ?? 0) !== 0;
}

/** Guarda la línea de tiempo necesaria para calcular duraciones y detectar cambios. */
export function updateState(account, previous, next, now = new Date()) {
  const nowIso = now.toISOString();
  const nextOnline = online(next);
  const previousOnline = previous ? online(previous) : false;

  const previousGameKey = gameKey(previous);
  const nextGameKey = gameKey(next);
  const previousType = Number(previous?.presenceType ?? 0);
  const nextType = Number(next.presenceType ?? 0);
  const gameChanged =
    Boolean(previous) &&
    previousType === 2 &&
    nextType === 2 &&
    Boolean(previousGameKey) &&
    Boolean(nextGameKey) &&
    previousGameKey !== nextGameKey;
  const activityChanged = !previous || previousType !== nextType || gameChanged;

  let sessionStart = previous?.sessionStart ?? null;
  if (nextOnline && (!previousOnline || !sessionStart)) {
    sessionStart = nowIso;
  }

  const isGame = next.presenceType === 2;
  const state = {
    userId: account.userId,
    username: account.username,
    displayName: account.displayName ?? previous?.displayName ?? null,

    presenceType: next.presenceType,
    isOnline: nextOnline,
    lastLocation: isGame ? next.lastLocation || previous?.lastLocation || "" : "",
    placeId: isGame ? next.placeId ?? previous?.placeId ?? null : null,
    rootPlaceId: isGame ? next.rootPlaceId ?? previous?.rootPlaceId ?? null : null,
    universeId: isGame ? next.universeId ?? previous?.universeId ?? null : null,
    gameId: isGame ? next.gameId ?? previous?.gameId ?? null : null,
    game: isGame ? next.game ?? previous?.game ?? null : null,

    since: activityChanged ? nowIso : previous?.since ?? nowIso,
    sessionStart,
    lastSeenAt: nextOnline ? nowIso : previous?.lastSeenAt ?? null,
    lastOnline: next.lastOnline?.toISOString() ?? previous?.lastOnline ?? null,
    lastSessionMs: previous?.lastSessionMs ?? null,
    lastPollAt: nowIso,
  };

  if (!nextOnline && previousOnline) {
    const startMs = timestampMs(sessionStart) ?? timestampMs(previous?.since);
    state.lastSessionMs = startMs ? Math.max(0, now.getTime() - startMs) : null;
    state.lastSeenAt = previous?.lastSeenAt ?? previous?.lastPollAt ?? null;
  }

  return state;
}

/**
 * Completa la presencia con datos del juego. Sin cookie de Roblox, la API
 * devuelve userPresenceType=2 pero oculta placeId/lastLocation.
 */
export async function resolveGameDetails(roblox, presence, logger = null) {
  if (Number(presence?.presenceType) !== 2) return null;

  const placeId = presence.placeId ?? presence.rootPlaceId ?? null;
  if (!placeId && !presence.lastLocation) return null;

  let details = null;
  try {
    if (presence.universeId) {
      details = await roblox.getGameByUniverse(presence.universeId, placeId);
    } else if (placeId) {
      details = await roblox.getPlaceDetails(placeId);
    }
  } catch (error) {
    logger?.warn?.(`No se pudo obtener la información del juego: ${error.message}`);
  }

  const rootPlaceId = presence.rootPlaceId ?? details?.rootPlaceId ?? placeId ?? null;
  const name = details?.name ?? (presence.lastLocation || null);
  if (!name && !placeId) return null;

  return {
    name,
    placeId,
    rootPlaceId,
    universeId: presence.universeId ?? details?.universeId ?? null,
    gameId: presence.gameId ?? null,
    iconUrl: details?.iconUrl ?? null,
    url: rootPlaceId ? `https://www.roblox.com/games/${rootPlaceId}` : details?.url ?? null,
    joinUrl: placeId
      ? `https://www.roblox.com/games/start?placeId=${placeId}${
          presence.gameId ? `&gameInstanceId=${encodeURIComponent(presence.gameId)}` : ""
        }`
      : null,
  };
}
