package logs

import "regexp"

// v3 port of MeshedRegex.py — full SCP: 5k / SCP Pandemic log patterns.
//
// Phase 2 added a small starter set. Phase 4 brings in the rest:
// objective completions, checkpoints, game start/end, gamemode changes,
// player join/leave variants, chat, and the report-file format.
//
// Patterns are tried in order; the first match wins. Each pattern can
// capture a Type (lowercase_snake) and a map of Fields.

// All values are string | nil; nil Fields are omitted in the response.
type pattern struct {
	re   *regexp.Regexp
	typ  string
	keys []string
}

var patterns = []pattern{
	// ---- Phase 4 additions ----
	{
		// LogObjectives: Completed Objective MTF_Escort successfully
		re:   regexp.MustCompile(`LogObjectives: Completed Objective (.+?) successfully`),
		typ:  "objective_completed",
		keys: []string{"objective"},
	},
	{
		// LogGameState: Unlocked Checkpoint SCP-CB-01
		re:   regexp.MustCompile(`LogGameState: Unlocked Checkpoint (.+)`),
		typ:  "checkpoint",
		keys: []string{"checkpoint"},
	},
	{
		// LogBlueprintUserMessages: Player Died
		re:   regexp.MustCompile(`LogBlueprintUserMessages: Player Died`),
		typ:  "player_died",
		keys: nil,
	},
	{
		// LogBlueprintUserMessages: Changing Game status to GS_PostGame
		re:   regexp.MustCompile(`LogBlueprintUserMessages: Changing Game status to GS_PostGame`),
		typ:  "game_ended",
		keys: nil,
	},
	{
		// LogBlueprintUserMessages: Gamemode started
		re:   regexp.MustCompile(`LogBlueprintUserMessages: Gamemode started`),
		typ:  "game_started",
		keys: nil,
	},
	{
		// Map vote has concluded, travelling to Area-79
		re:   regexp.MustCompile(`Map vote has concluded, travelling to (.+)`),
		typ:  "next_game",
		keys: []string{"map"},
	},
	{
		// LogAIModule: Creating AISystem for world <MapName> (skip TransitionMap)
		re:   regexp.MustCompile(`LogAIModule: Creating AISystem for world (.+)`),
		typ:  "game_loading",
		keys: []string{"map"},
	},
	{
		// LogLoad: Game class is 'GameModeName'
		re:   regexp.MustCompile(`LogLoad: Game class is '(.+?)'`),
		typ:  "gamemode_change",
		keys: []string{"gamemode"},
	},
	{
		re:   regexp.MustCompile(`Create session complete`),
		typ:  "session_creation",
		keys: nil,
	},
	{
		re:   regexp.MustCompile(`Entering Standby, going to standby map M_ServerDefault\.`),
		typ:  "entering_idle",
		keys: nil,
	},
	{
		// v2 player join via UNetDriver URL: ?Name=foo userId:... [0x...]
		// Captures Name and hex userId.
		re:   regexp.MustCompile(`\?Name=(.+?) userId:.*?\[?(0x[0-9A-Fa-f]+)\]?`),
		typ:  "player_join",
		keys: []string{"name", "hex_id"},
	},
	{
		// v2 player leave variants — three alternative forms.
		re:   regexp.MustCompile(`UNetConnection::Close: \[UNetConnection\] RemoteAddr: (\d+):`),
		typ:  "player_leave",
		keys: []string{"ip_id"},
	},
	{
		re:   regexp.MustCompile(`Successfully kicked player (\d+)`),
		typ:  "player_kicked",
		keys: []string{"ip_id"},
	},
	{
		// LogChat: [PlayerName]: message
		re:   regexp.MustCompile(`LogChat: \[(.+)\]: (.+)$`),
		typ:  "chat",
		keys: []string{"name", "message"},
	},
	{
		// Report file format: a separate log line, but for completeness
		// the report-detection is in internal/reports. This pattern
		// catches the in-log "Player X reported Player Y for reason Z"
		// style if the game ever adds it.
		re:   regexp.MustCompile(`Player reported: (.+)`),
		typ:  "report_notice",
		keys: []string{"details"},
	},

	// ---- Phase 2 starter set, kept for backwards compatibility ----
	{
		// Player 'Name' (76561198000000000) joined the server
		re:   regexp.MustCompile(`Player '([^']+)' \((\d{17})\) joined`),
		typ:  "player_join_steamid",
		keys: []string{"name", "steamid"},
	},
	{
		// Player 'Name' (76561198000000000) left the server
		re:   regexp.MustCompile(`Player '([^']+)' \((\d{17})\) left`),
		typ:  "player_leave_steamid",
		keys: []string{"name", "steamid"},
	},
	{
		// "Server is now hosting map: Area-79" (or "loading map:")
		re:   regexp.MustCompile(`(?i)(?:now hosting|loading) map:?\s+(.+)`),
		typ:  "map_change",
		keys: []string{"map"},
	},
	{
		// "[Chat] Name: message"
		re:   regexp.MustCompile(`\[Chat\]\s+([^:]+):\s+(.*)`),
		typ:  "chat_simple",
		keys: []string{"name", "message"},
	},
}

// ParseLine returns the event type and extracted fields, or "", nil if
// no pattern matched. The first match wins.
func ParseLine(text string) (string, map[string]any) {
	for _, p := range patterns {
		m := p.re.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		if len(p.keys) == 0 {
			return p.typ, nil
		}
		fields := make(map[string]any, len(p.keys))
		for i, k := range p.keys {
			if i+1 < len(m) {
				fields[k] = m[i+1]
			}
		}
		return p.typ, fields
	}
	return "", nil
}
