package api

import "github.com/Skomesh/MeshedServerTool/internal/server"

// lookupServer returns the in-memory *server.Server for a given name.
// Used by the logs handler. Returns nil if not found.
func lookupServer(m *server.Manager, name string) *server.Server {
	return m.ServerByName(name)
}
