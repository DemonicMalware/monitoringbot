import { setTimeout as sleep } from "node:timers/promises";

const USER_AGENT = "RobloxPresenceBot/1.0 (Node.js)";

export class HttpError extends Error {
  constructor(message, { url, status = null, body = null, cause = null } = {}) {
    super(message, cause ? { cause } : undefined);
    this.name = "HttpError";
    this.url = url;
    this.status = status;
    this.body = body;
  }
}

function parseJson(text) {
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

/**
 * Petición HTTP con timeout, reintentos y respeto de Retry-After.
 * No lanza por respuestas 4xx/5xx: devuelve la respuesta para que cada
 * integración decida qué hacer.
 */
export async function request(url, options = {}) {
  const {
    method = "GET",
    headers = {},
    body,
    timeoutMs = 15000,
    retries = 2,
    retryDelayMs = 1200,
    logger = null,
  } = options;

  const maxAttempts = Math.max(1, retries + 1);
  let lastError = null;

  for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);

    try {
      const requestHeaders = {
        "user-agent": USER_AGENT,
        accept: "application/json",
        ...(body !== undefined ? { "content-type": "application/json" } : {}),
        ...headers,
      };

      const response = await fetch(url, {
        method,
        headers: requestHeaders,
        body:
          body === undefined || body === null
            ? undefined
            : typeof body === "string"
              ? body
              : JSON.stringify(body),
        signal: controller.signal,
      });

      const text = await response.text();
      const data = parseJson(text);

      if (response.status === 429) {
        const retryAfter = Number(response.headers.get("retry-after")) || 2;
        if (attempt < maxAttempts) {
          logger?.warn?.(`Límite de peticiones (429). Reintentando en ${retryAfter}s...`);
          await sleep(retryAfter * 1000 + 250);
          continue;
        }
      }

      if (response.status >= 500 && attempt < maxAttempts) {
        const waitMs = retryDelayMs * 2 ** (attempt - 1) + Math.floor(Math.random() * 300);
        logger?.warn?.(`Servidor respondió ${response.status}. Reintentando en ${Math.round(waitMs / 1000)}s...`);
        await sleep(waitMs);
        continue;
      }

      return {
        status: response.status,
        ok: response.ok,
        headers: response.headers,
        text,
        data,
      };
    } catch (error) {
      lastError = error;
      if (attempt >= maxAttempts) break;

      const waitMs = retryDelayMs * 2 ** (attempt - 1) + Math.floor(Math.random() * 300);
      logger?.warn?.(
        `Error de red (${error.name === "AbortError" ? "tiempo de espera agotado" : error.message}). Reintentando en ${Math.round(waitMs / 1000)}s...`,
      );
      await sleep(waitMs);
    } finally {
      clearTimeout(timer);
    }
  }

  throw new HttpError(`No se pudo completar la petición a ${url}`, {
    url,
    cause: lastError,
  });
}
