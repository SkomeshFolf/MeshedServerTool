package logs

import "testing"

// TestParseLine_KnownPatterns exercises every pattern in the
// patterns list to confirm it matches what we expect. This is
// regression coverage for the v2 MeshedRegex.py port — if a
// pattern's regex changes, this test breaks.
func TestParseLine_KnownPatterns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		line    string
		want    string
		wantKey string
		wantVal string
	}{
		// objective
		{"objective", "LogObjectives: Completed Objective MTF_Escort successfully", "objective_completed", "objective", "MTF_Escort"},
		// checkpoint
		{"checkpoint", "LogGameState: Unlocked Checkpoint SCP-CB-01", "checkpoint", "checkpoint", "SCP-CB-01"},
		// player_died
		{"died", "LogBlueprintUserMessages: Player Died", "player_died", "", ""},
		// game_ended
		{"ended", "LogBlueprintUserMessages: Changing Game status to GS_PostGame", "game_ended", "", ""},
		// game_started
		{"started", "LogBlueprintUserMessages: Gamemode started", "game_started", "", ""},
		// next_game (map vote)
		{"next", "Map vote has concluded, travelling to Area-79", "next_game", "map", "Area-79"},
		// game_loading
		{"loading", "LogAIModule: Creating AISystem for world Area-79 (skip TransitionMap)", "game_loading", "map", "Area-79 (skip TransitionMap)"},
		// gamemode_change
		{"gamemode", "LogLoad: Game class is 'GameModeName'", "gamemode_change", "gamemode", "GameModeName"},
		// session_creation
		{"session", "Create session complete", "session_creation", "", ""},
		// entering_idle
		{"idle", "Entering Standby, going to standby map M_ServerDefault.", "entering_idle", "", ""},
		// player_join (UNetDriver)
		{"join_unet", "?Name=alice userId:foo[0xABC123]", "player_join", "name", "alice"},
		// player_leave
		{"leave", "UNetConnection::Close: [UNetConnection] RemoteAddr: 1234567890:", "player_leave", "ip_id", "1234567890"},
		// player_kicked
		{"kicked", "Successfully kicked player 1234567890", "player_kicked", "ip_id", "1234567890"},
		// chat (LogChat)
		{"chat_log", "LogChat: [alice]: hello world", "chat", "name", "alice"},
		// report_notice
		{"report", "Player reported: alice griefed at 12:34", "report_notice", "details", "alice griefed at 12:34"},
		// player_join_steamid (v2 starter)
		{"join_steamid", "Player 'alice' (76561198000000001) joined", "player_join_steamid", "name", "alice"},
		// player_leave_steamid
		{"leave_steamid", "Player 'alice' (76561198000000001) left", "player_leave_steamid", "name", "alice"},
		// map_change (now hosting)
		{"map_now", "Server is now hosting map: Area-79", "map_change", "map", "Area-79"},
		// map_change (loading)
		{"map_load", "Server is loading map: Area-79", "map_change", "map", "Area-79"},
		// chat_simple
		{"chat_simple", "[Chat] alice: hi", "chat_simple", "name", "alice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			typ, fields := ParseLine(tc.line)
			if typ != tc.want {
				t.Errorf("type: got %q, want %q", typ, tc.want)
			}
			if tc.wantKey != "" {
				if fields == nil {
					t.Errorf("fields is nil, want key %q", tc.wantKey)
					return
				}
				if got := fields[tc.wantKey]; got != tc.wantVal {
					t.Errorf("fields[%q]: got %v, want %q", tc.wantKey, got, tc.wantVal)
				}
			}
		})
	}
}

func TestParseLine_UnknownReturnsEmpty(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"random garbage",
		"not a log line",
		"Player who cares (notasteamid) joined",
	}
	for _, line := range cases {
		typ, fields := ParseLine(line)
		if typ != "" {
			t.Errorf("line %q: got type %q, want empty", line, typ)
		}
		if fields != nil {
			t.Errorf("line %q: got fields %v, want nil", line, fields)
		}
	}
}

func TestParseLine_ChatSimpleFieldMessage(t *testing.T) {
	t.Parallel()
	// chat_simple captures the second field as "message".
	_, fields := ParseLine("[Chat] alice: hello there")
	if fields == nil {
		t.Fatal("fields is nil")
	}
	if fields["message"] != "hello there" {
		t.Errorf("message: got %v, want 'hello there'", fields["message"])
	}
}

func TestParseLine_LogChatFieldMessage(t *testing.T) {
	t.Parallel()
	// chat (LogChat) captures second field as "message".
	_, fields := ParseLine("LogChat: [alice]: hello there")
	if fields == nil {
		t.Fatal("fields is nil")
	}
	if fields["message"] != "hello there" {
		t.Errorf("message: got %v, want 'hello there'", fields["message"])
	}
}

func TestParseLine_MapChangeIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	// The map_change regex has (?i) — both casings should match.
	for _, line := range []string{
		"Server is NOW HOSTING map: Area-79",
		"Server is LOADING map: Area-79",
	} {
		typ, _ := ParseLine(line)
		if typ != "map_change" {
			t.Errorf("line %q: got %q, want map_change", line, typ)
		}
	}
}

func TestParseLine_FirstMatchWins(t *testing.T) {
	t.Parallel()
	// A line that could match multiple patterns. The first one
	// in the patterns list should win.
	//
	// The current order in parse.go has objective_completed first.
	// We don't test ordering across the whole list, but we do
	// verify that a line that matches the very first pattern
	// returns that type (and not something later).
	line := "LogObjectives: Completed Objective Foo successfully"
	typ, _ := ParseLine(line)
	if typ != "objective_completed" {
		t.Errorf("first-pattern match: got %q, want objective_completed", typ)
	}
}
