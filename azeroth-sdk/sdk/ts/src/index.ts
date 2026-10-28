// azeroth-sdk TypeScript client: typed access to the azsdk daemon's JSON-RPC API.
//
//   const az = new Azeroth("http://127.0.0.1:8780");
//   const me = az.character("Garrick");
//   const s = await me.state();
//   await me.moveTo(-9460, 70); await me.waitIdle();
//
// GUIDs are strings (64-bit). Rage and runic power are x10 (0-1000), as the server sends them.

export type Guid = string;

export interface Unit {
  guid: Guid; entry: number; name?: string; level: number; health: number; max_health: number;
  x: number; y: number; z: number; distance: number; is_alive: boolean; is_player: boolean;
  faction: number; npc_flags: number; flags: number; dyn_flags: number; lootable: boolean; target: Guid;
}

export interface BagItem { bag: number; slot: number; guid: Guid; entry: number; count: number }
export interface QuestEntry { id: number; complete: boolean; failed: boolean; counts: number[] }
export interface ChatMessage { type: number; from: string; guid: Guid; channel?: string; text: string; time_ms: number }

export interface State {
  level: number; xp: number; xp_max: number; health: number; health_max: number;
  power: number; power_max: number; power_type: number; money: number;
  position: { x: number; y: number; z: number; o: number; map: number };
  alive: boolean; ghost: boolean; in_combat: boolean; moving: boolean; target: Guid;
  casting?: { spell: number; ms_left: number };
  units: Unit[]; players: Unit[]; attackers: Unit[]; bags: BagItem[]; quests: QuestEntry[]; chat_pending: number;
}

export interface WaitResult<T> { timed_out: boolean; result: T }

export class RpcError extends Error {
  constructor(public code: number, message: string) { super(message); }
}

// Lua returns empty tables as {} rather than []; normalise list fields.
const list = <T>(v: unknown): T[] => (Array.isArray(v) ? v : []) as T[];

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
  constructor(private az: Azeroth, public readonly name: string) {}
  private call<T>(method: string, params: Record<string, unknown> = {}) {
    return this.az.call<T>(method, { char: this.name, ...params });
  }

  async state(radius = 40): Promise<State> {
    const s = await this.call<State>("state.get", { radius });
    for (const k of ["units", "players", "attackers", "bags", "quests"] as const) (s as any)[k] = list((s as any)[k]);
    return s;
  }
  /** Run Lua in the character's session (same environment as its scripts). */
  eval<T = unknown>(code: string) { return this.call<T>("script.eval", { code }); }

  /** Path to x, y (z defaults to the ground). Resolves false if there is no walkable path. */
  moveTo(x: number, y: number, z?: number) { return this.call<boolean>("move.to", { x, y, z }); }
  stop() { return this.call("move.stop"); }
  attack(guid: Guid) { return this.call("combat.attack", { guid }); }
  stopAttack() { return this.call("combat.stop"); }
  /** Cast on a target (default: yourself). */
  cast(spell: number, target?: Guid) { return this.call("spell.cast", { spell, target }); }
  lootAll(guid: Guid) { return this.call("loot.all", { guid }); }
  acceptQuest(npc: Guid, quest: number) { return this.call("quest.accept", { npc, quest }); }
  turnIn(npc: Guid, quest: number, reward = 0) { return this.call("quest.turnIn", { npc, quest, reward }); }
  release() { return this.call("death.release"); }
  reclaimCorpse() { return this.call("death.reclaim"); }

  say(text: string) { return this.call("chat.send", { kind: "say", text }); }
  party(text: string) { return this.call("chat.send", { kind: "party", text }); }
  whisper(to: string, text: string) { return this.call("chat.send", { kind: "whisper", to, text }); }
  channel(name: string, text: string) { return this.call("chat.send", { kind: "channel", to: name, text }); }
  async readChat(): Promise<ChatMessage[]> { return list(await this.call("chat.read")); }

  /** Resolves when another player messages you (or after timeoutS), with the drained messages. */
  async waitForChat(timeoutS = 60): Promise<WaitResult<ChatMessage[]>> {
    const r = await this.call<WaitResult<ChatMessage[]>>("wait.chat", { timeout_s: timeoutS });
    return { ...r, result: list(r.result) };
  }
  /** Resolves when the character stops moving, with its position. */
  waitIdle(timeoutS = 120) { return this.call<WaitResult<{ x: number; y: number; z: number }>>("wait.idle", { timeout_s: timeoutS }); }
}
