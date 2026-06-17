package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// ini returns the contents of a representative .ini file covering the
// formats the parser is supposed to handle: section headers, blank
// lines, full-line comments (both ';' and '#'), and key=value entries.
const ini = `; leading comment about the file
# alternate comment style

[Server]
MaxPlayers=32
Port=7777
Hostname=My Server ; inline comment with semicolon
MOTD="Hello, world"

[/Script/SCPGame.SCPPlayerController]
BannedIDs=76561198000000001,76561198000000002
`

func writeTempINI(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ini")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write temp ini: %v", err)
	}
	return path
}

func findSection(f *File, name string) *Section {
	for i := range f.Sections {
		if f.Sections[i].Name == name {
			return &f.Sections[i]
		}
	}
	return nil
}

func entryValue(s *Section, key string) (string, bool) {
	if s == nil {
		return "", false
	}
	for _, e := range s.Entries {
		if e.Key == key {
			return e.Value, true
		}
	}
	return "", false
}

func TestParse_BasicSectionsAndEntries(t *testing.T) {
	path := writeTempINI(t, ini)
	f, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got, want := len(f.Sections), 2; got != want {
		t.Fatalf("got %d sections, want %d (got=%+v)", got, want, f.Sections)
	}

	srv := findSection(f, "Server")
	if srv == nil {
		t.Fatal("missing [Server] section")
	}
	if v, ok := entryValue(srv, "MaxPlayers"); !ok || v != "32" {
		t.Errorf("Server.MaxPlayers = %q (ok=%v), want 32", v, ok)
	}
	if v, ok := entryValue(srv, "Port"); !ok || v != "7777" {
		t.Errorf("Server.Port = %q (ok=%v), want 7777", v, ok)
	}
	// Inline comment is stripped — only the value remains.
	if v, ok := entryValue(srv, "Hostname"); !ok || v != "My Server" {
		t.Errorf("Server.Hostname = %q (ok=%v), want %q (inline comment must be stripped)", v, ok, "My Server")
	}
	// Quoted value keeps its quotes (the parser doesn't unquote; callers do).
	if v, ok := entryValue(srv, "MOTD"); !ok || v != `"Hello, world"` {
		t.Errorf("Server.MOTD = %q (ok=%v), want %q", v, ok, `"Hello, world"`)
	}

	pc := findSection(f, "/Script/SCPGame.SCPPlayerController")
	if pc == nil {
		t.Fatal("missing [/Script/SCPGame.SCPPlayerController] section")
	}
	if v, ok := entryValue(pc, "BannedIDs"); !ok || v != "76561198000000001,76561198000000002" {
		t.Errorf("PC.BannedIDs = %q (ok=%v), want 76561198000000001,76561198000000002", v, ok)
	}
}

func TestParse_MissingFileReturnsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.ini")
	f, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse on missing file: %v", err)
	}
	if len(f.Sections) != 0 {
		t.Errorf("missing-file Parse returned %d sections, want 0", len(f.Sections))
	}
	if f.Path != path {
		t.Errorf("f.Path = %q, want %q", f.Path, path)
	}
}

func TestParse_InlineComment(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		// The v2-compatible behaviour: the first unquoted ';' starts
		// a comment, and everything from it onward is stripped (with
		// surrounding whitespace trimmed). The task spec example:
		// "Key=Value ; this is a comment" → "Value".
		{"semicolon_after_space", "Key=Value ; this is a comment", "Value"},
		{"trailing_spaces", "Key=Value   ;  spaced comment  ", "Value"},
		{"semicolon_inside_quotes_preserved", `Key="v;1" ; comment`, `"v;1"`},
		{"multiple_semicolons_keep_only_first_segment", "Key=Value;more;stuff", "Value"},
		{"no_comment_present", "Key=Value", "Value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempINI(t, "[S]\n"+tc.line+"\n")
			f, err := Parse(path)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			s := findSection(f, "S")
			if s == nil {
				t.Fatal("missing [S] section")
			}
			v, ok := entryValue(s, "Key")
			if !ok {
				t.Fatalf("Key not present in section")
			}
			if v != tc.want {
				t.Errorf("Key value = %q, want %q", v, tc.want)
			}
		})
	}
}

func TestSetValue_UpdatesExistingKey(t *testing.T) {
	path := writeTempINI(t, ini)
	f, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	f.SetValue("Server", "Port", "8888")

	srv := findSection(f, "Server")
	if v, _ := entryValue(srv, "Port"); v != "8888" {
		t.Errorf("after SetValue, Server.Port = %q, want 8888", v)
	}
	// Other keys preserved.
	if v, _ := entryValue(srv, "MaxPlayers"); v != "32" {
		t.Errorf("Server.MaxPlayers mutated: got %q, want 32", v)
	}
}

func TestSetValue_CreatesSectionAndKey(t *testing.T) {
	f := &File{Path: filepath.Join(t.TempDir(), "f.ini")}
	f.SetValue("New", "Alpha", "1")

	s := findSection(f, "New")
	if s == nil {
		t.Fatal("SetValue did not create section")
	}
	if v, _ := entryValue(s, "Alpha"); v != "1" {
		t.Errorf("New.Alpha = %q, want 1", v)
	}
}

func TestWriteAndReparse_RoundTrip(t *testing.T) {
	path := writeTempINI(t, ini)
	f, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Mutate one value and add a brand new section/key.
	f.SetValue("Server", "Port", "8888")
	f.SetValue("NewSection", "Added", "yes")

	// Path defaults to where we read from; reuse it for the write.
	f.Path = path
	if err := f.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Reparse from disk and verify the change persisted.
	f2, err := Parse(path)
	if err != nil {
		t.Fatalf("re-Parse: %v", err)
	}
	if v, _ := entryValue(findSection(f2, "Server"), "Port"); v != "8888" {
		t.Errorf("after round-trip, Server.Port = %q, want 8888", v)
	}
	if v, _ := entryValue(findSection(f2, "NewSection"), "Added"); v != "yes" {
		t.Errorf("after round-trip, NewSection.Added = %q, want yes", v)
	}
	// Untouched key still there.
	if v, _ := entryValue(findSection(f2, "Server"), "MaxPlayers"); v != "32" {
		t.Errorf("after round-trip, Server.MaxPlayers = %q, want 32", v)
	}
}

func TestSyncBannedIDs_WritesV2Format(t *testing.T) {
	dir := t.TempDir()
	ids := []string{"76561198000000001", "76561198000000002", "76561198000000003"}
	if err := SyncBannedIDs(dir, ids); err != nil {
		t.Fatalf("SyncBannedIDs: %v", err)
	}
	path := filepath.Join(dir, "BannedIDs.ini")
	f, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse written BannedIDs.ini: %v", err)
	}
	s := findSection(f, "/Script/SCPGame.SCPPlayerController")
	if s == nil {
		t.Fatal("missing [/Script/SCPGame.SCPPlayerController] section")
	}
	v, ok := entryValue(s, "BannedIDs")
	if !ok {
		t.Fatal("missing BannedIDs entry")
	}
	want := "76561198000000001,76561198000000002,76561198000000003"
	if v != want {
		t.Errorf("BannedIDs = %q, want %q", v, want)
	}
}
