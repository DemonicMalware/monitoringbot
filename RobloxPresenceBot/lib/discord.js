import { setTimeout as sleep } from "node:timers/promises";
import { request } from "./http.js";

export class DiscordWebhook {
  constructor({
    url,
    username = "",
    avatarUrl = "",
    minIntervalMs = 600,
    timeoutMs = 15000,
    logger = null,
    dryRun = false,
  } = {}) {
    this.url = url;
    this.username = username;
    this.avatarUrl = avatarUrl;
    this.minIntervalMs = minIntervalMs;
    this.timeoutMs = timeoutMs;
    this.logger = logger;
    this.dryRun = dryRun;

    this.queue = [];
    this.sending = false;
  }

  async validate() {
    if (this.dryRun) return { name: "dry-run", channel_id: "dry-run" };

    const response = await request(this.url, {
      timeoutMs: this.timeoutMs,
      retries: 1,
      logger: this.logger,
    });

    if (response.status === 401 || response.status === 404) {
      throw new Error(
        "El webhook de Discord no existe o fue eliminado. Comprueba la URL en config.json.",
      );
    }
    if (!response.ok) {
      throw new Error(`Discord respondió HTTP ${response.status} al validar el webhook.`);
    }
    return response.data ?? {};
  }

  send(payload, { wait = false } = {}) {
    return this.#enqueue({ method: "POST", payload, wait });
  }

  /** Edita un mensaje ya enviado por este webhook (panel de estado). */
  edit(messageId, payload) {
    return this.#enqueue({ method: "PATCH", messageId: String(messageId), payload });
  }

  #enqueue(task) {
    return new Promise((resolve, reject) => {
      this.queue.push({ ...task, resolve, reject });
      void this.#drain();
    });
  }

  /** Espera a que la cola termine antes de cerrar el proceso. */
  async flush(timeoutMs = 15000) {
    const startedAt = Date.now();
    while ((this.sending || this.queue.length > 0) && Date.now() - startedAt < timeoutMs) {
      await sleep(100);
    }
  }

  async #drain() {
    if (this.sending) return;
    this.sending = true;

    try {
      while (this.queue.length > 0) {
        const item = this.queue.shift();
        try {
          item.resolve(await this.#executeOne(item));
        } catch (error) {
          item.reject(error);
        }

        if (this.queue.length > 0) {
          await sleep(this.minIntervalMs);
        }
      }
    } finally {
      this.sending = false;
    }
  }

  #prepare(payload) {
    const prepared = { ...payload };
    if (this.username) prepared.username = this.username;
    if (this.avatarUrl) prepared.avatar_url = this.avatarUrl;
    return prepared;
  }

  async #executeOne({ method, messageId = null, payload: rawPayload, wait = false }) {
    let payload = this.#prepare(rawPayload);
    const isEdit = method === "PATCH";

    if (this.dryRun) {
      if (isEdit) {
        this.logger?.info(`[dry-run] Se editaría el mensaje ${messageId}:\n${JSON.stringify(payload, null, 2)}`);
        return { id: messageId, channel_id: "dry-run", dryRun: true };
      }
      this.logger?.info(`[dry-run] Mensaje para Discord:\n${JSON.stringify(payload, null, 2)}`);
      return { id: "dry-run", channel_id: "dry-run", dryRun: true };
    }

    const url = isEdit
      ? `${this.url}/messages/${encodeURIComponent(messageId)}`
      : wait
        ? `${this.url}?wait=true`
        : this.url;

    for (let attempt = 1; attempt <= 4; attempt += 1) {
      const response = await request(url, {
        method,
        body: payload,
        timeoutMs: this.timeoutMs,
        retries: 0,
        logger: this.logger,
      });

      if (response.status === 200 || response.status === 204) {
        return response.data ?? { id: messageId, status: response.status };
      }

      if (response.status === 429) {
        const retryAfter = Number(response.headers.get("retry-after")) || Number(response.data?.retry_after) || 1;
        this.logger?.warn?.(`Discord limitó el envío (429). Esperando ${retryAfter}s...`);
        await sleep(retryAfter * 1000 + 300);
        continue;
      }

      if (response.status === 400 && payload.components) {
        this.logger?.warn?.("Discord rechazó los botones del mensaje. Se reintentará sin botones.");
        payload = { ...payload };
        delete payload.components;
        continue;
      }

      if (response.status >= 500 && attempt < 4) {
        await sleep(1000 * attempt);
        continue;
      }

      const error = new Error(
        `Discord respondió HTTP ${response.status}: ${(response.text || "").slice(0, 400)}`,
      );
      error.status = response.status;
      error.body = response.data ?? response.text;
      throw error;
    }

    throw new Error("No se pudo enviar el mensaje a Discord después de varios intentos.");
  }
}
