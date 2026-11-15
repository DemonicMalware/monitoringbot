import path from "node:path";
import fs from "node:fs";
import {
  loadAccounts,
  loadState,
  saveAccounts,
  writeJson,
  fileMtime,
} from "./store.js";
import {
  detectEvent,
  normalizePresence,
  resolveGameDetails,
  updateState,
} from "./presence.js";
import { buildEventPayload, buildStartupPayload, buildStatusPayload } from "./events.js";

const EVENT_FLAGS = {
  connected: "connect",
  disconnected: "disconnect",
  game_started: "gameStart",
  game_changed: "gameChange",
  left_game: "leaveGame",
  studio_opened: "studio",
  studio_closed: "studio",
  status_changed: "statusChange",
};

export class Monitor {
  constructor({ rootDir, config, logger, roblox, discord }) {
    this.rootDir = rootDir;
    this.config = config;
    this.logger = logger;
    this.roblox = roblox;
    this.discord = discord;

    this.accountsFile = path.join(rootDir, "accounts.json");
    this.stateFile = path.join(rootDir, "data", "state.json");
    this.eventsFile = path.join(rootDir, "data", "events.jsonl");

    this.state = loadState(this.stateFile, logger);
    if (!this.state.panel || typeof this.state.panel !== "object") this.state.panel = {};
    this.accounts = [];
    this.accountsMtime = -1;
    this.timer = null;
    this.stopped = false;
    this.polling = false;
  }

  async start() {
    await this.reloadAccounts();
    this.logger.info(`Cuentas a vigilar: ${this.accounts.length}`);

    let initialPollOk = true;
    try {
      await this.poll({ initial: true });
    } catch (error) {
      initialPollOk = false;
      this.logger.error("No se pudo obtener el estado inicial; se reintentará en el siguiente ciclo:", error);
    }

    if (initialPollOk && this.config.notify.startupSummary) {
      await this.sendStartupSummary();
    }

    this.scheduleNext();
  }

  async stop() {
    this.stopped = true;
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
    this.saveState();
    await this.discord.flush?.();
  }

  scheduleNext() {
    if (this.stopped) return;
    const delayMs = Math.max(5, this.config.pollIntervalSeconds) * 1000;
    this.timer = setTimeout(() => {
      void this.runPoll();
    }, delayMs);
  }

  async runPoll() {
    if (this.stopped) return;
    const startedAt = Date.now();

    try {
      await this.poll();
    } catch (error) {
      this.logger.error("Error durante el ciclo de monitoreo:", error);
    }

    const elapsed = Date.now() - startedAt;
    const delay = Math.max(2000, this.config.pollIntervalSeconds * 1000 - elapsed);

    if (!this.stopped) {
      this.timer = setTimeout(() => {
        void this.runPoll();
      }, delay);
    }
  }

  /** Carga accounts.json y resuelve los IDs que falten. */
  async reloadAccounts() {
    const mtime = fileMtime(this.accountsFile);
    if (mtime === this.accountsMtime) return;
    this.accountsMtime = mtime;

    let accounts;
    try {
      accounts = loadAccounts(this.accountsFile);
    } catch (error) {
      this.logger.error(`No se pudo leer ${this.accountsFile}: ${error.message}`);
      return;
    }

    const unresolved = accounts.filter((account) => !account.userId);
    if (unresolved.length > 0) {
      try {
        const resolved = await this.roblox.resolveUsernames(unresolved.map((account) => account.username));
        for (const account of accounts) {
          if (account.userId) continue;
          const found = resolved.get(account.username.toLowerCase());
          if (found) {
            account.userId = found.id;
            account.username = found.name;
            account.displayName = found.displayName;
            this.logger.info(`Cuenta resuelta: @${found.name} (ID ${found.id})`);
          } else {
            this.logger.warn(`No se encontró la cuenta "${account.username}" en Roblox. Se ignorará.`);
          }
        }
        // Guardamos los IDs resueltos para no repetir la consulta al reiniciar.
        // Las cuentas no encontradas se conservan para que se puedan corregir.
        if (accounts.length > 0) {
          try {
            saveAccounts(this.accountsFile, accounts);
            this.accountsMtime = fileMtime(this.accountsFile);
          } catch (error) {
            this.logger.warn(`No se pudo guardar accounts.json con los IDs resueltos: ${error.message}`);
          }
        }
      } catch (error) {
        this.logger.error(`No se pudieron resolver las cuentas: ${error.message}`);
        // Reintentar en el siguiente ciclo.
        this.accountsMtime = -1;
      }
    }

    const byId = new Map();
    for (const account of accounts) {
      if (account.userId) byId.set(account.userId, account);
    }
    this.accounts = [...byId.values()];

    if (this.accounts.length > 0 && this.accounts.length <= 12) {
      this.logger.info(`Vigilando: ${this.accounts.map((account) => `@${account.username}`).join(", ")}`);
    }
  }

  async poll({ initial = false } = {}) {
    if (this.polling) return;
    this.polling = true;

    try {
      if (!initial) await this.reloadAccounts();

      if (this.accounts.length === 0) {
        this.logger.warn("No hay cuentas con ID válido. Usa: node bot.js add <usuario>");
        this.saveState();
        await this.updateStatusPanel();
        return;
      }

      const now = new Date();
      const presences = await this.roblox.getPresences(this.accounts.map((account) => account.userId));
      const byUserId = new Map(presences.map((presence) => [Number(presence.userId), presence]));

      const events = [];
      let processed = 0;

      for (const account of this.accounts) {
        try {
          const raw = byUserId.get(Number(account.userId));
          if (!raw) {
            this.logger.warn(`Roblox no devolvió presencia para @${account.username} (ID ${account.userId}).`);
            continue;
          }

          const next = normalizePresence(raw);
          if (next.presenceType === 2) {
            next.game = await resolveGameDetails(this.roblox, next, this.logger);
          }

          const previous = this.state.users[account.userId] ?? null;

          if (!initial && previous) {
            const event = detectEvent(previous, next, now);
            if (event) {
              events.push({
                ...event,
                account,
                prev: previous,
                next,
              });
            }
          }

          this.state.users[account.userId] = updateState(account, previous, next, now);
          processed += 1;
        } catch (error) {
          this.logger.error(`Error procesando @${account.username}: ${error.message}`);
        }
      }

      this.state.updatedAt = now.toISOString();
      this.saveState();

      this.logger.info(
        `Presencia actualizada: ${processed}/${this.accounts.length} cuenta(s)${initial ? " (estado inicial)" : ""}.`,
      );

      if (!initial) {
        for (const event of events) {
          await this.notify(event);
        }
      }

      await this.updateStatusPanel();
    } finally {
      this.polling = false;
    }
  }

  /**
   * Crea o edita el mensaje fijo donde se ve el estado actual de cada cuenta.
   * Si Discord responde 404 (mensaje borrado), se publica uno nuevo.
   */
  async updateStatusPanel() {
    if (this.config.statusPanel?.enabled === false) return;
    // Sin cuentas no se crea un panel nuevo, pero sí se actualiza el existente.
    if (this.accounts.length === 0 && !this.state.panel.messageId) return;

    const now = new Date();
    const entries = this.accounts.map((account) => ({
      account,
      state: this.state.users[account.userId] ?? null,
    }));

    const payload = buildStatusPayload({ entries, config: this.config, now });
    const panel = this.state.panel;

    if (panel.messageId) {
      try {
        await this.discord.edit(panel.messageId, payload);
        panel.updatedAt = now.toISOString();
        return;
      } catch (error) {
        if (error.status !== 404) {
          this.logger.error(`No se pudo actualizar el panel de estado: ${error.message}`);
          return;
        }
        this.logger.warn("El panel de estado ya no existe en Discord. Se publicará uno nuevo.");
        panel.messageId = null;
        panel.channelId = null;
      }
    }

    try {
      const message = await this.discord.send(payload, { wait: true });
      const messageId = message?.id ?? null;

      if (messageId && !message?.dryRun) {
        panel.messageId = String(messageId);
        panel.channelId = message.channel_id ?? null;
        panel.createdAt = now.toISOString();
        panel.updatedAt = now.toISOString();
        this.logger.info(`Panel de estado publicado (mensaje ${panel.messageId}).`);
        this.saveState();
      }
    } catch (error) {
      this.logger.error(`No se pudo publicar el panel de estado: ${error.message}`);
    }
  }

  async notify(event) {
    this.appendEvent(event);

    const flag = EVENT_FLAGS[event.type];
    if (flag && this.config.notify[flag] === false) return;

    try {
      const avatarUrl = await this.roblox
        .getAvatarUrl(event.account.userId)
        .catch(() => null);

      const payload = buildEventPayload({
        ...event,
        avatarUrl,
        config: this.config,
      });

      if (!payload) return;

      await this.discord.send(payload);
      this.logger.info(
        `Aviso enviado: ${event.type} · @${event.account.username} (${event.at.toISOString()})`,
      );
    } catch (error) {
      this.logger.error(
        `No se pudo enviar el aviso ${event.type} de @${event.account.username}: ${error.message}`,
      );
    }
  }

  async sendStartupSummary() {
    const entries = this.accounts.map((account) => ({
      account,
      state: this.state.users[account.userId] ?? null,
    }));

    try {
      await this.discord.send(
        buildStartupPayload({
          entries,
          config: this.config,
        }),
      );
      this.logger.info(
        this.config.dryRun ? "Resumen inicial generado en modo dry-run (no enviado)." : "Resumen inicial enviado a Discord.",
      );
    } catch (error) {
      this.logger.error(`No se pudo enviar el resumen inicial: ${error.message}`);
    }
  }

  /** Historial append-only de todos los cambios detectados, aunque se silencien avisos. */
  appendEvent(event) {
    const record = {
      detectedAt: event.at.toISOString(),
      type: event.type,
      userId: event.account.userId,
      username: event.account.username,
      displayName: event.account.displayName ?? null,
      previousType: event.prev?.presenceType ?? null,
      presenceType: event.next?.presenceType ?? null,
      game: event.next?.game ?? event.prev?.game ?? null,
      sessionMs: event.sessionMs ?? null,
      activityMs: event.activityMs ?? null,
      lastOnline: event.lastOnline ? new Date(event.lastOnline).toISOString() : null,
    };

    try {
      fs.mkdirSync(path.dirname(this.eventsFile), { recursive: true });
      fs.appendFileSync(this.eventsFile, `${JSON.stringify(record)}\n`, "utf8");
    } catch (error) {
      this.logger.warn(`No se pudo escribir el historial de eventos: ${error.message}`);
    }
  }

  saveState() {
    try {
      writeJson(this.stateFile, this.state);
    } catch (error) {
      this.logger.error(`No se pudo guardar el estado: ${error.message}`);
    }
  }
}
