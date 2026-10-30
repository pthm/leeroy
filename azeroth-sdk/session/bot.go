package session

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/movement"
	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type BotStatus string

const (
	BotStatusIdle           BotStatus = "idle"
	BotStatusAuthenticating BotStatus = "authenticating"
	BotStatusConnecting     BotStatus = "connecting"
	BotStatusInWorld        BotStatus = "in_world"
	BotStatusDone           BotStatus = "done"
	BotStatusError          BotStatus = "error"
)

const (
	wanderRandomPathAttempts    = 8
	wanderRetryCooldown         = 500 * time.Millisecond
	wanderMaxPathLengthFactor   = 3.0
	wanderMaxSimplifiedPathSize = 260
)

// BotResult holds the result of a bot run
type BotResult struct {
	ID     string    `json:"id"`
	Status BotStatus `json:"status"`
	Error  string    `json:"error,omitempty"`
	Level  uint32    `json:"level,omitempty"`
	Kills  int       `json:"kills,omitempty"`
	Deaths int       `json:"deaths,omitempty"`
}

// BotEvent is a notable event that occurred during the bot's life.
type BotEvent struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Message string    `json:"message"`
}

// Bot implements the WoW client bot logic with behavior tree AI.
type Bot struct {
	rec              *Recorder // optional event log (-record)
	lastMoveNoPath   bool
	lastAttackReject atomic.Pointer[attackReject] // for bot.last_attack_error() // last move_to found no navmesh path (move_to returns false)
	id               string
	config           Config
	status           BotStatus
	err              error
	mu               sync.Mutex

	world          *client.WorldClient
	nav            navigation.Navigator
	wasCombat      bool // stateEvents: last tick's combat / alive
	wasAlive       *bool
	clog           *combatLog               // combat.log / combat.meter
	travel         *travelState             // flights, teleports, who moves you
	fight          *fightState              // the running fight, for fight_end
	fightLog       *fightLog                // positions and events of recent fights (wow fight)
	wasGroupCombat bool                     // you or a party member in sight was in combat last tick
	pathCache      map[uint64]pathEntry     // units.nearby routes, see annotateNearby
	cinfo          *combatInfo              // casts, combo points, evades (combatinfo.go)
	npc            *npcState                // gossip, quest giver markers, trainer lists (npc.go)
	agent          *agentState              // Lua agent API state (cast tracking); nil outside sessions
	playerName     func(guid uint64) string // name from the session's name-query cache ("" until the server answers)
	notify         func(text string)        // a notice event (set by setupControl)
	acked          func(key string) bool    // takes a server confirmation (chat echo, invite ok) setupControl recorded
	wm             *worldMsgs               // reputation, MOTD, refused travel... (worldmsg.go)
	lua            *script.Engine
	slowTicks      int       // slow ticks since the last slow_tick event
	slowEmitted    time.Time // the last slow_tick event

	// Stats
	kills  int
	deaths int
	events []BotEvent

	// Movement state is now fully delegated to the separate MovementController.
	movementMu     sync.Mutex
	moveController *movement.MovementController
	isMoving       bool // mirror for quick checks; controller is source of truth

	// Current pursuit target GUID for sticky chasing of moving creatures.
	// This ensures we keep updating the destination instead of heading to a stale snapshot.
	grindTargetGUID       uint64
	lastPursuitUpdate     time.Time
	lastBetterTargetCheck time.Time
	lastMoveToTargetPos   [3]float32
	lastMoveToTargetTime  time.Time

	// lastPursuedTargetPos keeps the last known position for the current pursuit target
	// so we can continue chasing even if the object temporarily goes out of range or is removed.
	lastPursuedTargetGUID uint64
	lastPursuedTargetPos  [3]float32
	lastPursuedTargetTime time.Time

	lastMovementPacket  time.Time
	lastMoveCommandPos  [3]float32
	lastMoveCommandTime time.Time
	targetCacheGUID     uint64
	targetCacheTime     time.Time

	// Combat state
	lastLootGUID        uint64
	lootMu              sync.Mutex
	lastLootAttemptGUID uint64
	lastLootAttemptAt   time.Time
	lastAttackSwingAt   time.Time
	lastCastTime        time.Time // GCD tracking
	lastVictoryRush     bool      // Victory Rush proc available

	// For unstick from bad/dead targets we selected but never entered real combat with
	currentTargetSetAt  time.Time
	lastEngagedGUID     uint64
	engagedTargetHealth uint32 // health when we first engaged; used to detect no-progress on "live" targets

	// Decision chat throttling (so we can see high-level AI choices in-game without flooding chat)
	lastDecisionChat time.Time

	// Separate throttle for "why I think mob is alive" debug messages in chat
	lastAliveReasonChat time.Time

	// per-bot "known dead" guids. This gives each bot its own version of which objects are dead,
	// even if the live cache from server packets still has positive health (stale 8/55 etc).
	// Once we infer dead (low health no progress, dyn flag, etc.), we keep treating it dead
	// in *this bot's* view until we see a positive health update.
	knownDead   map[uint64]bool
	knownDeadMu sync.Mutex

	// Stop channel
	stopCh chan struct{}
	group  *groupLog // group.report's record

	// Validation (only allocated/wired when config.ValidationMode && ValidationLogPath != "")
	// Follows zero-overhead rule: nothing allocated or written on normal paths.
	validationFile *os.File
	validationEnc  *json.Encoder
	validationMu   sync.Mutex
	validationSeq  uint64

	// Teleport / summon resume: set when near/far transfer returns to in_world.
	// Lua scripts poll via ConsumeTeleport() so AI can fully restart at the new pose.
	teleportMu      sync.Mutex
	teleportPending bool
	teleportReason  string

	// After CAST_FAILED NO_POWER (85), treat spell as not ready briefly so AI
	// does not re-spam the same cast every tick while rage/mana regenerates.
	noPowerMu    sync.Mutex
	noPowerUntil map[uint32]time.Time
}

// myPos returns current player position (convenience to avoid direct field access on client).
func (b *Bot) myPos() (x, y, z float32) {
	x, y, z, _, _ = b.world.Position()
	return
}

// NewBot creates a new bot
func NewBot(id string, config Config) *Bot {
	if config.Race == 0 {
		config.Race = 5
	}
	if config.Class == 0 {
		config.Class = 1
	}
	config.Mode = "lua" // the only mode: the client never plays on its own
	if config.AITickMs <= 0 {
		config.AITickMs = 200
	}
	// LogDecisionsToChat is respected as provided by caller (CLI default true for convenience,
	// but for high-scale node/orchestrator runs it should be set to false via config/profile/flag).
	// When false, logDecision becomes a near-zero-cost no-op (only the throttle check + return).

	// DisableTargetCache defaults to false (enable the 800ms target cache).
	// Set to true via flag to force fresh scans every tick for debugging stale target issues.

	b := &Bot{
		id:     id,
		config: config,
		status: BotStatusIdle,
		stopCh: make(chan struct{}),
	}
	if config.ValidationMode && config.ValidationLogPath != "" {
		if f, err := os.OpenFile(config.ValidationLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			b.validationFile = f
			b.validationEnc = json.NewEncoder(f)
		} else {
			// fall back to console note only (do not fail bot)
			fmt.Printf("[validation] failed to open log %s: %v (continuing without file)\n", config.ValidationLogPath, err)
		}
	}
	return b
}

func (b *Bot) Run() BotResult {
	b.setStatus(BotStatusAuthenticating)
	b.log("Starting bot for %s@%s, char: %s, mode: %s",
		b.config.Username, b.config.AuthServer, b.config.CharacterName, b.config.Mode)

	// Step 1: Authenticate
	authClient := client.NewAuthClient(b.config.Username, b.config.Password)
	realms, err := authClient.Authenticate(b.config.AuthServer)
	if err != nil {
		return b.fail("authentication failed: %v", err)
	}
	if len(realms) == 0 {
		return b.fail("no realms available")
	}

	realmIdx := b.config.RealmIndex
	if realmIdx >= len(realms) {
		realmIdx = 0
	}
	realm := realms[realmIdx]
	b.log("Authenticated. Connecting to realm: %s at %s", realm.Name, realm.Address)

	// Step 2: Connect to worldserver
	b.setStatus(BotStatusConnecting)
	b.world = client.NewWorldClient(b.config.Username, authClient.SessionKey(), b.log)
	// Load-safe default: suppress combat/selection/learn thrash on world client.
	// Validation / packet-trace runs raise verbosity for single-bot debug.
	switch {
	case b.config.EnablePacketTrace:
		b.world.SetLogLevel(client.LogTrace)
	case b.config.ValidationMode:
		b.world.SetLogLevel(client.LogDebug)
	default:
		b.world.SetLogLevel(client.LogWarn)
	}

	if err := b.world.Connect(realm.Address); err != nil {
		return b.fail("connect to worldserver failed: %v", err)
	}

	// Set up callbacks
	charListCh := make(chan []client.CharEnumEntry, 1)
	charCreateCh := make(chan uint8, 1)
	b.world.OnCharList = func(chars []client.CharEnumEntry) {
		select {
		case charListCh <- chars:
		default:
		}
	}
	b.world.OnCharCreateResult = func(data []byte) {
		if len(data) > 0 {
			select {
			case charCreateCh <- data[0]:
			default:
			}
		}
	}
	b.world.OnKill = func(victimGUID uint64) { // melee killing blows only: b.kills counts every kill (the kill event)
		// the kill line in the log comes from the kill event (events.go), spells included
		victim := b.world.GetObject(victimGUID)

		// Immediately mark dead in cache so IsAlive() and finders see death without waiting for update packets.
		b.world.MarkObjectDead(victimGUID)
		b.markKnownDead(victimGUID)

		// Only set lastLoot if the corpse is close; we do NOT want to path across the world to loot.
		// If far, just clear states and let grind/wander take over.
		setForLoot := false
		if victim != nil {
			px, py, pz, _, _ := b.world.Position()
			d := victim.DistanceTo(px, py, pz)
			// dist log removed from console
			if d <= 12.0 {
				b.lastLootGUID = victimGUID
				setForLoot = true
			}
		} else if b.world.TargetGUID() == victimGUID {
			// No obj but it was our target: don't set far loot
			b.lastLootGUID = 0
		}
		if !setForLoot && b.world.TargetGUID() == victimGUID {
			b.lastLootGUID = 0
		}

		// If this kill was our current target or we were attacking it, clear combat state NOW.
		if b.world.TargetGUID() == victimGUID {
			b.world.ClearTarget()
			b.world.ClearCombat()
			b.world.AttackStop()
			if b.config.Mode != "lua" { // lua scripts own movement
				b.stopCurrentMove()
			}
			b.currentTargetSetAt = time.Time{}
			b.lastEngagedGUID = 0
			// cleared log removed from console (chat will show via decisions)
		}

		b.lastVictoryRush = true // Victory Rush proc
	}
	b.world.OnDeath = func() {
		b.deaths++
		b.addEvent("death", "Bot died! (total deaths: %d)", b.deaths)
	}
	b.world.OnLevelUp = func(newLevel uint32) {
		b.addEvent("levelup", "Reached level %d", newLevel)
	}
	b.world.OnCombatStart = func(attacker, victim uint64) {
		if victim == b.world.CharGUID() || attacker == b.world.CharGUID() {
			b.debugf("Combat started (attacker: %d, victim: %d)", attacker, victim)
		}
	}
	// OnAttackReject is the preferred path: classifies AC swing feedback so we
	// never invent "dead" from BAD_FACING / NOT_IN_RANGE (protocol preconditions).
	b.world.OnAttackReject = func(r client.AttackReject) {
		b.lastAttackReject.Store(&attackReject{r, time.Now()})
		if b.config.Mode == "lua" {
			// No auto-face / auto-chase / stop for lua scripts: they see it via bot.last_attack_error().
			if r.Class == client.RejectTerminal && r.GUID != 0 {
				b.markKnownDead(r.GUID)
			}
			return
		}
		switch r.Class {
		case client.RejectTerminal:
			if r.GUID != 0 {
				b.markKnownDead(r.GUID)
			}
			b.logDecision("Server reject TERMINAL %s GUID=%d (drop target)", r.Reason, r.GUID)
			// Target/combat already cleared in WorldClient for terminal.
			b.stopCurrentMove()
		case client.RejectTransient:
			// Keep target; close the gap. Lua distance can be stale so we repath
			// on NOT_IN_RANGE even if AI thinks we are already in melee.
			b.logDecision("Server reject TRANSIENT %s GUID=%d (keep target, reapproach/face)", r.Reason, r.GUID)
			if r.GUID != 0 && r.Reason == client.RejectReasonBadFacing {
				_ = b.FaceTarget(r.GUID)
			}
			if r.GUID != 0 && r.Reason == client.RejectReasonNotInRange {
				if t := b.world.GetObject(r.GUID); t != nil {
					tx, ty, tz := t.InterpolatedPosition()
					// Force a fresh path (clear throttle).
					b.lastMoveCommandTime = time.Time{}
					b.moveToPoint(tx, ty, tz)
				}
			}
		default:
			// ATTACK_STOP / unknown: do not markKnownDead (ambiguous).
			b.logDecision("Server reject %s GUID=%d class=%s (no dead mark)", r.Reason, r.GUID, r.Class)
		}
	}
	// Legacy terminal-only callback (still fired by WorldClient for DEAD/CANT).
	b.world.OnInvalidTarget = func(victimGUID uint64) {
		if victimGUID == 0 {
			return
		}
		// OnAttackReject already handled markKnownDead for terminal; keep this
		// as a safety net if something only fires OnInvalidTarget.
		if !b.isKnownDead(victimGUID) {
			b.markKnownDead(victimGUID)
			b.logDecision("OnInvalidTarget GUID=%d (terminal safety net)", victimGUID)
		}
	}
	b.wm = b.watchWorldMessages()
	b.world.AddPacketHook(func(op uint16, data []byte) {
		if op == client.SmsgLootResponse {
			b.handleLootResponse(data)
		}
	})

	// Summon / .go / portal: interrupt movement+combat and flag Lua to restart AI.
	b.wireTeleportHandling()
	// Charge / blink / server-forced player splines: snap movement controller.
	b.wireServerRelocateHandling()

	// Start world client
	worldErrCh := make(chan error, 1)
	go func() {
		worldErrCh <- b.world.Run()
	}()

	time.Sleep(2 * time.Second)

	// Step 3: Character list
	b.world.SendReadyForAccountDataTimes()
	b.world.SendRealmSplit()
	if err := b.world.RequestCharList(); err != nil {
		return b.fail("request char list failed: %v", err)
	}

	var chars []client.CharEnumEntry
	select {
	case chars = <-charListCh:
	case <-time.After(120 * time.Second):
		return b.fail("timeout waiting for character list")
	}

	// Optional: delete all existing characters before creating (orchestrator or explicit flag only)
	if b.config.DeleteExistingCharacters && len(chars) > 0 {
		b.log("DeleteExistingCharacters enabled: deleting %d existing character(s)...", len(chars))
		for _, ch := range chars {
			if err := b.world.DeleteCharacter(ch.GUID); err != nil {
				b.log("Warning: failed to delete character %s (GUID %d): %v", ch.Name, ch.GUID, err)
			} else {
				b.log("Deleted character %s", ch.Name)
			}
			time.Sleep(150 * time.Millisecond)
		}
		// Refresh list after deletes
		time.Sleep(500 * time.Millisecond)
		b.world.SendReadyForAccountDataTimes()
		b.world.SendRealmSplit()
		if err := b.world.RequestCharList(); err != nil {
			return b.fail("request char list after delete failed: %v", err)
		}
		select {
		case chars = <-charListCh:
		case <-time.After(120 * time.Second):
			return b.fail("timeout waiting for character list after deletes")
		}
		b.log("Character list refreshed after deletes (%d remaining)", len(chars))
	}

	// Step 4: Find or create character
	var charGUID uint64
	found := false
	for _, ch := range chars {
		if strings.EqualFold(ch.Name, b.config.CharacterName) {
			charGUID = ch.GUID
			b.world.SetCharRace(ch.Race) // leeroy: was never set, so chat always used Common
			found = true
			b.log("Found character %s (GUID: %d, Level: %d)", ch.Name, ch.GUID, ch.Level)
			break
		}
	}

	if !found {
		// Generate a highly unique starting name. Orchestrator also generates one,
		// but we keep trying fresh unique names on NAME_IN_USE.
		charName := b.config.CharacterName
		var createResult uint8
		for attempt := 0; attempt < 10; attempt++ {
			if attempt > 0 {
				// Characters are addressed by name (judges, chat); never substitute another one.
				return b.fail("could not create character %s (result %d; name taken?)", charName, createResult)
			}
			b.log("Creating character %s (attempt %d)...", charName, attempt+1)
			if err := b.world.CreateCharacter(
				charName, b.config.Race, b.config.Class, b.config.Gender,
				0, 0, 0, 0, 0, 0,
			); err != nil {
				return b.fail("create character failed: %v", err)
			}

			select {
			case createResult = <-charCreateCh:
			case <-time.After(120 * time.Second):
				return b.fail("timeout waiting for character creation")
			}

			if createResult == 0x2F { // CHAR_CREATE_SUCCESS
				b.config.CharacterName = charName
				break
			}
			if createResult != 0x32 { // Not CHAR_CREATE_NAME_IN_USE
				return b.fail("character creation failed with code 0x%X", createResult)
			}
			b.log("Name %s already in use, generating a new unique name...", charName)
		}
		if createResult != 0x2F {
			return b.fail("character creation failed after retries, last code 0x%X", createResult)
		}

		b.log("Character created, requesting updated char list")
		b.world.SendReadyForAccountDataTimes()
		b.world.SendRealmSplit()
		if err := b.world.RequestCharList(); err != nil {
			return b.fail("request char list after create failed: %v", err)
		}

		select {
		case chars = <-charListCh:
		case <-time.After(120 * time.Second):
			return b.fail("timeout waiting for char list after create")
		}

		for _, ch := range chars {
			if strings.EqualFold(ch.Name, b.config.CharacterName) {
				charGUID = ch.GUID
				b.world.SetCharRace(ch.Race) // leeroy: was never set, so chat always used Common
				found = true
				break
			}
		}
		if !found {
			return b.fail("character not found after creation")
		}
	}

	// Step 5b: Login
	b.log("Logging in with character GUID %d", charGUID)
	if err := b.world.LoginCharacter(charGUID); err != nil {
		return b.fail("login character failed: %v", err)
	}

	// Race login completion vs connection error.
	loginErrCh := make(chan error, 1)
	go func() {
		loginErrCh <- b.world.WaitForLogin(120 * time.Second)
	}()
	select {
	case err := <-loginErrCh:
		if err != nil {
			return b.fail("login verify failed: %v", err)
		}
	case err := <-worldErrCh:
		if err != nil {
			return b.fail("world connection died during login: %v", err)
		}
	}

	b.setStatus(BotStatusInWorld)
	x, y, z, o, m := b.world.Position()
	_ = x
	_ = y
	_ = z
	_ = o // position available for movement controller
	b.log("Character in world on map %d", m)

	time.Sleep(1 * time.Second)
	b.world.SetActiveMover(charGUID)
	time.Sleep(500 * time.Millisecond)

	b.ensureMovementController()

	// Wait a bit more for all initial data to arrive
	time.Sleep(1 * time.Second)

	// Complete any cinematic (new characters get a cinematic that may block chat)
	b.world.CompleteCinematic()
	time.Sleep(500 * time.Millisecond)

	// Step 7: Initialize navigation
	b.initNavigation()

	// Snap to real ground height right after entering the world.
	// This prevents the bot from starting under the map due to DB position
	// or login placement not being perfectly on terrain.
	if b.nav != nil {
		x, y, z, _, mapID := b.world.Position()
		probeZ := z + 5.0
		if gh, ok := movementGroundHeight(b.nav, mapID, x, y, probeZ); ok {
			delta := gh - z
			if delta <= 1.0 {
				_, _, _, o, _ := b.world.Position()
				b.world.UpdatePosition(x, y, gh, o)
			}
		}
	} else {
		// no nav available for ground snap
	}

	// Step 8: Initialize Lua engine
	var bus *script.Bus
	if b.config.EventsFile != "" {
		bus = script.NewBus(b.config.EventsFile)
	}
	eng := script.NewEngine(b.log, bus)
	agent := b.config.AgentID
	if agent == "" {
		agent = b.config.CharacterName
	}
	eng.SetDiagnosticSink(agent, b.rec.writeDiagnostic)
	if b.config.LuaWorkspace != "" {
		if err := eng.SetWorkspace(b.config.LuaWorkspace); err != nil {
			return b.fail("Lua workspace: %v", err)
		}
	}
	b.mu.Lock()
	b.lua = eng
	b.mu.Unlock()
	b.setupControl() // leeroy: agent control bindings + AZGHOST_CONTROL eval endpoint

	// Startup script: inline code or a file.
	if b.config.LuaCode != "" {
		if err := b.lua.DoString(b.config.LuaCode); err != nil {
			b.log("LuaCode load error: %v", err)
		}
	} else if b.config.LuaScript != "" {
		if err := b.lua.DoFile(b.config.LuaScript); err != nil {
			b.log("Failed to load Lua script: %v", err)
		}
	}

	// Step 10: Run AI loop
	b.addEvent("start", "Bot entered world, starting AI loop (mode: %s, level: %d)", b.config.Mode, b.world.PlayerLevel())

	b.runAILoop(worldErrCh)

	b.world.Close()
	if b.nav != nil {
		b.nav.Close()
	}
	if b.lua != nil {
		b.lua.Close()
	}

	b.setStatus(BotStatusDone)
	b.log("Bot finished. Kills: %d, Deaths: %d", b.kills, b.deaths)

	return BotResult{
		ID:     b.id,
		Status: BotStatusDone,
		Level:  b.world.PlayerLevel(),
		Kills:  b.kills,
		Deaths: b.deaths,
	}
}

func (b *Bot) initNavigation() {
	if b.config.PathfindingAddress != "" {
		nav, err := navigation.NewRemoteNavigator(b.config.PathfindingAddress)
		if err != nil {
			b.log("Failed to connect to pathfinding service: %v, falling back to embedded", err)
		} else {
			b.nav = nav
			b.log("Using remote pathfinding service at %s", b.config.PathfindingAddress)
			b.attachNavigatorToMovementController()
			return
		}
	}
	if b.config.DataDir != "" {
		b.nav = navigation.NewEmbeddedNavigator(b.config.DataDir)
		b.log("Using embedded pathfinding with data dir %s", b.config.DataDir)
	} else {
		b.log("No pathfinding configured, movement will be direct")
	}
	b.attachNavigatorToMovementController()
	go b.warmNavigation()
}

// warmNavigation loads the navmesh around the character in the background whenever it arrives somewhere new (login,
// a teleport, a hearth, the end of a flight). The first route in an area loads every tile within ten grid cells of
// it, about a second; done here, no agent call waits for it.
// ponytail: polls position every 2 s; hook teleport events if that ever lags.
func (b *Bot) warmNavigation() {
	if b.nav == nil {
		return
	}
	var warmMap uint32 = math.MaxUint32
	var warmAt navigation.Point3D
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		x, y, z, _, m := b.world.Position()
		here := navigation.Point3D{X: x, Y: y, Z: z}
		if (x != 0 || y != 0) && (m != warmMap || here.DistanceTo2D(warmAt) >= 500) { // in the world, somewhere new
			start := time.Now()
			b.nav.FindPath(m, here, navigation.Point3D{X: x + 5, Y: y, Z: z}) // the route itself doesn't matter: its tiles do
			warmMap, warmAt = m, here
			b.log("navmesh warmed for map %d in %s", m, time.Since(start).Round(time.Millisecond))
		}
		select {
		case <-b.stopCh:
			return
		case <-tick.C:
		}
	}
}

func (b *Bot) attachNavigatorToMovementController() {
	if b.nav == nil {
		return
	}
	b.movementMu.Lock()
	defer b.movementMu.Unlock()
	if b.moveController != nil {
		b.moveController.SetNavigator(b.nav)
	}
}

func (b *Bot) ensureMovementController() {
	b.movementMu.Lock()
	defer b.movementMu.Unlock()
	b.ensureMovementControllerLocked()
}

func (b *Bot) ensureMovementControllerLocked() {
	if b.moveController != nil {
		if b.nav != nil {
			b.moveController.SetNavigator(b.nav)
		}
		return
	}
	if b.world == nil {
		return
	}
	sender := movement.NewWorldMovementSender(b.world)
	cfg := movement.DefaultMovementConfig()
	speed := b.world.MoveSpeed()
	if speed <= 0 {
		speed = 7.0
	}
	b.moveController = movement.NewMovementController(sender, speed, b.nav, cfg)
	w := b.world
	b.moveController.SpeedFunc = func(swimming bool) float32 {
		switch {
		case w.Rooted():
			return 0
		case swimming:
			return w.SwimSpeed()
		}
		return w.MoveSpeed()
	}
	b.moveController.WaterFunc = func(x, y float32) (float32, bool) {
		// resolved per call: b.nav may be attached after the controller is created
		wn, ok := b.nav.(interface {
			GetWaterLevel(mapID uint32, x, y float32) (float32, bool)
		})
		if !ok {
			return 0, false
		}
		_, _, _, _, m := w.Position()
		return wn.GetWaterLevel(m, x, y)
	}
	b.moveController.OnSwimChange = func(on bool) {
		w.SetSwimming(on)
		b.debugf("swimming=%v", on)
	}
	w.SetGroundFunc(func(mapID uint32, x, y, z float32) (float32, bool) {
		nav := b.nav
		if nav == nil {
			return 0, false
		}
		gh, ok := movementGroundHeight(nav, mapID, x, y, z)
		if !ok {
			return 0, false
		}
		return movement.FloorAware(nav, mapID, x, y, z, gh), true
	})
	// Seed the controller with the real position we just got from the server so we don't start at (0,0,0)
	cx, cy, cz, co, _ := b.world.Position()
	b.moveController.InitPositionFromWorld(cx, cy, cz, co)
}

func (b *Bot) runAILoop(worldErrCh chan error) {
	tick := time.Duration(b.config.AITickMs) * time.Millisecond
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	heartbeatTicker := time.NewTicker(5 * time.Second)
	defer heartbeatTicker.Stop()

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()
	var pingSeq uint32

	movementStop := make(chan struct{})
	movementDone := make(chan struct{})
	go b.runMovementLoop(movementStop, movementDone)
	defer func() {
		close(movementStop)
		<-movementDone
	}()

	for {
		select {
		case <-b.stopCh:
			// Log out like the real client: a dropped connection leaves the character in the world (and in combat
			// it can die there). AC takes ~20 s out of combat and refuses in combat, so cap the wait.
			if b.world.SendLogout() == nil {
				_ = b.world.WaitForLogout(25 * time.Second)
			}
			return
		case err := <-worldErrCh:
			if err != nil {
				b.log("World connection error: %v", err)
			}
			return
		case <-heartbeatTicker.C:
			// Only force a keepalive when not actively driving movement.
			// Frequent movement HBs are sent from runMovementLoop, independent of AI ticks.
			b.movementMu.Lock()
			if !b.isMoving {
				b.world.SendHeartbeat()
			}
			b.movementMu.Unlock()
		case <-pingTicker.C:
			pingSeq++
			b.world.SendPing(pingSeq)
		case <-ticker.C:
			// The agent's on_tick is the only AI: the client never plays on its own.
			tickStart := time.Now()
			if b.lua != nil {
				b.stateEvents()
				b.sampleGroup()
				b.lua.CallTick()
			}
			if elapsed := time.Since(tickStart); elapsed > 300*time.Millisecond {
				b.slowTick(elapsed)
			}
		}
	}
}

func (b *Bot) runMovementLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-b.stopCh:
			return
		case <-stop:
			return
		case <-ticker.C:
			b.updateMovement()
		}
	}
}

// ============================================================
// Behavior tree construction
// ============================================================

func (b *Bot) moveToPoint(x, y, z float32) {
	b.ensureMovementController()
	if b.world.Falling() { // no steering in the air
		return
	}

	now := time.Now()
	requestedDest := [3]float32{x, y, z}
	px, py, pz, po, mapID := b.world.Position()

	b.movementMu.Lock()
	if b.moveController == nil {
		b.movementMu.Unlock()
		// Fallback: at least stop any old movement
		b.world.MoveStop()
		return
	}

	if b.moveController.IsMoving() {
		b.moveController.Update(now)
		b.isMoving = b.moveController.IsMoving()
		cx, cy, cz, co := b.moveController.CurrentPosition()
		px, py, pz, po = cx, cy, cz, co
		b.world.UpdatePosition(cx, cy, cz, co)
	}

	// Throttle repaths hard while walking. Live mob chase calls move_to every
	// AI tick with a slightly new XYZ; re-SetPath is what makes movement look
	// like constant direction changes.
	if b.isMoving && !b.lastMoveCommandTime.IsZero() {
		since := now.Sub(b.lastMoveCommandTime)
		ddx := x - b.lastMoveCommandPos[0]
		ddy := y - b.lastMoveCommandPos[1]
		ddz := z - b.lastMoveCommandPos[2]
		destMoved2 := ddx*ddx + ddy*ddy

		// Sticky: same-ish dest for 1.5s — never repath.
		if destMoved2 < 25.0 && math.Abs(float64(ddz)) < 5.0 && since < 1500*time.Millisecond {
			b.movementMu.Unlock()
			return
		}
		// Moderate mob drift: repath at most ~0.67Hz.
		if destMoved2 < 64.0 && math.Abs(float64(ddz)) < 8.0 && since < 1200*time.Millisecond {
			b.movementMu.Unlock()
			return
		}
		// Absolute floor while moving.
		if since < 700*time.Millisecond {
			b.movementMu.Unlock()
			return
		}
		// Path already ends near requested dest.
		if ex, ey, ez, ok := b.moveController.Destination(); ok {
			edx, edy, edz := ex-x, ey-y, ez-z
			if edx*edx+edy*edy < 16.0 && math.Abs(float64(edz)) < 5.0 {
				b.movementMu.Unlock()
				return
			}
		}
	}
	b.movementMu.Unlock()

	// Before any movement decisions, snap both current position and target to real
	// ground height using a small Z offset. Querying GetHeight from slightly above
	// the expected ground is required to get accurate terrain height.
	if b.nav != nil {
		// Snap current position to ground if needed (small corrections only; avoid jumping levels).
		probeZ := pz + 5.0
		if gh, ok := movementGroundHeight(b.nav, mapID, px, py, probeZ); ok {
			delta := gh - pz
			if delta > 1.0 {
				// would snap up significantly (possible upper floor) - keep current Z
			} else if math.Abs(float64(delta)) > 0.5 {
				pz = gh
			} else {
				pz = gh
			}
		}
		targetProbe := z + 5.0
		if gh, ok := movementGroundHeight(b.nav, mapID, x, y, targetProbe); ok {
			delta := gh - z
			if math.Abs(float64(delta)) > 2.0 {
				// large difference - trust the provided target Z (e.g. from random generator or mob)
				gh = z
			} else if math.Abs(float64(delta)) > 0.5 {
				z = gh
			} else {
				z = gh
			}
		}
	}

	// (A clamp here kept the target within 5 yd of the current height, for the removed AI's mob pursuit. It
	// sent every trip to a higher floor, such as Stormwind from Goldshire, to the terrain under it.)

	current := navigation.Point3D{X: px, Y: py, Z: pz}

	// If we have nav, compute a proper path (real pathfinding as required for movement).
	var pts []navigation.Point3D
	if b.nav != nil {
		result, err := b.nav.FindPath(mapID, current, navigation.Point3D{X: x, Y: y, Z: z})
		b.lastMoveNoPath = !(err == nil && result != nil && result.Found)
		if err != nil {
			b.log("pathfinding error: %v", err) // e.g. mmaps version mismatch; was silent
		}
		if err == nil && result != nil && result.Found && len(result.Points) > 1 {
			pts = simplifyAndDensifyPath(result.Points, 3.0, 1.0)
			// The navmesh path is used as is: it already excludes slopes over the walkable angle and
			// obstacles. (A "crazy path" check here used to swap long paths for a straight line, which
			// walked bots up cliffs and through buildings.)
		} else {
			b.logDecision("No path found to target")
		}
	}

	if len(pts) == 0 {
		// No navmesh path: only short hops (melee positioning) go straight; anything longer would climb
		// unwalkable slopes, so don't move and let move_to report false.
		// 3D: a target straight overhead is not a short hop (it let bots step through floors)
		if b.nav != nil && current.DistanceTo(navigation.Point3D{X: x, Y: y, Z: z}) > 5 {
			return
		}
		pts = simplifyAndDensifyPath([]navigation.Point3D{current, {X: x, Y: y, Z: z}}, 3.0, 1.0)
	}

	// Ensure first point is exactly current for smooth handoff
	if len(pts) > 0 {
		pts[0] = current
	}

	if b.nav != nil {
		snapPathToGround(b.nav, mapID, pts)
		if len(pts) > 0 {
			pts[0] = current
		}
	}

	// Hand off to the (now only) movement implementation.
	// The controller handles time-based following, 500ms HBs, and explicit turns at direction changes.
	b.movementMu.Lock()
	b.ensureMovementControllerLocked()
	if b.moveController != nil {
		b.moveController.SetPath(pts, now, po, mapID)
		b.isMoving = true
		b.lastMoveCommandPos = requestedDest
		b.lastMoveCommandTime = now
	}
	b.movementMu.Unlock()

	// The controller will emit the appropriate START / HB packets.
}

type terrainHeightNavigator interface {
	GetTerrainHeight(mapID uint32, x, y float32) (float32, bool)
}

func movementGroundHeight(nav navigation.Navigator, mapID uint32, x, y, fallbackHintZ float32) (float32, bool) {
	gh, _, ok := movementGroundHeightWithSource(nav, mapID, x, y, fallbackHintZ)
	return gh, ok
}

func movementGroundHeightWithSource(nav navigation.Navigator, mapID uint32, x, y, fallbackHintZ float32) (float32, bool, bool) {
	if gh, ok := nav.GetHeight(mapID, x, y, fallbackHintZ); ok {
		return gh, false, true
	}
	if terrainNav, ok := nav.(terrainHeightNavigator); ok {
		if gh, ok := terrainNav.GetTerrainHeight(mapID, x, y); ok {
			return gh, true, true
		}
	}
	return 0, false, false
}

func snapPathToGround(nav navigation.Navigator, mapID uint32, pts []navigation.Point3D) {
	if nav == nil {
		return
	}
	for i := range pts {
		origZ := pts[i].Z
		gh, _, ok := movementGroundHeightWithSource(nav, mapID, pts[i].X, pts[i].Y, origZ)
		if !ok || math.IsNaN(float64(gh)) || math.IsInf(float64(gh), 0) {
			continue
		}
		pts[i].Z = movement.FloorAware(nav, mapID, pts[i].X, pts[i].Y, origZ, gh)
	}
}

// simplifyAndDensifyPath reduces zig-zags (simplify collinear) and adds intermediate points
// for smoother following on uneven terrain like Durotar hills. This helps make path following
// look less "crazy" when navmesh produces sparse or jagged paths.
func simplifyAndDensifyPath(pts []navigation.Point3D, maxStep, collinearTol float32) []navigation.Point3D {
	if len(pts) < 2 {
		return pts
	}
	// densify first (use 2D horizontal distance for step size; Z will be ground-snapped live)
	dense := []navigation.Point3D{pts[0]}
	for i := 1; i < len(pts); i++ {
		p0 := pts[i-1]
		p1 := pts[i]
		d := p0.DistanceTo2D(p1)
		if d > maxStep && d > 0.001 {
			n := int(d / maxStep)
			for k := 1; k <= n; k++ {
				t := float32(k) / float32(n+1)
				dense = append(dense, navigation.Point3D{
					X: p0.X + (p1.X-p0.X)*t,
					Y: p0.Y + (p1.Y-p0.Y)*t,
					Z: p0.Z + (p1.Z-p0.Z)*t, // interp; snapPathToGround corrects to terrain
				})
			}
		}
		dense = append(dense, p1)
	}
	if len(dense) <= 2 {
		return dense
	}
	// simplify collinear-ish points (keep changes in direction or steep Z)
	simp := []navigation.Point3D{dense[0]}
	for i := 1; i < len(dense)-1; i++ {
		a := simp[len(simp)-1]
		b := dense[i]
		c := dense[i+1]
		abx := b.X - a.X
		aby := b.Y - a.Y
		abz := b.Z - a.Z
		bcx := c.X - b.X
		bcy := c.Y - b.Y
		bcz := c.Z - b.Z
		// 3D cross product mag
		crx := aby*bcz - abz*bcy
		cry := abz*bcx - abx*bcz
		crz := abx*bcy - aby*bcx
		cross := float32(math.Sqrt(float64(crx*crx + cry*cry + crz*crz)))
		if cross > collinearTol || math.Abs(float64(abz)) > 1.5 || math.Abs(float64(bcz)) > 1.5 {
			simp = append(simp, b)
		}
	}
	simp = append(simp, dense[len(dense)-1])
	return simp
}

// stopCurrentMove aborts any in-progress path without necessarily sending a stop packet.
// Called on kill of target etc. to prevent continuing to run toward a now-dead mob's last position.
func (b *Bot) stopCurrentMove() {
	b.movementMu.Lock()
	defer b.movementMu.Unlock()
	b.isMoving = false
	b.lastMoveCommandTime = time.Time{}
	if b.moveController != nil {
		b.moveController.Stop(time.Now())
	}
}

// wireTeleportHandling installs a session-phase hook that reacts to near teleports
// (summon, MSG_MOVE_TELEPORT) and far transfers (SMSG_NEW_WORLD).
// Must run after wireValidationInstrumentation so both callbacks chain correctly.
func (b *Bot) wireTeleportHandling() {
	if b == nil || b.world == nil {
		return
	}
	prev := b.world.OnSessionPhase
	b.world.OnSessionPhase = func(c client.SessionPhaseChange) {
		if prev != nil {
			prev(c)
		}
		// Transfer in flight: drop local path immediately so the movement loop
		// cannot keep extrapolating from the pre-teleport pose.
		if c.To == client.PhaseNearTeleport || c.To == client.PhaseFarTransfer {
			b.abortMovementForTeleport()
			return
		}
		// Transfer complete: snap to new pose, clear combat, flag Lua restart.
		if c.To == client.PhaseInWorld &&
			(c.From == client.PhaseNearTeleport || c.From == client.PhaseFarTransfer) {
			b.handleTeleportResume(c.Reason)
		}
	}
}

// abortMovementForTeleport silently cancels any active path (no MoveStop at old coords).
func (b *Bot) abortMovementForTeleport() {
	b.movementMu.Lock()
	defer b.movementMu.Unlock()
	b.isMoving = false
	b.lastMoveCommandTime = time.Time{}
	if b.moveController != nil {
		b.moveController.AbortSilent()
	}
}

// handleTeleportResume runs when the session returns to in_world after a summon/teleport.
// Interrupts combat + movement, snaps the movement controller, and flags Lua AI to restart.
func (b *Bot) handleTeleportResume(reason string) {
	if b.world == nil {
		return
	}
	x, y, z, o, mapID := b.world.Position()
	b.debugf("Teleport resume (%s): map=%d pos=(%.1f,%.1f,%.1f) — interrupt everything, restart AI",
		reason, mapID, x, y, z)

	// Local combat interrupt first (always). Best-effort CMSG when socket is live.
	b.world.ClearTarget()
	b.world.ClearCombat()
	_ = b.world.AttackStop()
	_ = b.world.SetTarget(0)

	b.movementMu.Lock()
	b.isMoving = false
	b.lastMoveCommandTime = time.Time{}
	b.lastMoveCommandPos = [3]float32{}
	b.ensureMovementControllerLocked()
	if b.moveController != nil {
		b.moveController.AbortAndSnap(x, y, z, o)
	}
	b.movementMu.Unlock()

	// Drop Go-side pursuit / loot sticky state so built-in AI cannot chase old coords.
	b.grindTargetGUID = 0
	b.lastPursuedTargetGUID = 0
	b.lastPursuedTargetPos = [3]float32{}
	b.lastPursuedTargetTime = time.Time{}
	b.lastPursuitUpdate = time.Time{}
	b.lastMoveToTargetPos = [3]float32{}
	b.lastMoveToTargetTime = time.Time{}
	b.lastAttackSwingAt = time.Time{}
	b.currentTargetSetAt = time.Time{}
	b.lastEngagedGUID = 0
	b.engagedTargetHealth = 0
	b.lastLootGUID = 0
	b.lastLootAttemptGUID = 0
	b.lastLootAttemptAt = time.Time{}

	b.teleportMu.Lock()
	b.teleportPending = true
	b.teleportReason = reason
	b.teleportMu.Unlock()

	// Do not call into Lua here (world read loop). Scripts poll bot.consume_teleport()
	// on the next AI tick and fully restart sticky state from the new pose.
}

// ConsumeTeleport returns true once after a completed summon/teleport so Lua can
// clear sticky state and re-settle at the new position.
func (b *Bot) ConsumeTeleport() bool {
	b.teleportMu.Lock()
	defer b.teleportMu.Unlock()
	if !b.teleportPending {
		return false
	}
	b.teleportPending = false
	b.teleportReason = ""
	return true
}

// Spells that forcibly relocate the caster (Charge, Intercept, Blink, …).
// On SPELL_GO we abort local pathing; the final pose comes from MONSTER_MOVE.
var relocateOnCastSpells = map[uint32]struct{}{
	100:   {}, // Charge
	20252: {}, // Intercept
	3411:  {}, // Intervene
	1953:  {}, // Blink
}

// wireServerRelocateHandling snaps the movement controller when the server
// relocates the player (Charge is the common case: without this the controller
// keeps simulating the pre-charge path and rubber-bands the bot home).
func (b *Bot) wireServerRelocateHandling() {
	if b == nil || b.world == nil {
		return
	}
	prevReloc := b.world.OnServerRelocate
	b.world.OnServerRelocate = func(x, y, z, o float32, reason string) {
		b.debugf("Server relocate (%s): pos=(%.1f,%.1f,%.1f) — abort local path", reason, x, y, z)
		b.movementMu.Lock()
		b.isMoving = false
		b.lastMoveCommandTime = time.Time{}
		b.lastMoveCommandPos = [3]float32{}
		b.ensureMovementControllerLocked()
		if b.moveController != nil {
			b.moveController.AbortAndSnap(x, y, z, o)
		}
		b.movementMu.Unlock()
		// Clear sticky pursuit dest so we re-path from the new pose.
		b.lastPursuitUpdate = time.Time{}
		b.lastMoveToTargetTime = time.Time{}
		if prevReloc != nil {
			prevReloc(x, y, z, o, reason)
		}
	}

	prevSpell := b.world.OnSpellCastResult
	b.world.OnSpellCastResult = func(spellID uint32, success bool, failReason uint8) {
		if success {
			if _, ok := relocateOnCastSpells[spellID]; ok {
				// Drop local path immediately; MONSTER_MOVE will snap pose.
				b.abortMovementForTeleport()
				b.logDecision("RELOCATE_SPELL id=%d — abort path pending server pose", spellID)
			}
		} else if failReason == 85 { // SPELL_FAILED_NO_POWER
			b.noteSpellNoPower(spellID)
			b.logDecision("NO_POWER spell=%d — block re-cast 1.5s", spellID)
		}
		if prevSpell != nil {
			prevSpell(spellID, success, failReason)
		}
	}
}

func (b *Bot) updateMovement() {
	b.movementMu.Lock()
	defer b.movementMu.Unlock()

	if b.moveController == nil || b.world.Falling() { // the world client moves us until we land
		return
	}

	b.moveController.Update(time.Now())
	b.isMoving = b.moveController.IsMoving()

	// Sync position from controller ONLY if it has actually started a path (travelDist > 0 or isMoving).
	// Otherwise we would overwrite the correct login position with struct-zero (0,0,0).
	if b.moveController.TravelDist() > 0 || b.isMoving {
		cx, cy, cz, co := b.moveController.CurrentPosition()
		b.world.UpdatePosition(cx, cy, cz, co)
	}
}

func (b *Bot) movementActive() bool {
	b.movementMu.Lock()
	defer b.movementMu.Unlock()
	return b.isMoving
}

// ============================================================
// LuaEngine BotAPI implementation
// ============================================================

func (b *Bot) GetPosition() (x, y, z, o float32) {
	x, y, z, o, _ = b.world.Position()
	return
}

func (b *Bot) GetFacing() float32 {
	_, _, _, o, _ := b.world.Position()
	return o
}

func (b *Bot) SetFacing(o float32) error {
	// Preserve forward motion when Lua adjusts facing mid-path.
	if b.movementActive() {
		return b.world.SetFacingMoving(o)
	}
	return b.world.SetFacing(o)
}

func (b *Bot) FaceTarget(guid uint64) bool {
	u := b.GetUnitInfo(guid)
	if u == nil {
		return false
	}
	px, py, _, po := b.GetPosition()
	dx := u.PosX - px
	dy := u.PosY - py
	fo := float32(math.Atan2(float64(dy), float64(dx)))
	// Skip micro facing updates — spam breaks auto-attack anim and jerks movement.
	d := fo - po
	for d > math.Pi {
		d -= 2 * math.Pi
	}
	for d < -math.Pi {
		d += 2 * math.Pi
	}
	if math.Abs(float64(d)) < 0.35 { // ~20 degrees
		return true
	}
	// While pathing, keep FORWARD flag; standing melee uses plain set facing.
	if b.movementActive() {
		_ = b.world.SetFacingMoving(fo)
	} else {
		_ = b.world.SetFacing(fo)
	}
	return true
}

func (b *Bot) SetSheathed(state uint32) error {
	return b.world.SetSheathed(state)
}

func (b *Bot) MoveTo(x, y, z float32) error {
	b.moveToPoint(x, y, z)
	if b.lastMoveNoPath {
		return fmt.Errorf("no navmesh path; walking a straight line")
	}
	return nil
}

func (b *Bot) StopMoving() error {
	if !b.movementActive() {
		// Already stopped — do not re-send MoveStop (interrupts attack anim / idles).
		return nil
	}
	b.stopCurrentMove()
	return nil
}

func (b *Bot) AttackTarget(guid uint64) error {
	if b.world == nil {
		return nil
	}
	// Avoid CMSG_SET_SELECTION spam when already on this target.
	if b.world.TargetGUID() != guid {
		_ = b.world.SetTarget(guid)
	}
	// Keep auto-attack alive: re-issue ATTACKSWING at most every 1.5s while on
	// the same target. Calling only once often loses server-side swing after
	// range/facing glitches or MoveStop, so the client shows no attack anim.
	if b.world.AttackingGUID() == guid && !b.lastAttackSwingAt.IsZero() &&
		time.Since(b.lastAttackSwingAt) < 1500*time.Millisecond {
		return nil
	}
	b.logDecision("ATTACK_SWING guid=%d", guid)
	b.lastAttackSwingAt = time.Now()
	return b.world.AttackSwing(guid)
}

func (b *Bot) StopAttack() error {
	return b.world.AttackStop()
}

func (b *Bot) SetTarget(guid uint64) error {
	if b.world != nil && b.world.TargetGUID() == guid {
		return nil
	}
	return b.world.SetTarget(guid)
}

// IsMoving reports whether the movement controller is mid-path.
func (b *Bot) IsMoving() bool {
	return b.movementActive()
}

func (b *Bot) CastSpell(spellID uint32, targetGUID uint64) error {
	b.logDecision("CAST_SPELL id=%d target=%d", spellID, targetGUID)
	// Do NOT abort the chase path on cast attempt. Charge often CAST_FAILs
	// (stance/range/path); aborting here froze bots mid-pull so they only
	// swung NOT_IN_RANGE. Path is dropped on SPELL_GO success (relocate spells)
	// and on self MONSTER_MOVE (OnServerRelocate).
	if b.agent != nil {
		b.agent.castSent(spellID)
	}
	return b.world.CastSpell(spellID, targetGUID)
}

func (b *Bot) IsSpellReady(spellID uint32) bool {
	b.noPowerMu.Lock()
	if b.noPowerUntil != nil {
		if until, ok := b.noPowerUntil[spellID]; ok {
			if time.Now().Before(until) {
				b.noPowerMu.Unlock()
				return false
			}
			delete(b.noPowerUntil, spellID)
		}
	}
	b.noPowerMu.Unlock()
	if b.world == nil {
		return false
	}
	// AC doesn't push cooldowns for normal casts, so the world's list misses them; check the ones tracked from SPELL_GO.
	if b.agent != nil && b.agent.cooldownLeft(spellID) > 0 {
		return false
	}
	return b.world.IsSpellReady(spellID)
}

// noteSpellNoPower blocks a spell in IsSpellReady for a short window after
// SMSG_CAST_FAILED reason 85 (SPELL_FAILED_NO_POWER).
func (b *Bot) noteSpellNoPower(spellID uint32) {
	if spellID == 0 {
		return
	}
	b.noPowerMu.Lock()
	if b.noPowerUntil == nil {
		b.noPowerUntil = make(map[uint32]time.Time)
	}
	b.noPowerUntil[spellID] = time.Now().Add(1500 * time.Millisecond)
	b.noPowerMu.Unlock()
}

func (b *Bot) GetHealth() (current, max uint32) {
	return b.world.Health(), b.world.MaxHealth()
}

func (b *Bot) GetPower() (current, max uint32) {
	// world.Power() only tracks mana; read UNIT_FIELD_(MAX)POWER1+type for rage/energy/runic too.
	if o := b.world.GetObject(b.world.CharGUID()); o != nil {
		t := uint16(b.GetPowerType())
		return o.Value(client.UnitFieldPower1 + t), o.Value(client.UnitFieldMaxPower1 + t)
	}
	return b.world.Power()
}

func (b *Bot) GetLevel() uint32 {
	return b.world.PlayerLevel()
}

// GetClass and GetRace read UNIT_FIELD_BYTES_0 (race, class, gender, power type); the config values are only
// used to create a missing character, so they're the fallback before the first update arrives.
func (b *Bot) GetClass() uint8 {
	if o := b.world.GetObject(b.world.CharGUID()); o != nil && o.Value(client.UnitFieldBytes0) != 0 {
		return uint8(o.Value(client.UnitFieldBytes0) >> 8)
	}
	return b.config.Class
}

func (b *Bot) GetOwnGUID() uint64 {
	if b.world != nil {
		return b.world.CharGUID()
	}
	return 0
}

func (b *Bot) GetRace() uint8 {
	if o := b.world.GetObject(b.world.CharGUID()); o != nil && o.Value(client.UnitFieldBytes0) != 0 {
		return uint8(o.Value(client.UnitFieldBytes0))
	}
	return b.config.Race
}

func (b *Bot) GetPowerType() uint8 {
	if o := b.world.GetObject(b.world.CharGUID()); o != nil && o.Value(client.UnitFieldMaxHealth) != 0 {
		return uint8(o.Value(client.UnitFieldBytes0) >> 24) // UNIT_FIELD_BYTES_0 byte 3 = power type
	}
	// Common power types: 0=mana, 1=rage, 2=focus, 3=energy, 6=runic power
	// For simplicity, derive from class. Can be improved by reading UnitFieldBytes0.
	switch b.config.Class {
	case 1: // Warrior
		return 1 // rage
	case 2: // Paladin
		return 0 // mana
	case 3: // Hunter
		return 2 // focus (in WotLK)
	case 4: // Rogue
		return 3 // energy
	case 5: // Priest
		return 0
	case 6: // DK
		return 6 // runic
	case 7: // Shaman
		return 0
	case 8: // Mage
		return 0
	case 9: // Warlock
		return 0
	case 11: // Druid
		return 0 // or 3 in cat, but default mana
	default:
		return 0
	}
}

func (b *Bot) InCombat() bool {
	// The server's UNIT_FLAG_IN_COMBAT; the client-side flag went stale after fights.
	if o := b.world.GetObject(b.world.CharGUID()); o != nil && o.Value(client.UnitFieldMaxHealth) != 0 {
		return o.Value(client.UnitFieldFlags)&0x80000 != 0
	}
	return b.world.InCombat()
}

// IsGhost: PLAYER_FLAGS & PLAYER_FLAGS_GHOST (released spirit; health reads 1, not 0).
func (b *Bot) IsGhost() bool {
	o := b.world.GetObject(b.world.CharGUID())
	return o != nil && o.Value(0x96)&0x10 != 0
}

func (b *Bot) IsAlive() bool {
	h := b.world.Health()
	mh := b.world.MaxHealth()
	// If we haven't received health data yet, assume alive
	if h == 0 && mh == 0 {
		return true
	}
	if h == 0 || b.IsGhost() {
		return false
	}
	// Also check death count - if deaths > 0 and health is still 0, we're dead
	return h > 0
}

func (b *Bot) GetTargetGUID() uint64 {
	return b.world.TargetGUID()
}

func (b *Bot) GetNearbyUnits(maxDist float32) []UnitInfo {
	objects := b.world.GetNearbyUnits(maxDist)
	result := make([]UnitInfo, 0, len(objects))
	for _, obj := range objects {
		u := b.worldObjToUnitInfo(obj)
		if u.Name == "" && u.IsPlayer && b.playerName != nil {
			u.Name = b.playerName(obj.GUID)
		}
		result = append(result, u)
	}
	return result
}

func (b *Bot) GetNearbyPlayers(maxDist float32) []UnitInfo {
	objects := b.world.GetNearbyPlayers(maxDist)
	result := make([]UnitInfo, 0, len(objects))
	for _, obj := range objects {
		u := b.worldObjToUnitInfo(obj)
		if u.Name == "" && b.playerName != nil {
			u.Name = b.playerName(obj.GUID)
		}
		result = append(result, u)
	}
	return result
}

func (b *Bot) GetUnitInfo(guid uint64) *UnitInfo {
	obj := b.world.GetObject(guid)
	if obj == nil {
		return nil
	}
	info := b.worldObjToUnitInfo(obj)
	if info.Name == "" && info.IsPlayer && b.playerName != nil {
		info.Name = b.playerName(guid)
	}
	return &info
}

func (b *Bot) worldObjToUnitInfo(obj *client.WorldObject) UnitInfo {
	// Always expose the *current* estimated pose. Raw Pos* is the create/MONSTER_MOVE
	// segment start; chasing it is the classic "run to where the mob used to be" bug.
	px, py, pz := obj.InterpolatedPosition()
	mx, my, mz := b.myPos()
	dx, dy, dz := px-mx, py-my, pz-mz
	dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
	dyn := obj.Value(client.UnitDynamicFlags)
	return UnitInfo{
		GUID:      obj.GUID,
		Entry:     obj.Entry,
		Health:    obj.Health(),
		MaxHealth: obj.MaxHealth(),
		Level:     obj.Level(),
		PosX:      px,
		PosY:      py,
		PosZ:      pz,
		IsAlive:   obj.IsAlive(),
		IsPlayer:  obj.IsPlayer,
		Distance:  dist,
		Name:      b.objName(obj),
		Faction:   obj.Value(client.UnitFieldFaction),
		NPCFlags:  obj.Value(client.UnitNPCFlags),
		Flags:     obj.Value(client.UnitFieldFlags),
		DynFlags:  dyn,
		Lootable:  dyn&client.UnitDynflagLootable != 0,
		Target:    uint64(obj.Value(client.UnitFieldTarget)) | uint64(obj.Value(client.UnitFieldTarget+1))<<32,
	}
}

func (b *Bot) SendChat(message string) error {
	return b.world.SendChatMessage(client.ChatMsgSay, b.chatLang(), message) // leeroy: was always Common
}

func (b *Bot) SendCommand(command string) error {
	return b.world.SendGMCommand(command)
}

func (b *Bot) SendGuildCommand(command string) error {
	if b.world == nil {
		return nil
	}
	return b.world.SendGuildCommand(command)
}

func (b *Bot) Loot(guid uint64) error {
	return b.world.Loot(guid)
}

// LootAll opens loot, takes money, and releases without blocking the AI tick.
// Previously this slept 700ms on the AI goroutine, which froze ticks and caused
// loot spam under Lua grind (validation run: 122× CMSG_LOOT in ~45s).
// Server responses arrive asynchronously via SMSG_LOOT_RESPONSE (handleLootResponse).
func (b *Bot) LootAll(guid uint64) error {
	if b.world == nil || guid == 0 {
		return nil
	}
	// Throttle: at most one open/release cycle per GUID every 3s.
	now := time.Now()
	b.lootMu.Lock()
	if b.lastLootAttemptGUID == guid && now.Sub(b.lastLootAttemptAt) < 3*time.Second {
		b.lootMu.Unlock()
		return nil
	}
	b.lastLootAttemptGUID = guid
	b.lastLootAttemptAt = now
	b.lootMu.Unlock()

	// Items, money and release happen in handleLootResponse once SMSG_LOOT_RESPONSE arrives; releasing
	// here (before the response) closed the window first, so items were never taken.
	return b.world.Loot(guid)
}

func (b *Bot) Log(format string, args ...interface{}) {
	b.log(format, args...)
}

// Extended BotAPI methods for the Lua AI framework (warrior rends, pet, stance, aura checks, etc.)
func (b *Bot) HasAuraOn(guid uint64, spellID uint32) bool {
	if b.world == nil {
		return false
	}
	obj := b.world.GetObject(guid)
	if obj == nil {
		return false
	}
	return obj.HasAura(spellID)
}

func (b *Bot) CanCast(spellID uint32, targetGUID uint64) bool {
	if b.world == nil {
		return true // optimistic for headless tests
	}
	// Use Bot.IsSpellReady so noPowerUntil (CAST_FAILED NO_POWER) is honored
	// for callers that only check can_cast without a prior is_spell_ready.
	if !b.IsSpellReady(spellID) {
		return false
	}
	// Enough power? Base cost from Spell.dbc, in the same units as GetPower (rage/runic x10). It used to be
	// a warrior-only table checked against the mana-only world.Power(), so rage abilities never passed.
	if sp := spellInfo(spellID); sp != nil && sp.Cost > 0 && sp.PowerType == uint32(b.GetPowerType()) {
		if cur, _ := b.GetPower(); cur < sp.Cost {
			return false
		}
	}
	// aura states the spell needs (UNIT_FIELD_AURASTATE): Victory Rush after a kill, Execute below 20%, Revenge after a block
	if sp := spellInfo(spellID); sp != nil {
		if sp.casterState != 0 && !hasAuraState(b.world.GetObject(b.world.CharGUID()), sp.casterState) {
			return false
		}
		if sp.targetState != 0 && targetGUID != 0 && targetGUID != b.world.CharGUID() && !hasAuraState(b.world.GetObject(targetGUID), sp.targetState) {
			return false
		}
	}
	return true
}

func (b *Bot) GetPetGUID() uint64 {
	if b.world == nil {
		return 0
	}
	if me := b.world.GetObject(b.world.CharGUID()); me != nil { // UNIT_FIELD_SUMMON (OBJECT_END + 0x02)
		return uint64(me.Value(0x08)) | uint64(me.Value(0x09))<<32
	}
	return 0
}

func (b *Bot) PetAttack(target uint64) {
	if b.world != nil {
		b.log("Lua AI: pet attack request on %d", target)
		// Real implementation would send pet action packet if sidecar supports it.
	}
}

func (b *Bot) GetStance() int {
	// Stance/form often in UNIT_FIELD_BYTES_1 or detectable via auras (battle stance etc).
	// Return 0 (default) ; advanced detection can be added.
	return 0
}

func (b *Bot) IsBehindTarget(targetGUID uint64) bool {
	if b.world == nil {
		return false
	}
	if targetGUID == 0 {
		targetGUID = b.GetTargetGUID()
	}
	if targetGUID == 0 {
		return false
	}
	obj := b.world.GetObject(targetGUID)
	if obj == nil {
		return false
	}
	px, py, _, _ := b.GetPosition()
	tx, ty := obj.PosX, obj.PosY
	to := obj.Orientation

	dx := float64(px - tx)
	dy := float64(py - ty)
	angleToPlayer := math.Atan2(dy, dx)
	delta := angleToPlayer - float64(to)
	// normalize to [-pi, pi]
	for delta > math.Pi {
		delta -= 2 * math.Pi
	}
	for delta < -math.Pi {
		delta += 2 * math.Pi
	}
	absd := math.Abs(delta)
	// Behind if in the rear 180 deg arc (abs delta > pi/2 from target's forward)
	return absd > (math.Pi / 2)
}

// ValidationMode satisfies the script.BotAPI requirement.
// It is the central cheap gate for all heavy validation tooling (structured logs,
// packet traces, ring buffers, detailed aura data, etc.).
// When false (the default for regular operation), callers must do almost no work.
func (b *Bot) ValidationMode() bool {
	return b.config.ValidationMode
}

// markKnownDead marks this GUID as dead in *this bot's* private view of the world.
// This persists even if server packets keep sending positive health (stale cache from death not fully propagated to this connection).
func (b *Bot) markKnownDead(guid uint64) {
	b.knownDeadMu.Lock()
	if b.knownDead == nil {
		b.knownDead = make(map[uint64]bool)
	}
	b.knownDead[guid] = true
	b.knownDeadMu.Unlock()
	b.world.MarkObjectDead(guid) // also force live cache for this bot's worldclient
}

// isKnownDead returns true if this bot has decided the guid is dead in its view.
func (b *Bot) isKnownDead(guid uint64) bool {
	b.knownDeadMu.Lock()
	defer b.knownDeadMu.Unlock()
	return b.knownDead != nil && b.knownDead[guid]
}

// clearKnownDead if we see evidence it's alive (positive health update).
func (b *Bot) clearKnownDead(guid uint64) {
	b.knownDeadMu.Lock()
	if b.knownDead != nil {
		delete(b.knownDead, guid)
	}
	b.knownDeadMu.Unlock()
}

// ============================================================
// Utility methods
// ============================================================

// Stop signals the bot to stop running.
// Engine returns the session's Lua engine, or nil until the character is in the world.
func (b *Bot) Engine() *script.Engine {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lua
}

// Name is the character this session plays.
func (b *Bot) Name() string { return b.config.CharacterName }

func (b *Bot) Stop() {
	select {
	case <-b.stopCh:
	default:
		close(b.stopCh)
	}
}

func (b *Bot) LoadLuaScript(code string) error {
	if b.lua == nil {
		return fmt.Errorf("lua engine not initialized")
	}
	return b.lua.DoString(code)
}

func (b *Bot) Events() []BotEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]BotEvent, len(b.events))
	copy(result, b.events)
	return result
}

func (b *Bot) setStatus(s BotStatus) {
	b.mu.Lock()
	b.status = s
	b.mu.Unlock()
}

// Status returns current status
func (b *Bot) Status() BotResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := BotResult{
		ID:     b.id,
		Status: b.status,
		Kills:  b.kills,
		Deaths: b.deaths,
	}
	if b.err != nil {
		result.Error = b.err.Error()
	}
	if b.world != nil {
		result.Level = b.world.PlayerLevel()
	}
	return result
}

func (b *Bot) fail(format string, args ...interface{}) BotResult {
	msg := fmt.Sprintf(format, args...)
	b.log("ERROR: %s", msg)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = BotStatusError
	b.err = fmt.Errorf("%s", msg)
	if b.world != nil {
		b.world.Close()
	}
	return BotResult{
		ID:     b.id,
		Status: BotStatusError,
		Error:  msg,
	}
}

// log writes a line to the session's output (the agent's session.log), stamped with the time of day so it lines up
// with the events (their t is the same clock).
func (b *Bot) log(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	// Combat thrash lines only in validation / observation / packet-trace —
	// army load must not fmt.Printf every melee tick.
	if strings.HasPrefix(msg, "ATTACK ") && !b.verbose() {
		return
	}
	fmt.Printf("%s [Bot %s] %s\n", time.Now().Format("15:04:05"), b.id, msg)
}

// slowTick reports a tick that held the game up: a line each time, and a slow_tick event at most every 30 s (with
// how many there were), naming the task that took longest.
func (b *Bot) slowTick(d time.Duration) {
	ev := types.SlowTick{MS: int(d.Milliseconds())}
	if b.lua != nil {
		task, td := b.lua.SlowestTask()
		ev.Task, ev.TaskMS = task, int(td.Milliseconds())
	}
	if ev.Task != "" {
		b.log("script tick took %dms (task %s %dms)", ev.MS, ev.Task, ev.TaskMS)
	} else {
		b.log("script tick took %dms", ev.MS)
	}
	b.slowTicks++
	if time.Since(b.slowEmitted) < 30*time.Second {
		return
	}
	ev.Count, b.slowTicks, b.slowEmitted = b.slowTicks, 0, time.Now()
	b.emit("slow_tick", ev)
}

// debugf is for the client's own bookkeeping (combat flags, swimming, teleports): only when validating or tracing,
// so it doesn't bury what a script logged.
func (b *Bot) debugf(format string, args ...interface{}) {
	if b.verbose() {
		b.log(format, args...)
	}
}

func (b *Bot) verbose() bool {
	return b.config.ValidationMode || b.config.LogDecisionsToChat || b.config.EnablePacketTrace
}

// logDecision logs an important AI/behavior decision both to the console and
// (throttled) as an in-game /say so you can observe what the bot is "thinking"
// while watching it in the world.
func (b *Bot) logDecision(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)

	// Console is unreadable with hundreds/thousands of bots.
	// All decision logging goes to in-game /say chat only.
	// (use b.log(...) explicitly for anything you want in node console)
	if b.config.LogDecisionsToChat {
		if time.Since(b.lastDecisionChat) < 700*time.Millisecond {
			return
		}
		b.lastDecisionChat = time.Now()

		chat := "[AI] " + msg
		if len(chat) > 110 {
			chat = chat[:107] + "..."
		}
		_ = b.world.SendChatMessage(client.ChatMsgSay, client.LangCommon, chat)
	}
}

func (b *Bot) addEvent(eventType, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	b.log("[EVENT:%s] %s", eventType, msg)
	b.mu.Lock()
	b.events = append(b.events, BotEvent{
		Time:    time.Now(),
		Type:    eventType,
		Message: msg,
	})
	b.mu.Unlock()
}
