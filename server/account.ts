// bun server/account.ts <user> <pass> [gmLevel]  — create/reset an account in the auth DB (via the ac-database container).
// gmLevel 3 lets the bot use GM dot-commands (.levelup, .additem, .tele) — handy for scenario setup, unfair in a benchmark.
import { createHash, randomBytes } from "node:crypto";
import { execFileSync } from "node:child_process";

const N = BigInt("0x894B645E89E1535BBDAD5B8B290650530801B18EBFBF5E8FAB3C82872A3E9BB7");
const le = (b: Uint8Array) => b.reduceRight((n, x) => (n << 8n) | BigInt(x), 0n);
const toLe = (n: bigint, len: number) => { const b = Buffer.alloc(len); for (let i = 0; i < len; i++, n >>= 8n) b[i] = Number(n & 0xffn); return b; };
const modPow = (b: bigint, e: bigint, m: bigint) => { let r = 1n; for (b %= m; e > 0n; e >>= 1n, b = b * b % m) if (e & 1n) r = r * b % m; return r; };
const sha1 = (...p: Uint8Array[]) => createHash("sha1").update(Buffer.concat(p)).digest();

const [userArg, pass, gm = "0"] = process.argv.slice(2);
if (!userArg || !pass) throw new Error("usage: bun server/account.ts <user> <pass> [gmLevel]");
const user = userArg.toUpperCase();
if (!/^[A-Z0-9]{1,16}$/.test(user) || !/^\d$/.test(gm)) throw new Error("account must be alphanumeric <= 16 chars; gmLevel 0-9");

const salt = randomBytes(32);
const verifier = toLe(modPow(7n, le(sha1(salt, sha1(Buffer.from(`${user}:${pass.toUpperCase()}`)))), N), 32);
const sql = `INSERT INTO account (username, salt, verifier, expansion) VALUES ('${user}', 0x${salt.toString("hex")}, 0x${verifier.toString("hex")}, 2)
  ON DUPLICATE KEY UPDATE salt = VALUES(salt), verifier = VALUES(verifier);
DELETE FROM account_access WHERE id = (SELECT id FROM account WHERE username = '${user}');
${gm !== "0" ? `INSERT INTO account_access (id, gmlevel, RealmID) SELECT id, ${gm}, -1 FROM account WHERE username = '${user}';` : ""}`;
execFileSync("docker", ["exec", "-i", "ac-database", "mysql", "-uroot", "-ppassword", "acore_auth"], { input: sql, stdio: ["pipe", "inherit", "ignore"] });
console.log(`account ${user} ready (gm ${gm})`);
