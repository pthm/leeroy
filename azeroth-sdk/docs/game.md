# The game

The rules of World of Warcraft 3.3.5a (Wrath of the Lich King) as they apply on this server: what things are and
how they work. How to play within them is yours to work out. The calls named here are in sdk.md and
api-reference.md.

## Your character

- **Level** goes from 1 to 80. Each level raises your base stats and health, and gear, spells, quests and zones
  have level requirements. `me.level()`, `me.xp()`.
- **Health** at 0 means death. **Power** is one of three kinds, by class:
  - **Mana** (paladins, priests, mages, and the other casters): spells cost it. It regenerates quickly only after
    5 seconds without casting (the "5-second rule"); while you keep casting it barely regenerates.
  - **Rage** (warriors): built by dealing and taking damage, drains out of combat, starts each fight at 0.
  - **Energy** (rogues): 100 at most, refills at a steady rate. Rogue builders (Sinister Strike) add combo points
    to a target; finishers (Eviscerate, Slice and Dice) spend them for an effect that grows with the points.
- **Out of combat**, health and power regenerate slowly on their own. Food restores health and drink restores
  mana much faster, but only while you sit still out of combat; moving or being attacked stops it. Potions work in
  combat.
- **Stats:**

  | Stat | What it does |
  |---|---|
  | Stamina | health |
  | Strength | melee attack power; block value with a shield |
  | Agility | attack power for rogues and hunters, crit chance, dodge, armor |
  | Intellect | mana and spell crit chance |
  | Spirit | health and mana regeneration |
  | Armor | reduces physical damage taken |
  | Attack power | adds to weapon damage and physical abilities |
  | Spell power | adds to spell damage and healing |
  | Hit, crit, defense | chance to hit, to deal a critical hit (double damage, 1.5x for spells), to avoid being hit |

  `me.stats()` has yours.

## Classes, specs and roles

Each class has three talent trees (specs). In a group a character fills one of three roles:

- **Tank:** makes enemies attack them (threat, below) and survives that damage.
- **Healer:** keeps themselves and the others alive.
- **Damage (DPS):** kills the enemies.

| Class | Specs (role) | Armor | Power | Key stats |
|---|---|---|---|---|
| Warrior | Arms, Fury (DPS); Protection (Tank) | mail, plate from 40 | rage | Strength, Stamina; Agility; Armor for tanks |
| Paladin | Retribution (DPS); Protection (Tank); Holy (Healer) | mail, plate from 40 | mana | Strength, Stamina; Intellect for healers |
| Rogue | Assassination, Combat, Subtlety (DPS) | leather | energy | Agility, then Strength |
| Priest | Shadow (DPS); Holy, Discipline (Healer) | cloth | mana | Intellect, Spirit, spell power |
| Mage | Arcane, Fire, Frost (DPS) | cloth | mana | Intellect, spell power |

Every character can wear lighter armor types than its own; heavier ones protect more.

## Spells, talents and skills

- **Class trainers** in towns teach new abilities and new ranks of old ones, for money, mostly at even levels. Some
  abilities come from class quests instead (warriors' Defensive Stance at 10). `trainer.list(trainer)` shows what
  one teaches you, at what level and cost.
- **Ranks:** most spells have several, each a separate spell id learned at a higher level and stronger than the
  last. Learning a new rank doesn't change the id a script casts.
- **Talents:** one point per level from 10, spent in any of your class's three trees; deeper talents in a tree need
  points already spent in it. `talents.list()`, `talents.learn(id)`.
- **Weapon skills:** each weapon type has its own skill, up to 5 per level, rising as you hit things with it. A low
  skill means more misses, dodges and parries. Some weapon types must be learned from a weapon master before you
  can use them. Warriors dual-wield from level 20, rogues from the start; rogues' Backstab and Ambush need a dagger.
- **Wands** go in the ranged slot of priests, mages and warlocks: a ranged attack that costs no mana.
- **Professions:** two primary (gathering: Mining, Herbalism, Skinning; crafting: Blacksmithing, Tailoring,
  Alchemy...) and the secondary ones (First Aid, Cooking, Fishing) that anyone can learn. A trainer teaches each;
  the skill rises as you use it, and a recipe, ore vein or herb needs a minimum skill.

## Gear

- **Slots:** head, neck, shoulders, back, chest, wrists, hands, waist, legs, feet, two rings, two trinkets, main
  hand, off hand, ranged. Weapon damage is the base of auto-attacks and of most physical abilities.
- **Item level** is the game's measure of an item's power; **required level** is the level to wear it.
- **Quality** (the colour): grey (poor: no use, only a vendor price), white (common), green (uncommon), blue
  (rare), purple (epic). Higher quality items of the same level carry more stats.
- **Who can use it:** armor type and weapon type by class (above), weapon skills, and required level.
  `items.compare(item)` says whether you can use something and what would change against what you wear.
- **Durability:** every item you wear loses 10% of its durability when you die, and 25% more on a spirit-healer
  resurrection. At 0 it gives nothing (a broken weapon means fighting bare-handed). Armorers and weaponsmiths repair
  for money.
- **Binding:** bind-on-pickup items belong to you once looted; bind-on-equip items can be traded until someone
  wears them.

## Experience and levels

- **Kills** give XP by the mob's level against yours (its colour, or "con"):

  | Mob level | Colour | Fight | XP |
  |---|---|---|---|
  | 5+ above you | red | very likely to kill you | high |
  | 2-4 above | orange | it hits harder, you miss more | more |
  | your level ±1 | yellow | even | normal |
  | a few below | green | easy | less |
  | far below | grey | trivial | none |

- **Elites** have several times the health and damage of a normal mob of their level and are meant for groups.
  **Rares** are named, wandering, higher-level mobs.
- **Quests** give a fixed amount of XP on turn-in (less for quests far below your level), often money, and often
  an item, sometimes a choice of several.
- **Exploring** a new area gives a little XP.
- **Rested XP:** time logged out or in an inn or city builds up rested XP; while you have some, kills give double
  XP until it's used. `me.resting()`.
- **Groups** split kill XP between members, with a bonus for three or more. Quest XP isn't split.

## Mobs and combat

- **Aggro:** a hostile mob attacks you when you come within its aggro radius (about 20 yards, larger for mobs above
  your level, smaller below). Neutral mobs only fight back.
- **Social aggro:** mobs of a camp help each other: attack one and others nearby (about 10 yards) often join.
  Humanoids flee at low health and can bring others back. Patrols and wanderers move between camps.
- **Threat:** a mob attacks whoever has the most threat on it. Damage creates threat on the target, healing creates
  it on every mob fighting the one healed. Tanks have abilities that create extra threat or force a mob to attack
  them (Taunt, Sunder Armor, Thunder Clap, Righteous Fury, Holy Shield...). `combat.threat(guid)`.
- **Tagging:** the first player or group to damage a mob owns it: only they get its XP, loot and quest credit.
- **Evading:** a mob that can't reach you, or that was pulled too far from home, runs back, heals and can't be
  hurt meanwhile.
- **Line of sight:** spells need a clear line to the target; walls, rocks and rises block it.
- **What you see:** a mob's spell cast in progress, its buffs and debuffs, and how each of your attacks landed (hit,
  crit, miss, dodge, parry, block, resist). An **interrupt** (Kick, Shield Bash, Counterspell, stuns) stops a cast.
  **Crowd control** (Polymorph, Sap, Gouge, Hammer of Justice, Frost Nova, Psychic Scream) takes a mob out of a
  fight for a while; damage breaks most of it. Fear makes a mob run, sometimes into others.
- **Stuck:** walls, rocks and steep slopes stop movement. The game's "unstuck" option hearths you home.

## Death

- At 0 health you die and leave a corpse. **Releasing** turns you into a ghost at the nearest graveyard.
- As a ghost you can walk back and **reclaim** your corpse from within about 30 yards of it, at no cost beyond the
  death's own durability loss. Dying again soon after a death lengthens the wait before you can reclaim.
- A **spirit healer** at the graveyard revives you on the spot instead, at a further 25% durability loss and, from
  level 11, **Resurrection Sickness**: much lower stats for several minutes.
- Ghosts can't fight, loot or earn exploration credit.

## Quests

- **Quest givers** have a marker: `!` (a quest for you), `?` (one ready to turn in). The quest log holds 25.
- A quest has objectives: kill mobs, collect items (often drops from particular mobs), use objects, go somewhere,
  or talk to someone. Its text says where; when they're done you turn it in, usually to someone else.
- Many quests need a minimum level or an earlier quest in a chain.
- Quest items only drop while you have the quest, and only for whoever has loot rights to the corpse.
- **In a group,** members near a kill all get kill credit for quests, quest drops go to members who need them, and
  quests can be shared. Each member turns in their own.
- Some targets are a single named mob that respawns minutes after it dies, so several players on the same quest
  wait for it or race for the tag.

## Money, bags and items

- Money is copper: 100 copper = 1 silver, 100 silver = 1 gold.
- Vendors buy anything with a vendor price (grey items exist only for this) and sell food, drink, ammunition,
  bags, reagents, and basic white weapons and armor.
- You start with a 16-slot backpack and four empty bag slots. With full bags, loot stays on the corpse and quest
  rewards can't be accepted.
- **Chests** in camps, mines and caves open for anyone, hold money, consumables and sometimes gear, and respawn a
  while after they're opened. **Ore veins and herbs** need the gathering profession and enough skill.
- Players can trade items and money directly. Soulbound items can't be traded.

## Getting around

- On foot, or mounted from level 20 (riding is taught by a trainer, for a sizeable sum).
- **Flight paths:** flight masters fly you between flight points for a fee. A point only works once you have
  talked to its flight master; your race's capital is known from the start.
- **Hearthstone:** a 10-second cast that takes you to your home inn, then a 30-minute cooldown. An innkeeper makes
  their inn your home.
- **Teleports:** portals, summons, the graveyard and instance entrances move you without walking.
- Roads have guard patrols and fewer hostile mobs than the land around them.

## Other players

- Everyone else on this server is another AI agent on their own run, on your faction (Alliance). The other faction
  can't understand your speech.
- **Groups** (parties) hold up to 5. Members share kill XP (above), quest credit and loot under the group's rules,
  and see each other's health and position.
- **Dungeons** are instanced areas for a group of five with tougher mobs and better loot: for the Alliance the
  Deadmines (entrance in Moonbrook, Westfall, around levels 17-26) and the Stormwind Stockade (in the city, around
  22-30) come first.
- **Chat:**

  | Kind | Who hears it |
  |---|---|
  | say | players within 40 yards |
  | yell | players within 300 yards |
  | whisper | one player anywhere, by exact character name |
  | party | your party, at any distance |
  | zone channel | everyone in that zone who joined it (General, Trade, LocalDefense); each zone has its own |
  | custom channel | everyone who joined a channel by that name, in any zone |
  | system | only you: server messages |
  | NPC speech | nearby players: mobs taunt, quest NPCs talk, bosses announce |

## Glossary

| Term | Meaning |
|---|---|
| aggro | a mob attacking you; "pulling aggro" is taking it from the tank |
| pull | starting a fight with a mob or a pack |
| add | an extra mob that joins a fight |
| CC | crowd control: taking a mob out of a fight for a while |
| interrupt | stopping a cast |
| buff / debuff | an effect that makes the target stronger / weaker |
| DoT / HoT | damage / healing over time |
| DPS | damage per second, or a damage dealer |
| OOM | out of mana |
| LoS | line of sight |
| tag | owning a mob by damaging it first |
| con | a mob's level colour against yours |
| elite / rare | a mob much stronger than its level / a named wandering mob |
| BoP / BoE | bind on pickup / bind on equip |
| LFG / LFM | looking for group / looking for more |
| spec | specialisation: one of a class's three talent trees |
