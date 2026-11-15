export function unixSeconds(value) {
  return Math.floor(new Date(value).getTime() / 1000);
}

/** Marca de tiempo de Discord: F = fecha y hora completas, R = relativa. */
export function discordTime(value, style = "F") {
  return `<t:${unixSeconds(value)}:${style}>`;
}

export function utcString(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "fecha desconocida";
  const pad = (number) => String(number).padStart(2, "0");
  return `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}:${pad(date.getUTCSeconds())} UTC`;
}

export function localString(value, timeZone = "") {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "fecha desconocida";
  try {
    return new Intl.DateTimeFormat("es-ES", {
      timeZone: timeZone || undefined,
      dateStyle: "short",
      timeStyle: "medium",
    }).format(date);
  } catch {
    return date.toISOString().replace("T", " ").slice(0, 19);
  }
}

export function formatDuration(milliseconds) {
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return "desconocido";

  const totalSeconds = Math.floor(milliseconds / 1000);
  if (totalSeconds < 1) return "menos de 1 s";

  const days = Math.floor(totalSeconds / 86400);
  const hours = Math.floor((totalSeconds % 86400) / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;

  const parts = [];
  if (days) parts.push(`${days} d`);
  if (hours) parts.push(`${hours} h`);
  if (minutes) parts.push(`${minutes} min`);
  if (!days && !hours && seconds) parts.push(`${seconds} s`);

  return parts.slice(0, 3).join(" ") || "menos de 1 s";
}

export function escapeMarkdown(text) {
  return String(text ?? "").replace(/([\\`*_~|>[\]])/g, "\\$1");
}

export function truncate(text, maxLength = 1024) {
  const value = String(text ?? "");
  if (value.length <= maxLength) return value;
  return `${value.slice(0, Math.max(0, maxLength - 1))}…`;
}

export function gameLabel(game) {
  if (!game) return null;
  return game.name ? escapeMarkdown(game.name) : null;
}

export function presenceLabel(state) {
  if (!state) return "Sin datos";

  switch (Number(state.presenceType)) {
    case 0:
      return "Desconectado";
    case 1:
      return "En línea (sitio web)";
    case 2: {
      const name = gameLabel(state.game);
      return name ? `Jugando **${name}**` : "Jugando (detalles no disponibles)";
    }
    case 3:
      return "En Roblox Studio";
    default:
      return "Estado desconocido";
  }
}

export function presenceTypeLabel(type) {
  return presenceLabel({ presenceType: type });
}
