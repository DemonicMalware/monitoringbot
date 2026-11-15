import {
  discordTime,
  escapeMarkdown,
  formatDuration,
  presenceLabel,
  truncate,
  utcString,
} from "./format.js";

const COLORS = {
  green: 0x57f287,
  red: 0xed4245,
  blurple: 0x5865f2,
  yellow: 0xfee75c,
  gray: 0x99aab5,
  orange: 0xe67e22,
};

function displayName(account) {
  return account.displayName && account.displayName !== account.username
    ? `${account.displayName} (@${account.username})`
    : account.username;
}

function baseEmbed(account, at, avatarUrl) {
  return {
    author: {
      name: truncate(displayName(account), 250),
      ...(avatarUrl ? { icon_url: avatarUrl } : {}),
    },
    footer: { text: truncate(`Roblox Presence Monitor · ${utcString(at)}`, 2048) },
    timestamp: new Date(at).toISOString(),
  };
}

function accountField(account) {
  return {
    name: "👤 Cuenta",
    value: `\`${account.username}\`\nID: ${account.userId}`,
    inline: true,
  };
}

function timeField(at, label = "🕒 Fecha y hora") {
  return {
    name: label,
    value: `${discordTime(at, "F")}\n${discordTime(at, "R")}`,
    inline: false,
  };
}

function gameField(game, label = "🎮 Juego") {
  if (!game?.name) {
    return {
      name: label,
      value: game?.placeId
        ? `Detalles no disponibles\nLugar: \`${game.placeId}\``
        : "Detalles no disponibles (falta la cookie de Roblox o la cuenta es privada)",
      inline: false,
    };
  }

  const name = game.url ? `[${escapeMarkdown(game.name)}](${game.url})` : escapeMarkdown(game.name);
  const lines = [name];
  const placeId = game.rootPlaceId ?? game.placeId;
  if (placeId) lines.push(`Lugar: \`${placeId}\``);
  if (game.gameId) lines.push(`Servidor: \`${game.gameId}\``);

  return {
    name: label,
    value: truncate(lines.join("\n"), 1024),
    inline: true,
  };
}

function gameButtons(game) {
  if (!game?.url) return undefined;
  const buttons = [{ type: 2, style: 5, label: "Ver juego", url: game.url }];
  if (game.joinUrl) buttons.push({ type: 2, style: 5, label: "Unirse", url: game.joinUrl });
  return [{ type: 1, components: buttons }];
}

function withMention(payload, config) {
  if (config?.mention) payload.content = config.mention;
  return payload;
}

function gameThumbnail(game) {
  return game?.iconUrl ? { thumbnail: { url: game.iconUrl } } : {};
}

function plausibleOfflineTime(lastOnline, now) {
  if (!lastOnline) return null;
  const value = new Date(lastOnline).getTime();
  if (Number.isNaN(value)) return null;
  // Solo lo usamos si parece la hora real de esta desconexión.
  if (value > now.getTime() + 5000) return null;
  if (value < now.getTime() - 15 * 60 * 1000) return null;
  return new Date(value);
}

export function buildEventPayload({ type, account, prev, next, at, avatarUrl, config }) {
  const name = `**${escapeMarkdown(displayName(account))}**`;
  const base = baseEmbed(account, at, avatarUrl);
  let embed = null;

  switch (type) {
    case "connected": {
      const game = next.game;
      const title =
        next.presenceType === 2
          ? "🟢 Se conectó y está jugando"
          : next.presenceType === 3
            ? "🟢 Se conectó a Roblox Studio"
            : "🟢 Se conectó a Roblox";

      const fields = [
        accountField(account),
        { name: "📌 Estado", value: presenceLabel(next), inline: true },
      ];
      if (game) fields.push(gameField(game));
      fields.push(timeField(at));

      embed = {
        ...base,
        title,
        description: `${name} se conectó a Roblox.`,
        color: COLORS.green,
        fields,
        ...gameThumbnail(game),
      };
      break;
    }

    case "disconnected": {
      const offlineAt = plausibleOfflineTime(next.lastOnline, at);
      const sessionStart = prev.sessionStart ? new Date(prev.sessionStart).getTime() : null;
      const sessionMs =
        offlineAt && sessionStart && offlineAt.getTime() >= sessionStart
          ? offlineAt.getTime() - sessionStart
          : prev.sessionStart
            ? Math.max(0, at.getTime() - new Date(prev.sessionStart).getTime())
            : prev.since
              ? Math.max(0, at.getTime() - new Date(prev.since).getTime())
              : null;

      const fields = [
        accountField(account),
        { name: "📌 Última actividad", value: presenceLabel(prev), inline: true },
        { name: "⏱️ Tiempo en línea", value: formatDuration(sessionMs), inline: true },
      ];
      if (offlineAt) {
        fields.push({
          name: "🕒 Hora de desconexión (Roblox)",
          value: `${discordTime(offlineAt, "F")}\n${discordTime(offlineAt, "R")}`,
          inline: false,
        });
      }
      fields.push(timeField(at, "🕒 Detectado por el bot"));

      embed = {
        ...base,
        title: "🔴 Se desconectó de Roblox",
        description: `${name} se desconectó.`,
        color: COLORS.red,
        fields,
      };
      break;
    }

    case "game_started": {
      const game = next.game;
      embed = {
        ...base,
        title: game?.name ? "🎮 Empezó a jugar" : "🎮 Entró a un juego",
        description: game?.name
          ? `${name} empezó a jugar **${escapeMarkdown(game.name)}**.`
          : `${name} entró a un juego, pero Roblox no compartió el nombre (configura la cookie para verlo).`,
        color: COLORS.blurple,
        fields: [
          accountField(account),
          gameField(game),
          timeField(at),
        ],
        ...gameThumbnail(game),
      };
      break;
    }

    case "game_changed": {
      const from = prev.game;
      const to = next.game;
      embed = {
        ...base,
        title: "🔄 Cambió de juego",
        description: `${name} cambió de juego.`,
        color: COLORS.yellow,
        fields: [
          accountField(account),
          gameField(from, "🎮 Juego anterior"),
          gameField(to, "🎮 Juego nuevo"),
          timeField(at),
        ],
        ...gameThumbnail(to),
      };
      break;
    }

    case "left_game": {
      const game = prev.game;
      embed = {
        ...base,
        title: "🚪 Salió del juego",
        description: `${name} dejó de jugar${game?.name ? ` **${escapeMarkdown(game.name)}**` : ""}.`,
        color: COLORS.gray,
        fields: [
          accountField(account),
          ...(game ? [gameField(game)] : []),
          { name: "⏱️ Tiempo en el juego", value: formatDuration(prev.since ? at.getTime() - new Date(prev.since).getTime() : null), inline: true },
          timeField(at),
        ],
      };
      break;
    }

    case "studio_opened": {
      embed = {
        ...base,
        title: "🛠️ Abrió Roblox Studio",
        description: `${name} abrió Roblox Studio.`,
        color: COLORS.orange,
        fields: [accountField(account), timeField(at)],
      };
      break;
    }

    case "studio_closed": {
      embed = {
        ...base,
        title: "🛠️ Cerró Roblox Studio",
        description: `${name} cerró Roblox Studio.`,
        color: COLORS.gray,
        fields: [
          accountField(account),
          {
            name: "⏱️ Tiempo en Studio",
            value: formatDuration(prev.since ? at.getTime() - new Date(prev.since).getTime() : null),
            inline: true,
          },
          timeField(at),
        ],
      };
      break;
    }

    case "status_changed": {
      embed = {
        ...base,
        title: "🔄 Cambió de estado",
        description: `${name} cambió de estado.`,
        color: COLORS.yellow,
        fields: [
          accountField(account),
          { name: "📌 Estado anterior", value: presenceLabel(prev), inline: true },
          { name: "📌 Estado nuevo", value: presenceLabel(next), inline: true },
          timeField(at),
        ],
      };
      break;
    }

    default:
      return null;
  }

  const payload = { embeds: [embed] };
  const components = ["connected", "game_started", "game_changed"].includes(type)
    ? gameButtons(next.game)
    : undefined;
  if (components) payload.components = components;

  return withMention(payload, config);
}

function statusEmoji(state) {
  if (!state) return "⚪";
  switch (Number(state.presenceType)) {
    case 2:
      return "🎮";
    case 3:
      return "🛠️";
    case 1:
      return "🟢";
    default:
      return "⚪";
  }
}

function statusTimeLine(label, value) {
  return `${label}: ${discordTime(value, "F")} (${discordTime(value, "R")})`;
}

function statusAccountField(account, state) {
  const lines = [];

  if (!state) {
    lines.push("Sin datos todavía (esperando la primera consulta).");
  } else {
    const type = Number(state.presenceType ?? 0);
    lines.push(presenceLabel(state));

    if (type === 2 && state.game?.url) {
      lines.push(`🔗 [Abrir juego](${state.game.url})`);
    }

    if (type !== 0) {
      const onlineSince = state.sessionStart ?? state.since;
      if (onlineSince) lines.push(statusTimeLine("🕒 En línea desde", onlineSince));
      if (type === 2 && state.since) lines.push(statusTimeLine("🎮 Jugando desde", state.since));
      if (type === 3 && state.since) lines.push(statusTimeLine("🛠️ Studio desde", state.since));
    } else {
      const candidates = [state.lastOnline, state.lastSeenAt]
        .filter(Boolean)
        .map((value) => new Date(value).getTime())
        .filter((value) => Number.isFinite(value));
      if (candidates.length > 0) {
        const lastConnection = new Date(Math.max(...candidates)).toISOString();
        lines.push(statusTimeLine("🕒 Última conexión", lastConnection));
      }
      if (Number.isFinite(state.lastSessionMs) && state.lastSessionMs > 0) {
        lines.push(`⏳ Última sesión: ${formatDuration(state.lastSessionMs)}`);
      }
    }
  }

  return {
    name: truncate(`${statusEmoji(state)} @${account.username}`, 256),
    value: truncate(lines.join("\n"), 1024),
    inline: false,
  };
}

/**
 * Panel persistente: un solo mensaje que el monitor crea y edita en cada ciclo.
 * Discord admite 25 campos por embed, así que se reparten en varios embeds.
 */
export function buildStatusPayload({ entries, config, now = new Date() }) {
  const list = entries ?? [];
  const fields = list.map(({ account, state }) => statusAccountField(account, state));

  const chunks = [];
  for (let index = 0; index < fields.length; index += 25) {
    chunks.push(fields.slice(index, index + 25));
  }
  if (chunks.length === 0) chunks.push([]);

  const interval = config?.pollIntervalSeconds ?? 30;
  const updatedLabel = utcString(now);

  const embeds = chunks.map((chunk, index) => {
    const embed = {
      color: COLORS.blurple,
      fields: chunk,
    };

    if (index === 0) {
      embed.title = "📡 Estado de presencia en Roblox";
      embed.description =
        list.length === 0
          ? "No hay cuentas configuradas. Añádelas con `node bot.js add <usuario>`."
          : `Estado actual de las cuentas vigiladas. Este mensaje se actualiza automáticamente cada **${interval} s**. Cada persona ve las horas en su propia zona horaria.`;
    }

    if (index === chunks.length - 1) {
      embed.footer = {
        text: truncate(`Roblox Presence Monitor · Actualizado: ${updatedLabel}`, 2048),
      };
      embed.timestamp = new Date(now).toISOString();
    }

    return embed;
  });

  return { embeds };
}

export function buildStartupPayload({ entries, config }) {
  const total = entries.length;
  const fields = entries.slice(0, 24).map(({ account, state }) => ({
    name: truncate(`@${account.username}`, 256),
    value: state ? presenceLabel(state) : "Sin datos todavía",
    inline: true,
  }));

  if (total > 24) {
    fields.push({
      name: "…",
      value: `y ${total - 24} cuenta(s) más`,
      inline: false,
    });
  }

  return {
    embeds: [
      {
        title: "📡 Monitor de presencia iniciado",
        description:
          total === 0
            ? "No hay cuentas configuradas todavía. Añade cuentas con `node bot.js add <usuario>`."
            : `Vigilando **${total}** cuenta(s) cada **${config.pollIntervalSeconds} s**. Los cambios se notificarán en este canal.`,
        color: COLORS.blurple,
        fields,
        footer: { text: `Roblox Presence Monitor · ${utcString(new Date())}` },
        timestamp: new Date().toISOString(),
      },
    ],
  };
}

export function buildTestPayload({ config, channelInfo, accountCount, cookieUser }) {
  return {
    embeds: [
      {
        title: "✅ Webhook conectado",
        description:
          "El monitor puede enviar mensajes a este canal. Si ves este mensaje, la configuración del webhook es correcta.",
        color: COLORS.green,
        fields: [
          { name: "📡 Canal", value: `\`${channelInfo?.channel_id ?? "desconocido"}\``, inline: true },
          { name: "👥 Cuentas vigiladas", value: String(accountCount), inline: true },
          { name: "⏱️ Intervalo", value: `${config.pollIntervalSeconds} s`, inline: true },
          {
            name: "🍪 Cookie de Roblox",
            value: cookieUser
              ? `Válida: \`@${cookieUser.name}\`\nSe verán nombres de juegos y servidores.`
              : "No configurada\nSolo se verá conectado/desconectado/en juego.",
            inline: false,
          },
        ],
        footer: { text: `Roblox Presence Monitor · ${utcString(new Date())}` },
        timestamp: new Date().toISOString(),
      },
    ],
  };
}
