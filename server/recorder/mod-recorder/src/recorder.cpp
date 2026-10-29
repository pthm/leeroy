// mod-recorder: writes one JSON line per player event to a file, for replaying runs (see replay.ts).
//   pos     every RECORDER_INTERVAL_MS (default 1000) per online player: position, health, power, level, state
//   chat, dmg, heal, cast, kill, death, pvp, release, resurrect, level, xp, money, quest, achievement, loot,
//   zone, spell (learned), login, logout, group (party changes: grp, leader, members; grp 0 = left)
// File: $RECORDER_FILE (default /azerothcore/env/dist/logs/recorder.jsonl, the host's server/azerothcore/env/dist/logs).
// Units are {"id": entry (creature) or guid counter (player), "n": name, "pl": 1 if a player}.

#include "Channel.h"
#include "Creature.h"
#include "DBCStructure.h"
#include "Group.h"
#include "Item.h"
#include "ObjectAccessor.h"
#include "Player.h"
#include "QuestDef.h"
#include "ScriptMgr.h"
#include "Spell.h"
#include "SpellInfo.h"
#include <chrono>
#include <cstdlib>
#include <fstream>
#include <mutex>
#include <sstream>
#include <unordered_map>

namespace
{
std::mutex mu;
std::ofstream out;
std::unordered_map<uint32, uint32> sinceSample; // player guid counter -> ms since last pos sample
uint32 intervalMs = 1000;

std::string esc(std::string const& s)
{
    std::string r;
    r.reserve(s.size() + 2);
    for (char c : s)
    {
        switch (c)
        {
            case '"': r += "\\\""; break;
            case '\\': r += "\\\\"; break;
            case '\n': r += "\\n"; break;
            case '\r': r += "\\r"; break;
            case '\t': r += "\\t"; break;
            default:
                if (static_cast<unsigned char>(c) < 0x20)
                    r += ' ';
                else
                    r += c;
        }
    }
    return r;
}

std::string unit(Unit const* u)
{
    if (!u)
        return "null";
    std::ostringstream o;
    if (Player const* p = u->ToPlayer())
        o << "{\"id\":" << p->GetGUID().GetCounter() << ",\"n\":\"" << esc(p->GetName()) << "\",\"pl\":1}";
    else
        o << "{\"id\":" << u->GetEntry() << ",\"n\":\"" << esc(u->GetName()) << "\",\"lvl\":" << uint32(u->GetLevel()) << "}";
    return o.str();
}

// One line: {"t":<ms>,"ev":"<ev>","p":<player guid>,"n":"<player name>",<body>}
void write(char const* ev, Player const* p, std::string const& body)
{
    auto t = std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::system_clock::now().time_since_epoch()).count();
    std::ostringstream o;
    o << "{\"t\":" << t << ",\"ev\":\"" << ev << "\"";
    if (p)
        o << ",\"p\":" << p->GetGUID().GetCounter() << ",\"n\":\"" << esc(p->GetName()) << "\"";
    if (!body.empty())
        o << "," << body;
    o << "}\n";
    std::lock_guard<std::mutex> lock(mu);
    if (!out.is_open())
    {
        char const* f = std::getenv("RECORDER_FILE");
        out.open(f ? f : "/azerothcore/env/dist/logs/recorder.jsonl", std::ios::app);
        if (char const* i = std::getenv("RECORDER_INTERVAL_MS"))
            intervalMs = std::max(100, std::atoi(i));
    }
    out << o.str();
    out.flush();
}

void chat(Player* p, uint32 type, std::string const& msg, std::string const& to)
{
    std::ostringstream o;
    o << "\"type\":" << type << ",\"msg\":\"" << esc(msg) << "\"";
    if (!to.empty())
        o << ",\"to\":\"" << esc(to) << "\"";
    write("chat", p, o.str());
}
}

class RecorderPlayerScript : public PlayerScript
{
public:
    RecorderPlayerScript() : PlayerScript("RecorderPlayerScript", {
        PLAYERHOOK_ON_UPDATE, PLAYERHOOK_ON_LOGIN, PLAYERHOOK_ON_LOGOUT, PLAYERHOOK_ON_CREATURE_KILL,
        PLAYERHOOK_ON_PLAYER_KILLED_BY_CREATURE, PLAYERHOOK_ON_PVP_KILL, PLAYERHOOK_ON_PLAYER_RELEASED_GHOST,
        PLAYERHOOK_ON_PLAYER_RESURRECT, PLAYERHOOK_ON_LEVEL_CHANGED, PLAYERHOOK_ON_GIVE_EXP, PLAYERHOOK_ON_MONEY_CHANGED,
        PLAYERHOOK_ON_PLAYER_COMPLETE_QUEST, PLAYERHOOK_ON_ACHI_COMPLETE, PLAYERHOOK_ON_LOOT_ITEM, PLAYERHOOK_ON_UPDATE_ZONE,
        PLAYERHOOK_ON_LEARN_SPELL, PLAYERHOOK_ON_SPELL_CAST, PLAYERHOOK_CAN_PLAYER_USE_CHAT,
        PLAYERHOOK_CAN_PLAYER_USE_PRIVATE_CHAT, PLAYERHOOK_CAN_PLAYER_USE_GROUP_CHAT, PLAYERHOOK_CAN_PLAYER_USE_GUILD_CHAT,
        PLAYERHOOK_CAN_PLAYER_USE_CHANNEL_CHAT }) { }

    void OnPlayerUpdate(Player* p, uint32 diff) override
    {
        uint32& acc = sinceSample[p->GetGUID().GetCounter()]; // ponytail: unlocked map, map threads=1 (AC default)
        acc += diff;
        if (acc < intervalMs)
            return;
        acc = 0;
        Powers pt = p->getPowerType();
        std::ostringstream o;
        o.setf(std::ios::fixed);
        o.precision(1);
        o << "\"m\":" << p->GetMapId() << ",\"x\":" << p->GetPositionX() << ",\"y\":" << p->GetPositionY()
          << ",\"z\":" << p->GetPositionZ() << ",\"o\":" << p->GetOrientation()
          << ",\"hp\":" << p->GetHealth() << ",\"hpmax\":" << p->GetMaxHealth()
          << ",\"pt\":" << int(pt) << ",\"pw\":" << p->GetPower(pt) << ",\"pwmax\":" << p->GetMaxPower(pt)
          << ",\"lvl\":" << uint32(p->GetLevel()) << ",\"xp\":" << p->GetUInt32Value(PLAYER_XP)
          << ",\"money\":" << p->GetMoney() << ",\"zone\":" << p->GetZoneId() << ",\"area\":" << p->GetAreaId()
          << ",\"alive\":" << (p->IsAlive() ? 1 : 0) << ",\"ghost\":" << (p->HasPlayerFlag(PLAYER_FLAGS_GHOST) ? 1 : 0)
          << ",\"combat\":" << (p->IsInCombat() ? 1 : 0)
          << ",\"grp\":" << (p->GetGroup() ? p->GetGroup()->GetGUID().GetCounter() : 0);
        if (Unit* t = p->GetVictim())
            o << ",\"tgt\":" << unit(t);
        write("pos", p, o.str());
    }

    void OnPlayerLogin(Player* p) override
    {
        std::ostringstream o;
        o << "\"race\":" << uint32(p->getRace()) << ",\"cls\":" << uint32(p->getClass()) << ",\"lvl\":" << uint32(p->GetLevel());
        write("login", p, o.str());
    }
    void OnPlayerLogout(Player* p) override { write("logout", p, ""); }
    void OnPlayerCreatureKill(Player* p, Creature* c) override { write("kill", p, "\"victim\":" + unit(c)); }
    void OnPlayerKilledByCreature(Creature* c, Player* p) override { write("death", p, "\"killer\":" + unit(c)); }
    void OnPlayerPVPKill(Player* k, Player* v) override
    {
        write("pvp", k, "\"victim\":" + unit(v));
        write("death", v, "\"killer\":" + unit(k));
    }
    void OnPlayerReleasedGhost(Player* p) override { write("release", p, ""); }
    void OnPlayerResurrect(Player* p, float pct, bool& sickness) override
    {
        std::ostringstream o;
        o << "\"pct\":" << pct << ",\"sickness\":" << (sickness ? 1 : 0);
        write("resurrect", p, o.str());
    }
    void OnPlayerLevelChanged(Player* p, uint8 old) override
    {
        write("level", p, "\"from\":" + std::to_string(old) + ",\"to\":" + std::to_string(p->GetLevel()));
    }
    void OnPlayerGiveXP(Player* p, uint32& amount, Unit* victim, uint8 source) override
    {
        write("xp", p, "\"amount\":" + std::to_string(amount) + ",\"src\":" + std::to_string(source) + (victim ? ",\"victim\":" + unit(victim) : ""));
    }
    void OnPlayerMoneyChanged(Player* p, int32& amount) override { write("money", p, "\"delta\":" + std::to_string(amount)); }
    void OnPlayerCompleteQuest(Player* p, Quest const* q) override
    {
        write("quest", p, "\"id\":" + std::to_string(q->GetQuestId()) + ",\"title\":\"" + esc(q->GetTitle()) + "\"");
    }
    void OnPlayerAchievementComplete(Player* p, AchievementEntry const* a) override
    {
        write("achievement", p, "\"id\":" + std::to_string(a->ID) + ",\"title\":\"" + esc(a->name[0] ? a->name[0] : "") + "\",\"points\":" + std::to_string(a->points));
    }
    void OnPlayerLootItem(Player* p, Item* item, uint32 count, ObjectGuid) override
    {
        write("loot", p, "\"item\":" + std::to_string(item->GetEntry()) + ",\"count\":" + std::to_string(count));
    }
    void OnPlayerUpdateZone(Player* p, uint32 zone, uint32 area) override
    {
        write("zone", p, "\"zone\":" + std::to_string(zone) + ",\"area\":" + std::to_string(area));
    }
    void OnPlayerLearnSpell(Player* p, uint32 spell) override { write("spell", p, "\"id\":" + std::to_string(spell)); }
    void OnPlayerSpellCast(Player* p, Spell* s, bool) override
    {
        SpellInfo const* si = s->GetSpellInfo();
        if (si->IsPassive())
            return; // passives are "cast" on login
        std::string name = si->SpellName[0] ? si->SpellName[0] : "";
        write("cast", p, "\"spell\":" + std::to_string(si->Id) + ",\"name\":\"" + esc(name) + "\",\"target\":" + unit(s->m_targets.GetUnitTarget()));
    }

    bool OnPlayerCanUseChat(Player* p, uint32 type, uint32, std::string& msg) override { chat(p, type, msg, ""); return true; }
    bool OnPlayerCanUseChat(Player* p, uint32 type, uint32, std::string& msg, Player* to) override
    {
        chat(p, type, msg, to ? to->GetName() : "");
        return true;
    }
    bool OnPlayerCanUseChat(Player* p, uint32 type, uint32, std::string& msg, Group*) override { chat(p, type, msg, "party"); return true; }
    bool OnPlayerCanUseChat(Player* p, uint32 type, uint32, std::string& msg, Guild*) override { chat(p, type, msg, "guild"); return true; }
    bool OnPlayerCanUseChat(Player* p, uint32 type, uint32, std::string& msg, Channel* ch) override
    {
        chat(p, type, msg, ch ? ch->GetName() : "");
        return true;
    }
};

// One "group" event per online member, the same shape the SDK recorder writes: {grp, leader, members}.
static void groupChanged(Group* g)
{
    std::ostringstream members;
    bool first = true;
    for (Group::MemberSlot const& m : g->GetMemberSlots())
    {
        members << (first ? "" : ",") << "\"" << esc(m.name) << "\"";
        first = false;
    }
    std::string body = "\"grp\":" + std::to_string(g->GetGUID().GetCounter()) + ",\"leader\":\"" + esc(g->GetLeaderName()) +
        "\",\"members\":[" + members.str() + "]";
    for (Group::MemberSlot const& m : g->GetMemberSlots())
        if (Player* p = ObjectAccessor::FindConnectedPlayer(m.guid))
            write("group", p, body);
}

static void leftGroup(ObjectGuid guid)
{
    if (Player* p = ObjectAccessor::FindConnectedPlayer(guid))
        write("group", p, "\"grp\":0,\"members\":[]");
}

class RecorderGroupScript : public GroupScript
{
public:
    RecorderGroupScript() : GroupScript("RecorderGroupScript", { GROUPHOOK_ON_ADD_MEMBER, GROUPHOOK_ON_REMOVE_MEMBER,
        GROUPHOOK_ON_CHANGE_LEADER, GROUPHOOK_ON_DISBAND }) { }

    void OnAddMember(Group* g, ObjectGuid) override { groupChanged(g); }
    void OnRemoveMember(Group* g, ObjectGuid guid, RemoveMethod, ObjectGuid, char const*) override
    {
        leftGroup(guid);
        groupChanged(g);
    }
    void OnChangeLeader(Group* g, ObjectGuid, ObjectGuid) override { groupChanged(g); }
    void OnDisband(Group* g) override
    {
        for (Group::MemberSlot const& m : g->GetMemberSlots())
            leftGroup(m.guid);
    }
};

class RecorderUnitScript : public UnitScript
{
public:
    RecorderUnitScript() : UnitScript("RecorderUnitScript", true, { UNITHOOK_ON_DAMAGE, UNITHOOK_ON_HEAL }) { }

    void OnDamage(Unit* a, Unit* v, uint32& dmg) override { log("dmg", a, v, dmg); }
    void OnHeal(Unit* a, Unit* v, uint32& gain) override { log("heal", a, v, gain); }

private:
    static void log(char const* ev, Unit* a, Unit* v, uint32 amount)
    {
        Player* p = (a && a->ToPlayer()) ? a->ToPlayer() : (v ? v->ToPlayer() : nullptr);
        if (!p || !amount)
            return; // only events involving a player
        write(ev, p, "\"src\":" + unit(a) + ",\"dst\":" + unit(v) + ",\"amount\":" + std::to_string(amount));
    }
};

void Addmod_recorderScripts()
{
    new RecorderPlayerScript();
    new RecorderUnitScript();
    new RecorderGroupScript();
}
