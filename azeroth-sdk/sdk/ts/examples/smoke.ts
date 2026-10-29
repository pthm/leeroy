// bun examples/smoke.ts <character>: status, a short walk, chat, and the event stream.
import { Azeroth } from "../src/index.ts";

const az = new Azeroth((globalThis as any).process?.env.AZSDK_URL);
const me = az.character((globalThis as any).process?.argv[2] ?? "Garrick");
console.log("sessions", await az.sessions());
const s = await me.api.me.status();
console.log("status", { level: s.level, hp: s.health, where: s.location, mobs: s.mobs_nearby, quests: s.quests.map((q) => q.title) });
const walk = await me.await(await me.api.move.to(s.position.x + 15, s.position.y, s.position.z));
console.log("walk", walk.ok ? walk.data : walk.error);
await me.api.chat.say("hello from the azeroth-sdk smoke test");
console.log("recent", (await me.query({ last: 5 })).map((e) => e.type));
