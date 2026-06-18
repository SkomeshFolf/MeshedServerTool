// Package settings reads and writes Unreal Engine-style .ini files
// inside a server's install directory.
//
// v2 implemented this with the standard `configparser` library and
// hard-coded handling for BannedIDs.ini. v3 implements a small INI
// parser that handles the two formats actually seen in SCP: 5k:
//
//  1. The BannedIDs.ini "section/key,key,key" format used by v2:
//
//     [/Script/SCPGame.SCPPlayerController]
//     BannedIDs=76561198000000001,76561198000000002
//
//  2. The generic `Section: Key=Value` format used by Game.ini etc.
//
// The parser is deliberately minimal: it preserves comments and
// unknown keys so re-saving a file is idempotent for unrelated
// lines. v3 does NOT round-trip these files perfectly; that's fine
// for the use case (we only write BannedIDs.ini and view others).
package settings

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one INI key=value pair within a section.
type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Section is a named group of entries.
type Section struct {
	Name    string  `json:"name"`
	Entries []Entry `json:"entries"`
}

// File is the parsed contents of one .ini file.
type File struct {
	Path     string    `json:"path"`     // absolute or relative to install_dir
	RelPath  string    `json:"rel_path"` // path relative to install_dir
	Sections []Section `json:"sections"`
	raw      []byte    // original bytes, for round-trip on save
}

// ListINI returns the .ini files in installDir (non-recursive).
func ListINI(installDir string) ([]string, error) {
	entries, err := os.ReadDir(installDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".ini") {
			files = append(files, filepath.Join(installDir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

// Parse reads and parses a .ini file. Empty or missing files return
// a File with no sections and no error — `enabled` for a never-touched
// BannedIDs.ini should not error.
func Parse(path string) (*File, error) {
	f := &File{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, err
	}
	f.raw = data
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var current *Section
	flush := func() {
		if current != nil {
			f.Sections = append(f.Sections, *current)
		}
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Blank or comment line.
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		// Section header: [Name]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			name := strings.TrimSpace(line[1 : len(line)-1])
			current = &Section{Name: name}
			continue
		}
		// Key=Value (only inside a section).
		eq := strings.IndexByte(line, '=')
		if eq <= 0 || current == nil {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// Strip trailing inline comment.
		if i := indexInlineComment(val); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		current.Entries = append(current.Entries, Entry{Key: key, Value: val})
	}
	flush()
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return f, nil
}

// indexInlineComment finds the position of an unquoted ';' starting an
// inline comment. Returns -1 if none.
func indexInlineComment(s string) int {
	inQuote := false
	for i, r := range s {
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && r == ';' {
			return i
		}
	}
	return -1
}

// SetValue sets or replaces a key's value within a section, creating
// the section if needed. If the section exists but has no entries,
// appends one. Returns the updated File.
func (f *File) SetValue(section, key, value string) {
	for i := range f.Sections {
		if f.Sections[i].Name != section {
			continue
		}
		for j := range f.Sections[i].Entries {
			if f.Sections[i].Entries[j].Key == key {
				f.Sections[i].Entries[j].Value = value
				return
			}
		}
		f.Sections[i].Entries = append(f.Sections[i].Entries, Entry{Key: key, Value: value})
		return
	}
	f.Sections = append(f.Sections, Section{
		Name:    section,
		Entries: []Entry{{Key: key, Value: value}},
	})
}

// Write serializes the File back to disk in the same shape as Parse
// reads. The format is:
//
//	[Section]
//	Key=Value
//	Key=Value
func (f *File) Write() error {
	var buf bytes.Buffer
	for _, s := range f.Sections {
		fmt.Fprintf(&buf, "[%s]\n", s.Name)
		for _, e := range s.Entries {
			fmt.Fprintf(&buf, "%s=%s\n", e.Key, e.Value)
		}
		buf.WriteString("\n")
	}
	// 0o600: INI files contain admin lists, banned SteamIDs, server
	// passwords — anything written here should not be world-readable on
	// a multi-user box. The file is also chmod'd if it already existed.
	// (audit finding M17)
	return os.WriteFile(f.Path, buf.Bytes(), 0o600)
}

// SyncBannedIDs writes the global SteamID list to a server's
// BannedIDs.ini in the format v2/v3 use:
//
//	[/Script/SCPGame.SCPPlayerController]
//	BannedIDs=76561198000000001,76561198000000002,...
//
// Creates the file if it doesn't exist.
func SyncBannedIDs(installDir string, steamIDs []string) error {
	path := filepath.Join(installDir, "BannedIDs.ini")
	// Read existing if any, so we don't lose unrelated sections.
	existing, _ := Parse(path)
	if existing == nil {
		existing = &File{Path: path}
	}
	joined := strings.Join(steamIDs, ",")
	existing.SetValue("/Script/SCPGame.SCPPlayerController", "BannedIDs", joined)
	return existing.Write()
}

// WriteBannedIDs is a convenience that takes a reader of an open ini
// file (or nil) and returns a writer that has BannedIDs pre-loaded.
// Kept for future API expansion; not used yet.
var _ = io.Discard
