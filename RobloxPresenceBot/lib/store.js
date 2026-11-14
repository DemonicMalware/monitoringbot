import fs from "node:fs";
import path from "node:path";

/**
 * Lee un JSON. Si el archivo no existe devuelve una copia del valor por defecto.
 * Si existe pero está corrupto, lanza un error para no perder datos en silencio.
 */
export function readJson(file, fallback) {
  try {
    const raw = fs.readFileSync(file, "utf8");
    return JSON.parse(raw);
  } catch (error) {
    if (error.code === "ENOENT") return structuredClone(fallback);
    throw new Error(`No se pudo leer ${file}: ${error.message}`);
  }
}

/** Escritura atómica para no dejar archivos a medias si el proceso se corta. */
export function writeJson(file, data) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const tmp = `${file}.${process.pid}.tmp`;
  fs.writeFileSync(tmp, `${JSON.stringify(data, null, 2)}\n`, "utf8");
  try {
    fs.renameSync(tmp, file);
  } catch {
    // En algunos sistemas Windows el rename sobre un archivo existente puede fallar.
    fs.copyFileSync(tmp, file);
    fs.unlinkSync(tmp);
  }
}

export function fileMtime(file) {
  try {
    return fs.statSync(file).mtimeMs;
  } catch {
    return 0;
  }
}

/**
 * Acepta tanto ["usuario"] como [{ "username": "usuario", "userId": 123 }].
 * Devuelve siempre una lista normalizada y sin duplicados.
 */
export function loadAccounts(file) {
  const raw = readJson(file, []);
  const list = Array.isArray(raw) ? raw : Array.isArray(raw?.accounts) ? raw.accounts : [];

  const accounts = [];
  const seen = new Set();

  for (const entry of list) {
    const username = cleanUsername(
      typeof entry === "string" ? entry : entry?.username ?? entry?.name ?? "",
    );
    if (!username) continue;

    const key = username.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);

    const rawId = typeof entry === "object" ? entry.userId ?? entry.id : null;
    const userId = Number.isFinite(Number(rawId)) && Number(rawId) > 0 ? Number(rawId) : null;

    accounts.push({
      username,
      userId,
      displayName: typeof entry === "object" ? entry.displayName ?? null : null,
      addedAt: typeof entry === "object" ? entry.addedAt ?? null : null,
    });
  }

  return accounts;
}

export function saveAccounts(file, accounts) {
  const clean = accounts.map((account) => ({
    username: account.username,
    displayName: account.displayName ?? null,
    userId: account.userId ?? null,
    addedAt: account.addedAt ?? null,
  }));
  writeJson(file, clean);
}

export function cleanUsername(value) {
  return String(value ?? "")
    .trim()
    .replace(/^@+/, "")
    .replace(/\s+/g, "");
}

export function loadState(file, logger) {
  try {
    const state = readJson(file, { version: 1, users: {}, updatedAt: null });
    if (!state || typeof state !== "object") throw new Error("formato inválido");
    if (!state.users || typeof state.users !== "object") state.users = {};
    state.version = 1;
    return state;
  } catch (error) {
    const backup = `${file}.corrupt-${Date.now()}`;
    try {
      if (fs.existsSync(file)) fs.renameSync(file, backup);
      logger?.warn(`Estado corrupto en ${file}. Se guardó una copia en ${backup} y se empieza de cero.`);
    } catch {
      logger?.warn(`Estado corrupto en ${file}. Se empieza de cero.`);
    }
    return { version: 1, users: {}, updatedAt: null };
  }
}
