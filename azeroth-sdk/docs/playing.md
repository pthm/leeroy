# Playing World of Warcraft well

The game rewards preparation more than persistence. A character that fights with the right gear, the spells it
should have at its level, and a plan for each pull kills faster, dies less and earns more per hour than one that
grinds through the same mobs by trial and error. This page is the game knowledge behind that; how you turn it into
play is yours to work out.

## What makes your character strong

Your power comes from four things, and the first two matter most at low levels:

1. **Gear.** Every point of damage you deal with a weapon starts from the weapon's damage: auto-attacks and most
   physical abilities (Heroic Strike, Sinister Strike, Crusader Strike, Raptor Strike...) add to it. A level-1
   weapon at level 10 roughly halves your damage. Armor reduces the damage you take; stats add health, mana,
   attack power, crit.
2. **Spells and abilities.** Class trainers teach new abilities and higher ranks of old ones every even level.
   A rank-1 spell at level 12 does a fraction of the rank-3 version. Some class abilities come from class quests
   (warriors get Defensive Stance at 10, for example).
3. **Talents.** One point per level from 10, in your spec's tree.
4. **Level.** Each level adds stats, and you need levels to wear better gear and learn better spells.

`me.status()` grades the first three: `gear` (a 0-100 score of your gear against your level, the weakest slots,
empty and broken ones), `spellbook` (the class spells you could know at your level and which are missing) and
`unspent_talents`.

## Gear

- **Item level** is the game's own measure of how strong an item is. Gear near your level is what the game expects
  you to wear; gear far below it means you're fighting at a disadvantage. `me.gear()` shows how far behind you are.
- **Quality** (the colour): grey (poor, junk to sell), white (common), green (uncommon: better stats), blue (rare),
  purple (epic). A green item at your level usually beats a white one, but a better colour isn't always better for
  you: check the armor type, whether your class can use the weapon, and whether its stats are ones you need.
- **Where to get it, early on:** quest rewards are the best source below level 20, and many quests let you choose
  (`azworld quest <id>` lists the choices). Weaponsmiths and armorers sell white weapons and armor for a few silver
  that are far better than starting gear. Mobs drop greens now and then. `azworld upgrades --class <yours> --level
  <yours>` lists what you could use and where it comes from; `items.compare(item)` tells you if something beats
  what you wear.
- **Armor types:** cloth (mages, priests, warlocks), leather (rogues, druids), mail (hunters, shamans), plate
  (warriors, paladins from level 40; mail before). You can wear lighter types; heavier types protect more.
- **Weapons:** physical classes want the highest damage-per-second weapon they can use (two-handers hit hardest,
  dual wielding comes at level 20 for warriors and is there from the start for rogues; rogues' Backstab and
  Ambush need a dagger). Casters care about the stats on a staff or wand, not its damage, and a wand in the ranged
  slot is a cheap, mana-free way to finish mobs.
- **Durability:** each death costs 10% durability on everything you wear, and a spirit-healer resurrection 25% more.
  At 0 an item does nothing. Repair at any armorer or weaponsmith (`vendor.repair`).

## Experience

- **Kills and quests.** Kill XP depends on the mob's level against yours (the table under Combat): grey mobs give
  nothing, green less, yellow normal, orange more. Quests give large chunks of XP for one turn-in, and often an
  item; a few quests in the same area usually beat grinding the same mobs.
- **Rested XP.** Time spent in an inn or a city builds up "rested" XP (`me.resting()` says when you're in a resting
  area). While you have it, kills give double XP until it's used up. It builds slowly, so it's a bonus, not a
  strategy, but it means a stop in town to train and repair isn't entirely lost time.
- **Talents.** A point per level from 10. Putting most points into one tree beats spreading them out: a specialist
  is stronger than someone mediocre at everything. Your spec's tree is the one to fill.

## Mana, rage and energy

- **Mana and the 5-second rule.** Mana regenerates fast only after 5 seconds without casting. Casting constantly
  keeps you at the slow rate, so a caster who chain-casts runs dry. Drinking after a fight restores mana far faster
  than standing around. When you run out mid-fight, a wand or melee finishes a nearly dead mob without mana.
- **Rage** (warriors) comes from dealing and taking damage and drains out of combat; it starts at zero each fight,
  so openers that build it (Charge, Bloodrage) matter.
- **Energy** (rogues) refills steadily; combo points from builders (Sinister Strike) feed finishers (Eviscerate,
  Slice and Dice).

## Spells, skills and items

- **Train every time you can.** Visit your class trainer every two levels (`me.spellbook()` lists what's missing;
  `azworld npc <trainer>` lists spells, levels and costs). Training is cheap compared with what it adds.
- **Ranks.** Most spells have ranks, each a separate spell id, learned at higher levels. Learning a new rank doesn't
  change what an old script casts: a script that casts the rank-1 id keeps casting rank 1. Cast the highest rank
  you know (`act.best({rank ids...})`, `spells.known`).
- **Weapon skill.** Each weapon type has its own skill, which rises as you hit things with it. With a low skill
  you miss, and get dodged and parried, far more. A new weapon type starts low: it pays to keep using the same
  types. Some weapon types have to be learned first from a weapon master (in Stormwind; `azworld near x y --kind
  trainer`); `items.info` says when you lack the skill (`why_not`).
- **Use what you have.** Classes have openers, finishers, crowd control, defensive cooldowns and buffs; a fight
  that uses only one ability is usually a long fight. `combat.meter()` and the `fight_end` event show which
  abilities you actually used.
- **Professions that keep you alive:** First Aid (bandages: healing that costs no mana) and Cooking (food that
  restores more) are trained in cities and use cloth and meat you loot anyway.
- **Consumables:** food restores health and drink restores mana far faster than waiting, out of combat; potions
  heal in combat. Buy them from innkeepers and general goods vendors.

## Getting around

- **Your hearthstone** (item 6948) takes you home: a 10-second cast, then a 1-hour cooldown. Home is the inn you
  last bound at (`me.home()`); talk to an innkeeper to make their inn your home (`innkeeper.bind`). An inn near where
  you're questing turns a long walk back to town (to train, repair and sell) into ten seconds.
- **Flight paths:** talk to every flight master you pass, even if you don't need to fly now; a flight point only
  works once you've discovered it, and later trips get much shorter (`flights.map`).
- **Roads are safer than straight lines:** fewer mobs, and you notice what's ahead. Watch the level of mobs near
  the road before you leave it.
- **Coming back from death:** your corpse can be reclaimed within about 30 yards of it, so you can come back to
  life on the side away from whatever killed you. A spirit healer revives you at the graveyard instead, at the cost
  of durability and, from level 11, Resurrection Sickness (much weaker for a while: don't fight until it wears off).

## Quests

- Quest givers offer quests (`quest_giver` role); the quest log holds 25. When it fills up, drop the quests you
  won't do (`quests.abandon`): grey quests (far below your level) give little XP.
- Read what a quest asks (`quests.log()` has its summary and objectives; `azworld quest <id>` has the details and
  where its targets are) and what your spells do (`azworld spell <id>`): both often tell you how to do it well.
- Quests that send you to the same area can be done together in one trip; turning several in at once saves the
  walk.
- When a quest is too hard alone, ask for help: other players are on the same quests.

## Money, bags and loot

- **Sell your junk.** Grey items exist only to be sold; picking up everything a mob drops and selling it at the
  next vendor is a large share of a low-level character's money. Whites are sometimes crafting or quest materials;
  greens and better are worth more, and might be an upgrade for someone else.
- **Bags.** You start with a 16-slot backpack, and a full bag means loot left on corpses and refused quest rewards.
  Extra bags (general goods vendors sell small ones) are one of the best early buys.
- **Binding.** Bind-on-pickup items are yours alone the moment you loot them; bind-on-equip items can be traded
  until someone wears them (`trade.offer`). An upgrade you can't use might be one for another player.
- **What money is for** at these levels: training (every even level), repairs, bags, food and drink, flights, and
  a few white weapons and armor pieces when quests haven't given you better. Riding becomes available at level 20.
  Sell what you don't need in town and keep enough for your next training visit.
- **Chests and gathering.** Not every chest is a quest object: treasure chests in camps, mines and caves open for
  anyone, hold money, food and sometimes uncommon gear, and come back a while after they're opened. The gathering
  professions (Mining, Herbalism, Skinning; taught by trainers in towns) turn ore veins, herbs and beast corpses
  into materials that sell or that crafters need, and their skill rises as you use it: a node that needs more skill
  than you have can't be gathered yet. `me.status()` lists the chests, nodes and quest objects near you, and
  `objects.nearby` says what each one takes (`gather`, `skill`, `can_use`, `key`), whether it's in sight, and how far
  the walk is: a chest in a mine under you can be close in a straight line and a long walk round.

## Combat

**Choosing targets.** A mob's level against yours decides how hard it is and how much XP it gives:

| Mob level | Difficulty | XP |
|---|---|---|
| 5+ above you (red) | very likely to kill you | high, if you survive |
| 2-4 above (orange) | hard: it hits harder, you miss more | more |
| your level ±1 (yellow) | the normal fight | normal |
| a few below (green) | easy | less |
| far below (grey) | trivial | none |

Elites (much more health) are meant for groups. Rare named mobs (higher level, wandering) are dangerous to anyone
near their level.

**Aggro and threat.** A mob attacks whoever it has the most threat on. Damage and healing both create threat
(healing creates it on every mob fighting the target you heal), so a healer or a high-damage player can pull a mob
off the tank. Tanks hold threat with abilities made for it (Taunt, Sunder Armor, Thunder Clap, Righteous Fury,
Holy Shield...). Without a tank, the player taking the hits is the one doing the most damage or healing.

**Pulls.** Most deaths start with a bad pull:

- Mobs notice you within an aggro radius (about 20 yards, larger for mobs above your level, smaller below).
  Walking through a camp pulls everything in it.
- Mobs help each other: attack one and others of its kind nearby join (social aggro). Look at what stands around
  a target (`units.nearby`) before you pull.
- Pull one at a time from the edge of a group, with a ranged attack (a thrown weapon, a gun or bow, a spell) so the
  mob comes to you, away from its friends. Casters can pull around a corner so the mob runs to them (line of sight).
- Humanoids flee at low health and bring friends back: finish them, stun them or snare them.
- Patrols and wandering mobs join fights late: watch for them.
- Start every pull with full health (and mana): rest or eat first.

**Reading a fight.** The client shows what the game shows a player: what an enemy is casting (`casting` on a unit,
and a `unit_cast_start` event when it casts at you or your group: interrupt or stun it), its debuffs and yours
(`units.auras`, with what can dispel them), the game's aura states (a target under 20% health is in Execute range;
`defense` means you just dodged, parried or blocked), combo points, and how each of your attacks landed (dodge,
parry, miss, resist: `fight_end.outcomes`). A mob that is `evading` can't be hurt: it's resetting or unreachable,
so leave it.

**Getting stuck.** Walls, rocks and steep slopes stop you; side-stepping and jumping often frees you. If nothing
works, the game's "Character stuck" option (`move.unstuck`, or `act.go_to{..., unstuck = true}` as a last resort) hearths you home.

**Crowd control and adds.** When a second mob joins (an add), take it out of the fight (Polymorph, Sap, Gouge,
Hammer of Justice, Frost Nova, Psychic Scream) or fight it with defensive abilities ready, rather than trading hits
with both.

**Where you fight.** The graveyard and the corpse run matter: dying in a camp near no graveyard means a long walk
back into the same camp. Your corpse can be reclaimed from about 30 yards away, so come back from a safe side.

## Groups

Roles in a group:

- **Tank:** gets the mobs to attack them and keeps them there. Pulls what the group can handle, one pack at a
  time ("slow is smooth, smooth is fast"), and watches the healer's mana: a healer out of mana means the group dies.
- **Healer:** keeps everyone alive, the tank first. Says when the tank pulls too much and when they're out of mana.
- **Damage:** lets the tank build threat for a few seconds before attacking, attacks the same target as the other
  damage dealers so it dies fast, interrupts casters, uses crowd control on extra mobs (before the pull when it can),
  and stays out of the way of what hits hard. The difference between a mediocre and a good damage dealer is
  usually how little damage they take and how rarely they pull aggro.
- **Only the tank pulls.** A damage dealer or healer who runs ahead and attacks starts fights the group isn't ready
  for. Fear in a group fight is risky: a feared mob runs into others and brings them.

To find a group, ask in the zone's General channel ("LF tank and healer for Deadmines", LF = looking for). Dungeons
are instanced areas for five players with tougher mobs and better loot; the first Alliance ones are the Deadmines
(entrance in Moonbrook, Westfall, around levels 17-26) and the Stormwind Stockade (in the city, around 22-30).

A group kills faster and survives harder fights: a tank holds the mobs, a healer keeps everyone up, damage dealers
kill. But kill XP is split between members (with a bonus for three or more), so a group only pays off when it
takes on what you couldn't alone, like elites, dense camps or higher-level areas, or when it saves you deaths. A
group that grinds the same easy mobs each of you could solo just splits the XP. Quests can be shared, and quest
kills count for everyone nearby.

## Getting better

Treat each fight as data. `fight_end` events tell you how long a fight took, what you dealt and took, which
abilities you used and how it ended; `combat.meter()` compares you with others; `standings` and `party` show your
rate against everyone's. When a fight goes badly, the cause is usually one of: a target too high for you, an add
you didn't plan for, weak gear, untrained spells, or starting the fight hurt. Fix the cause, not the symptom.

## Glossary

| Term | Meaning |
|---|---|
| aggro | a mob attacking you; "pulling aggro" is taking it from the tank |
| pull | starting a fight with a mob or a pack |
| add | an extra mob that joins a fight |
| CC | crowd control: taking a mob out of a fight for a while (Polymorph, Sap, stuns, Frost Nova) |
| interrupt | stopping a mob's spell cast (Kick, Shield Bash, Counterspell, stuns) |
| buff / debuff | an effect that makes the target stronger / weaker |
| DoT / HoT | damage / healing over time |
| DPS | damage per second, or a damage dealer |
| OOM | out of mana |
| LoS | line of sight: pulling a caster mob around a corner so it has to come to you |
| tag | the first player to damage a mob owns its XP and loot |
| con | a mob's level colour against yours (grey, green, yellow, orange, red) |
| elite | a mob much stronger than its level, meant for groups |
| rare | a named, often higher-level mob that wanders |
| BoP / BoE | bind on pickup / bind on equip |
| LFG / LFM | looking for group / looking for more |
| spec | specialisation: the talent tree you fill |

