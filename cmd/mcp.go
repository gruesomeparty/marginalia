package cmd

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/gruesomeparty/marginalia/internal/mcpserver"
)

// newMCPCmd speaks MCP over stdio, so an agent hands a document to a human
// with a tool call instead of shelling out and parsing a startup banner —
// or, with --http, over Streamable HTTP, so an agent on another machine can
// do the same.
//
// Separate from `serve` on purpose: serve blocks on a human, mcp blocks on a
// client, and folding them together would make the flag set incoherent.
func newMCPCmd() *cobra.Command {
	var (
		opts     mcpserver.Options
		httpAddr string
	)
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Speak MCP over stdio (or Streamable HTTP with --http) so agents can start reviews and read feedback as tool calls",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Stdout carries JSON-RPC frames under stdio, so every notice a
			// review server would print goes to stderr instead. One stray
			// banner corrupts the protocol for the whole session. The same
			// holds under --http, for a different reason: a container's
			// stdout is its log stream, and the protocol never touched it.
			opts.Log = cmd.ErrOrStderr()
			token := os.Getenv(mcpserver.TokenEnv)
			if httpAddr != "" {
				// Refuse before anything is resolved or bound: an MCP
				// endpoint off loopback with no credential is not a
				// configuration, it is an open door.
				if err := mcpserver.CheckBind(httpAddr, token); err != nil {
					return err
				}
			}
			srv, err := mcpserver.New(opts)
			if err != nil {
				return advertise(err)
			}
			defer srv.Close()
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if httpAddr == "" {
				return srv.MCP().Run(ctx, &mcp.StdioTransport{})
			}
			ln, err := net.Listen("tcp", httpAddr)
			if err != nil {
				return err
			}
			return srv.ServeHTTP(ctx, ln, token)
		},
	}
	cmd.Flags().StringVar(&opts.Root, "root", "", "confine reviewable paths to this directory (default: $MARGINALIA_MCP_ROOT, else the working directory)")
	cmd.Flags().StringVar(&opts.Host, "host", "127.0.0.1", "host/interface review pages bind to")
	cmd.Flags().StringVar(&httpAddr, "http", "", "serve MCP over Streamable HTTP on this address (e.g. :8080) instead of stdio; off loopback, $"+mcpserver.TokenEnv+" must be set")
	cmd.Flags().StringVar(&opts.Diagrams, "diagrams", "auto", "draw mermaid diagrams: auto (when mermaid-cli is installed), off")
	cmd.Flags().StringVar(&opts.MMDC, "mmdc", "", "path to mermaid-cli (default: mmdc on PATH)")
	return cmd
}
