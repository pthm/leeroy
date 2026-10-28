// bun examples/smoke.ts <character>: state, a short walk, chat, and a whisper wait.
import { Azeroth } from "../src/index.ts";

const az = new Azeroth(process.env.AZSDK_URL);
const name = process.argv[2] ?? "Garrick";
console.log("sessions", await az.sessions());
const me = az.character(name);
const s = await me.state();
console.log("state", { level: s.level, hp: `${s.health}/${s.health_max}`, pos: s.position, units: s.units.length, bags: s.bags.length });
const { x, y } = s.position;
console.log("moveTo", await me.moveTo(x + 15, y));
console.log("idle", await me.waitIdle(30));
await me.say("hello from the azeroth-sdk smoke test");
console.log("chat", await me.readChat());
