import fs from "node:fs";
import path from "node:path";
import util from "node:util";

const LEVELS = { debug: 10, info: 20, warn: 30, error: 40 };
const COLORS = {
  debug: "\x1b[90m",
  info: "\x1b[36m",
  warn: "\x1b[33m",
  error: "\x1b[31m",
  reset: "\x1b[0m",
};

function stringify(value) {
  if (value instanceof Error) return value.stack || value.message;
  if (typeof value === "string") return value;
  return util.inspect(value, { depth: 4, breakLength: 140, colors: false });
}

function localTimestamp(date, timeZone) {
  try {
    return new Intl.DateTimeFormat("sv-SE", {
      timeZone: timeZone || undefined,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    }).format(date);
  } catch {
    return date.toISOString().replace("T", " ").slice(0, 19);
  }
}

export function createLogger({ level = "info", timeZone = "", logFile = null } = {}) {
  const threshold = LEVELS[level] ?? LEVELS.info;
  const useColor = Boolean(process.stdout.isTTY) && !process.env.NO_COLOR;

  let stream = null;
  if (logFile) {
    try {
      fs.mkdirSync(path.dirname(logFile), { recursive: true });
      // Rotación sencilla: si supera 5 MB, se conserva una copia.
      if (fs.existsSync(logFile) && fs.statSync(logFile).size > 5 * 1024 * 1024) {
        fs.rmSync(`${logFile}.1`, { force: true });
        fs.renameSync(logFile, `${logFile}.1`);
      }
      stream = fs.createWriteStream(logFile, { flags: "a", encoding: "utf8" });
      stream.on("error", () => {
        stream = null;
      });
    } catch {
      stream = null;
    }
  }

  function write(levelName, args) {
    if (LEVELS[levelName] < threshold) return;

    const now = new Date();
    const message = args.map(stringify).join(" ");
    const color = useColor ? COLORS[levelName] : "";
    const reset = useColor ? COLORS.reset : "";

    console.log(`${color}[${localTimestamp(now, timeZone)}] ${levelName.toUpperCase().padEnd(5)} ${message}${reset}`);
    stream?.write(`${now.toISOString()} [${levelName.toUpperCase()}] ${message}\n`);
  }

  return {
    debug: (...args) => write("debug", args),
    info: (...args) => write("info", args),
    warn: (...args) => write("warn", args),
    error: (...args) => write("error", args),
    close: () => {
      try {
        stream?.end();
      } catch {
        /* ignorar */
      }
    },
  };
}
