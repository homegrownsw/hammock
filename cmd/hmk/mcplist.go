package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/homegrownsw/hammock/internal/daemon"
	"github.com/homegrownsw/hammock/internal/hammockconfig"
)

// listedTool is one tool in `hmk mcp --list-tools`: what the docs site's
// MCP reference is made from.
type listedTool struct {
	Group       string `json:"group"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema,omitempty"`
}

// mcpListTools prints every tool `hmk mcp` can offer, every kind of pane
// on, as JSON, with the server's instructions: for the docs, not agents. It
// needs no daemon and no pane.
func mcpListTools() error {
	ctx := context.Background()
	groups := []struct {
		name     string
		register func(*mcpServer, *mcp.Server)
	}{
		{"panes", func(s *mcpServer, m *mcp.Server) { s.register(m) }},
		{"sessions", func(s *mcpServer, m *mcp.Server) { s.registerSessions(m) }},
		{"schedules", func(s *mcpServer, m *mcp.Server) { s.registerSchedules(m) }},
	}
	for _, k := range hammockconfig.PaneKinds {
		groups = append(groups, struct {
			name     string
			register func(*mcpServer, *mcp.Server)
		}{k, func(s *mcpServer, m *mcp.Server) {
			s.kindTools = map[string][]string{}
			s.registerKind(m, k)
		}})
	}
	var out struct {
		Instructions string       `json:"instructions"`
		Tools        []listedTool `json:"tools"`
	}
	out.Instructions = mcpInstructions(nil)
	for _, g := range groups {
		server := mcp.NewServer(&mcp.Implementation{Name: "hammock", Version: daemon.Version}, nil)
		g.register(&mcpServer{}, server)
		st, ct := mcp.NewInMemoryTransports()
		ss, err := server.Connect(ctx, st, nil)
		if err != nil {
			return err
		}
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "hmk-list"}, nil).Connect(ctx, ct, nil)
		if err != nil {
			return err
		}
		for t, err := range cs.Tools(ctx, nil) {
			if err != nil {
				return err
			}
			out.Tools = append(out.Tools, listedTool{Group: g.name, Name: t.Name, Title: t.Title, Description: t.Description, InputSchema: t.InputSchema})
		}
		cs.Close()
		ss.Close()
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
