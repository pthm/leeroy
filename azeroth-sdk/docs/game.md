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
  - **Rage** (warriors): built by dealing and taking damage; out of combat it drains at 1 a second. <!-- Player::Regenerate POWER_RAGE -->
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
  | 3-4 above | orange | it hits harder, you miss more | more |
  | your level ±2 | yellow | even | normal |
  | a few below | green | easy | less |
  | far below | grey | trivial | none |

  The grey level is 0 up to level 5; your level − 5 − (your level / 10, rounded down) from 6 to 39 (4 at level 10,
  13 at 20, 22 at 30); your level − 1 − (your level / 5) from 40 to 59; your level − 9 from 60. A mob at or below it
  is grey.
- **Kill XP** in the level 1-60 zones, with L your level: a mob at or above your level gives (5L + 45) × (20 + d) / 20,
  where d is how many levels above you it is, at most 4 (95 XP for a level 10 mob at level 10). A mob below you gives
  (5L + 45) × (Z − levels below) / Z, where Z is 5 below level 8, 6 at 8-9, 7 at 10-11, 8 at 12-15, 9 at 16-19, 11 at
  20-29 and 12 at 30-39. Elites give double. A creature's `ExperienceModifier` in `creature_template` multiplies it,
  and one with a `HealthModifier` of 0.75 or less gives that fraction. Critters, totems and pets give none.
  <!-- Formulas.h GetGrayLevel, GetColorCode, GetZeroDifference; Formulas.cpp BaseGain, Gain; KillRewarder::_InitXP -->
- **Who gets it:** a creature gives XP, loot and quest credit only if players (and their pets) did at least half its
  health in damage; otherwise nobody does. The first player whose damage or hostile spell hits it **tags** it
  (`tapped_by_other` for anyone else): only that player, or that player's group, is rewarded. A creature that evades
  loses its tag. <!-- Unit::DealDamage SetLootRecipient; Unit::AtTargetAttacked (3.0.8 tap on aggressive spell); Creature::IsDamageEnoughForLootingAndReward, ResetPlayerDamageReq (health / 2); CreatureAI::_EnterEvadeMode -->
- **Elites** have several times the health and damage of a normal mob of their level and are meant for groups.
  **Rares** are named, wandering, higher-level mobs.
- **Quests** give a fixed amount of XP on turn-in (less for quests far below your level), often money, and often
  an item, sometimes a choice of several.
- **Exploring** a new area gives a little XP.
- **Rested XP:** time logged out or in an inn or city builds up rested XP; while you have some, kills give double
  XP until it's used. `me.resting()`.
- **Groups:** a kill rewards the members within 74 yards of it (a dead member: of their corpse; inside a dungeon, the
  whole instance). The XP is worked out for the highest-level living member to whom the mob isn't grey, multiplied
  by 1.166 with three living members in range, 1.3 with four and 1.4 with five, and shared in proportion to level:
  each living member gets that × their level / the sum of the living members' levels. Dead members get no XP. A
  member above that highest level gets none, and when the mob is grey to any living member in range, the others
  get half. Quest kill credit goes to every member in range who is alive or hasn't released. Quest XP isn't split.
  <!-- KillRewarder::_InitGroupData, _RewardXP, _RewardKillCredit; Formulas.h xp_in_group_rate; Player::IsAtGroupRewardDistance; worldserver.conf MaxGroupXPDistance = 74 -->

## Mobs and combat

- **Aggro:** a hostile mob attacks you when you're within its aggro radius, in its line of sight, and no more than
  3 yards above or below it. The radius is the creature's detection range (`detection_range` in `creature_template`:
  18 yards for most creatures, 20 or 10 for others), plus a yard for each level it has above you or minus a yard for
  each level below, at least 5 and at most 45 yards. Neutral mobs only fight back, and a creature doesn't start a
  fight in the 5 seconds after it respawns. Patrols and wanderers move between camps.
  <!-- Creature::GetAggroRange (MAX_AGGRO_RADIUS 45), Creature::CanStartAttack (CREATURE_Z_ATTACK_RANGE 3), Creature::CanCreatureAttack (5 s after respawn) -->
- **Assistance:** when a creature starts attacking, creatures within 10 yards of it (centre to centre) that have the
  same faction, are idle, aggressive, not evading, in its line of sight and able to see the enemy join it 2 seconds
  later; it calls again every 3 seconds while it fights. <!-- Unit::Attack -> Creature::CallAssistance; AnyAssistCreatureInRangeCheck; Creature::CanAssistTo; worldserver.conf CreatureFamilyAssistanceRadius 10, AssistanceDelay 2000, AssistancePeriod 3000 -->
- **Formations:** creatures linked in a formation (`creature_formations` in the world database, by spawn guid) enter
  combat together: when one of them engages a target, the others engage the same target wherever they stand, as
  the formation's `groupAI` flags set (0x1: the members join their leader, 0x2: the leader and members join any
  member; 0x4: they evade together). <!-- CreatureGroup::MemberEngagingTarget, MemberEvaded; CreatureGroups.h GroupAIFlags -->
- **Scripts:** a creature's script (`smart_scripts`, and the `casts` of `azworld npc`) can add more: casting spells,
  calling for help over its own radius, or fleeing. Most fleeing creatures in this database flee below 15% health:
  a fleeing creature runs to the nearest idle creature of its faction within 30 yards and in sight, which, with any
  others of its faction within 10 yards of it, joins the fight; with none to run to it runs from its target for 7
  seconds. A stunned, feared or otherwise controlled creature doesn't flee. `fleeing` on a unit is the server's
  flag. <!-- SmartScript SMART_ACTION_FLEE_FOR_ASSIST (event HEALTH_PCT 0-15 in 733 of 774 scripts), SMART_ACTION_CALL_FOR_HELP; Creature::DoFleeToGetAssistance; AssistanceMovementGenerator::Finalize; worldserver.conf CreatureFamilyFleeAssistanceRadius 30, CreatureFamilyFleeDelay 7000 -->
- **Threat:** each creature keeps its own threat list and attacks whoever is highest on it. Damage adds threat equal
  to the damage, scaled by the attacker's threat modifiers (stances, auras, some spells' own bonus). Healing adds half
  the health actually restored (a quarter for paladins), split evenly between the creatures that have the healed
  unit on their threat list; stunned, polymorphed (confused) and fleeing creatures get none of it. A buff on a unit
  that creatures are fighting puts its caster on their threat lists. A creature changes target only when someone
  passes 110% of its current target's threat while in melee range of it, or 130% from farther away. A taunt makes
  the creature attack the taunter while the taunt lasts and raises the taunter's threat to the highest on its list.
  `combat.threat(guid)`. <!-- ThreatManager::ReselectVictim (1.1, 1.3), ForwardThreatForAssistingMe (UNIT_STATE_CONTROLLED excluded), CompareReferencesLT (taunt precedence); Spell::DoAllEffectOnTarget heal threat 0.5, paladin 0.5; Spell::EffectTaunt MatchUnitThreatToHighestThreat; SpellEffects ForwardThreatForAssistingMe(caster, 0) for buffs -->
- **Evading:** an evading creature runs back to where it started, drops its threat list and its tag, loses its auras,
  can't be hurt (`evade`) and doesn't attack until it's back; out of combat it regains a third of its health every 2
  seconds. Outdoors a creature evades when it is more than 30 yards from where it started and hasn't taken direct
  damage, a hostile spell or a taunt for 11 seconds (a second more per 10 levels from level 30: 17 at level 80).
  Inside dungeons there is no such limit. A creature that can't reach its target starts a 10-second timer and
  regenerates health for the last 5 of them; then it evades, or, with others on its threat list, drops the
  unreachable one.
  <!-- CreatureAI::_EnterEvadeMode; Creature::CanCreatureAttack (CreatureLeashRadius 30; IsDungeon), GetLeashTimer, UpdateLeashExtensionTime callers (Unit::DealDamage non-DoT, Unit::AtTargetAttacked, lost control); Creature::RegenerateHealth (maxValue / 3, CREATURE_REGEN_INTERVAL 2 s); CombatManager EVADE_TIMER_DURATION 10 s, EVADE_REGEN_DELAY 5 s; UnitAI::EvadeTimerExpired -->
- **Line of sight:** the server checks it for spells on any other unit, friendly or hostile, when the cast starts:
  a line from the caster's eyes to the target's (each at its model's height). Buildings, caves, tunnels and other
  large world models block it, and so do game objects such as doors; trees, fences and other small doodads don't,
  and neither does open terrain. <!-- Spell::CheckCast, WorldObject::IsWithinLOSInMap, Map::isInLineOfSight (ModelIgnoreFlags::M2; CheckGameObjectLoS = 1) -->
- **Range:** melee attacks reach a target whose centre is within the attacker's and target's combat reach added
  together plus 1⅓ yards, and at least 5 yards (5 yards between a player and a creature of normal size; large
  creatures reach farther), with 2.66 yards more while both are running. A spell reaches its range plus both combat
  reaches, checked when the cast starts and, for a spell that isn't melee-range, again when the cast finishes, with
  10% (at most 3 yards) to spare. A ranged spell's minimum range is its own minimum plus the melee reach. <!-- Unit::GetMeleeRange (NOMINAL_MELEE_RANGE 5, 4/3), WorldObject::GetLeewayBonusRange (LEEWAY_BONUS_RANGE 2.66), Unit::IsWithinCombatRange, Spell::CheckRange -->
- **Facing:** spells flagged for it in Spell.dbc (melee attacks, bolts) need the target within 90 degrees either side
  of where the caster faces; heals and buffs don't (fighting.md). <!-- Spell::CheckRange SPELL_FACING_FLAG_INFRONT -->
- **What you see:** a mob's spell cast in progress, its buffs and debuffs, and how each of your attacks landed (hit,
  crit, miss, dodge, parry, block, resist). An **interrupt** (Kick, Shield Bash, Counterspell, stuns) stops a cast.
- **Crowd control** (Polymorph, Sap, Gouge, Hammer of Justice, Frost Nova, Psychic Scream) takes a mob out of a
  fight for a while. An aura that breaks on damage (`breaks_on_damage` in a unit's `control`) ends at the first
  damage; a fear, root, stun, confuse or transform from a spell with proc flags ends once the damage taken passes a
  cap set by the caster's level (`damage_limit`). Fear makes a mob run, sometimes into others. A polymorphed creature
  regains a third of its health every 2 seconds, in combat too, and other creatures can still heal or buff a
  controlled one. **Diminishing returns:** a second stun within 15 seconds of the last one ending lasts half as long,
  a third a quarter, and a fourth doesn't land; this applies to creatures for stuns, charges and Cyclone only, and
  to players for the other control categories too (polymorph, fear, root...).
  <!-- AuraInterruptFlags TAKE_DAMAGE; SpellAuraEffects.cpp CalculateAmount (CONFUSE/FEAR/STUN/ROOT/TRANSFORM with ProcFlags: BaseHealth / 4.75 of caster level) and HandleBreakableCCAuraProc; Creature::Update (!IsInCombat() || IsPolymorphed()) -> RegenerateHealth; Unit::GetDiminishing (15000 ms), ApplyDiminishingToDuration (0.5, 0.25, immune), SpellMgr GetDiminishingReturnsGroupType (DRTYPE_ALL: stun, controlled/opening stun, cyclone, charge, taunt; taunt only for flagged creatures) -->
- **Stuck:** walls, rocks and steep slopes stop movement. The game's "unstuck" option hearths you home.

## Death

- At 0 health you die and leave a corpse. **Releasing** turns you into a ghost at the nearest graveyard.
- As a ghost you can walk back and **reclaim** your corpse from within 39 yards of it (counted from the edges of you
  and the corpse: about 40.9 yards between your centres), at no cost beyond the death's own durability loss; you come
  back with half your health and mana. The wait before you can reclaim starts when you release: 30 seconds, 60
  after a second death within 5 minutes, 120 after a third (the most). Each 5 minutes without a death takes it down
  a step. <!-- Corpse.h CORPSE_RECLAIM_RADIUS 39; MiscHandler.cpp HandleReclaimCorpseOpcode (ResurrectPlayer 0.5); Player.cpp copseReclaimDelay {30, 60, 120}, GetCorpseReclaimDelay; PlayerUpdates.cpp UpdateCorpseReclaimDelay (DEATH_EXPIRE_STEP 5 min, MAX_DEATH_COUNT 3); WorldObject::_IsWithinDist adds both object sizes. Checked live 2026-10-31: reclaim ok at 40.6 yd, refused at 41.4; waits 30, 60, 120 s on three deaths; 113 of 227 health on reclaim -->
- A **spirit healer** at the graveyard revives you on the spot instead, at a further 25% durability loss and, from
  level 11, **Resurrection Sickness**: 75% less of every attribute and of the damage you deal, for 1 minute at level
  11, a minute more per level, and 10 minutes from level 20. <!-- Player::ResurrectPlayer (Death.SicknessLevel 11; spell 15007); DurabilityLoss.OnSpiritResurrect 25 -->
- Another player's resurrection spell brings you back where your body lies, dead or as a ghost
  (`death.accept_res()`).
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
- **Dungeons** are instanced areas for a group of up to five, with elite mobs and bosses. The Deadmines' entrance is in
  Moonbrook, Westfall, and needs level 10; the Stormwind Stockade's is in the city and needs level 15
  (`dungeon_access_template` in the world database). See "Dungeons and instances" below.
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

## Dungeons and instances

- Each group gets its own copy (**instance**) of a dungeon; its members enter that copy, and a character who leaves
  the group is no longer bound to it. An account can enter at most 5 different instances an hour
  (`too_many_instances`). <!-- PlayerMisc.cpp ResetInstances INSTANCE_RESET_GROUP_LEAVE; worldserver.conf AccountInstancesPerHour = 5 -->
- An instance keeps its state while it exists: killed creatures stay dead until their own respawn time passes (in
  the Deadmines, 24 hours for every creature: `spawntimesecs` in the `creature` table), and opened doors and
  finished bosses stay that way. A creature's respawn time counts from when its corpse disappears. An instance
  nobody is in unloads after 30 minutes and is loaded again, with its state, when someone enters. A normal
  dungeon's instance expires 3 days after it was created. The group leader (or a player alone) can reset their
  instances while nobody is inside them (`client.call("ResetInstances")`); a reset one starts again from scratch.
  <!-- Map::SaveCreatureRespawnTime; Creature respawn (m_respawnTime = now + respawnDelay + corpseDelay); InstanceMap::AfterPlayerUnlinkFromMap (Instance.UnloadDelay 1800000); InstanceSaveMgr::AddInstanceSave (normals expire after 3 days); WorldSession::HandleResetInstancesOpcode, InstanceMap::Reset (fails while players are inside) -->
- Inside a dungeon there's no leash (Mobs and combat, "Evading"), and every group member in the instance is in
  range for XP, loot and quest credit.
- **Dying in a dungeon:** releasing sends your ghost to the graveyard outside. A ghost walking into the entrance of
  the instance its corpse is in comes back to life just inside, with half its health and mana. When the instance
  can't be entered (full, a boss fight in progress, too many instances), the ghost comes back to life at the
  entrance outside instead. <!-- Graveyard::GetClosestGraveyard (graveyard on the entrance map, nearest the entrance); MovementHandler.cpp HandleMoveWorldportAck (ResurrectPlayer 0.5 on entering the corpse's dungeon map); MiscHandler.cpp HandleAreaTriggerOpcode reviveAtTrigger -->

## Group loot

- Corpses of creatures your group tagged are looted under the group's **loot method** (`group.set_loot`, the leader
  only; a new group starts with group loot and an uncommon threshold): free for all (any member takes anything),
  round robin (each corpse goes to the next member in turn), master loot (the master looter hands out items at or
  above the threshold), group loot and need before greed (items at or above the threshold are rolled for). Under
  every method but free for all, items below the threshold go to the round-robin member. Only members within 74
  yards of the corpse (anywhere in a dungeon instance) take part. <!-- Group.cpp (m_lootMethod = GROUP_LOOT, m_lootThreshold = ITEM_QUALITY_UNCOMMON), GroupLoot, NeedBeforeGreed, MasterLoot, UpdateLooterGuid; Player::IsAtLootRewardDistance -->
- **Rolls** last 60 seconds. Need beats greed and disenchant; among the needers the highest roll from 1 to 100 wins,
  and if nobody needs, the highest greed or disenchant roll wins. Under need before greed you can only need an item
  your character can use; disenchant needs enough Enchanting skill. An item won with full bags is mailed to the
  winner. <!-- Group::CountTheRoll (urand 1-100, NEED first, GREED and DISENCHANT together), SendLootStartRoll 60000, CanRollForItemInLFG, SendRollWonItemViaMail -->
- A normal creature's corpse stays 60 seconds (elites and rares 5 minutes); once it's fully looted it keeps half
  the time it had left. <!-- worldserver.conf Corpse.Decay.*; Creature::AllLootRemovedFromCorpse (Rate.Corpse.Decay.Looted 0.5) -->

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
