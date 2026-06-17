package logs

import "regexp"

// SCP 5k log line parsers. Real v2 patterns are in MeshedRegex.py —
// this is a starter set for Phase 2 to prove the pipeline; Phase 4
// will import the full v2 regex library (or a Go port of it).

// Patterns. The first submatch that yields a Type+Fields wins.
type pattern struct {
	re   *regexp.Regexp
	typ  string
	keys []string
}

var patterns = []pattern{
	{
		// "Player 'Name' (76561198000000000) joined the server"
		re:   regexp.MustCompile(`Player '([^']+)' \((\d{17})\) joined`),
		typ:  "player_join",
		keys: []string{"name", "steamid"},
	},
	{
		// "Player 'Name' (76561198000000000) left the server"
		re:   regexp.MustCompile(`Player '([^']+)' \((\d{17})\) left`),
		typ:  "player_leave",
		keys: []string{"name", "steamid"},
	},
	{
		// "Server is now hosting map: Area-79"
		re:   regexp.MustCompile(`(?i)(?:now hosting|loading) map:?\s+(.+)`),
		typ:  "map_change",
		keys: []string{"map"},
	},
	{
		// "[Chat] Name: message"
		re:   regexp.MustCompile(`\[Chat\]\s+([^:]+):\s+(.*)`),
		typ:  "chat",
		keys: []string{"name", "message"},
	},
}

// ParseLine returns the event type and extracted fields, or "", nil if
// no pattern matched. Cheap regex compile, single pass per line.
func ParseLine(text string) (string, map[string]any) {
	for _, p := range patterns {
		m := p.re.FindStringSubmatch(text)
		if m == nil {
			continue
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
