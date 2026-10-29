// azeroth-sdk TypeScript client for the azsdk daemon's JSON-RPC API. The game API and its types are generated from
// the SDK itself (api.ts, types.ts: cmd/apigen), so they match the Lua API exactly: same names, same fields.
//
//   const az = new Azeroth("http://127.0.0.1:8780");
//   const me = az.character("Garrick");
//   const status = await me.api.me.status();
//   const r = await me.api.move.to(-9460, 70, 56);           // a Result<MoveDone>: pending until it resolves
//   const done = await me.await(r);                           // wait for the result
//   for await (const ev of me.events({ types: ["chat"] })) console.log(ev.data.from, ev.data.text);
//
// GUIDs are strings. Rage and runic power are x10 (0-1000), as the server sends them.
import { gameApi, type GameApi } from "./api.ts";
import type { AnyEvent, Event, Result } from "./types.ts";

export * from "./types.ts";
export type { GameApi } from "./api.ts";

export class RpcError extends Error {
  constructor(public code: number, message: string) { super(message); }
}

export interface EventQuery { since?: number; types?: string[]; match?: string; last?: number; limit?: number }

export class Azeroth {
  private id = 0;
  constructor(public url = "http://127.0.0.1:8780") {}

  async call<T = unknown>(method: string, params: Record<string, unknown> = {}): Promise<T> {
    const res = await fetch(`${this.url}/rpc`, {
      method: "POST", headers: { "content-type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: ++this.id, method, params }),
    });
    const body = await res.json() as { result?: T; error?: { code: number; message: string } };
    if (body.error) throw new RpcError(body.error.code, body.error.message);
    return body.result as T;
  }

  sessions() { return this.call<{ char: string; status: string; error?: string }[]>("sessions.list"); }
  character(name: string) { return new Character(this, name); }
}

export class Character {
  /** The game API (docs/api-reference.md): me, units, objects, world, move, combat, spells, items, vendor, ... */
  readonly api: GameApi;

  constructor(private az: Azeroth, public readonly name: string) {
    this.api = gameApi((fn, args) => this.call("api.call", { fn, args }));
  }
  private call<T>(method: string, params: Record<string, unknown> = {}) {
    return this.az.call<T>(method, { char: this.name, ...params });
  }

  /** Run Lua in the character's session (what its scripts see). */
  eval<T = unknown>(code: string) { return this.call<{ result: T; events?: unknown; script_errors?: unknown }>("script.eval", { code }); }

  /** Past events (nothing is consumed). */
  query(q: EventQuery = {}) { return this.call<AnyEvent[]>("events.query", { ...q }); }

  /** The next events matching q after since (default: now), or [] after timeoutS. */
  wait(q: EventQuery = {}, timeoutS = 60) {
    return this.call<{ events: AnyEvent[]; timed_out: boolean; seq: number }>("events.wait", { ...q, timeout_s: timeoutS });
  }

  /** Every matching event from now on (or from q.since). */
  async *events(q: EventQuery = {}): AsyncGenerator<AnyEvent> {
    let since = q.since;
    for (;;) {
      const r = await this.call<{ events: AnyEvent[]; seq: number }>("events.wait", { ...q, since, timeout_s: 60 });
      for (const ev of r.events) yield ev;
      since = r.events.length ? r.events[r.events.length - 1].seq : r.seq;
    }
  }

  /** Wait for an action's result (the request an api call returned). */
  async await<T>(req: Result<T>, timeoutS = 60): Promise<Result<T>> {
    if (!req.pending) return req;
    const deadline = Date.now() + timeoutS * 1000;
    let since: number | undefined;
    while (Date.now() < deadline) {
      const r = await this.call<{ events: Event<Result<T>>[]; seq: number }>("events.wait",
        { types: ["result"], match: `"id":${req.id},`, since, timeout_s: Math.max(1, (deadline - Date.now()) / 1000) });
      const hit = r.events.find((e) => e.data.id === req.id);
      if (hit) return hit.data;
      since = r.seq;
    }
    return { ...req, ok: false, error: { code: "wait_timeout", message: "stopped waiting; the request may still finish" } };
  }
}
