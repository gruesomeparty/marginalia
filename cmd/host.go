package cmd

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/gruesomeparty/marginalia/internal/hosted"
	"github.com/gruesomeparty/marginalia/internal/mcpserver"
)

// StoreEnv names the environment variable that stands in for --store, so a
// container can be configured without a command line.
const StoreEnv = "MARGINALIA_STORE"

// newHostCmd is hosted mode (PRD §5.9): one always-on process serving many
// reviews out of a store, for agents that share no filesystem with the
// reviewer. Additive and opt-in — `serve` and `mcp` are untouched.
func newHostCmd() *cobra.Command {
	var (
		addr      string
		storeURI  string
		publicURL string
	)
	cmd := &cobra.Command{
		Use:   "host",
		Short: "Run one always-on instance that agents publish reviews to over remote MCP",
		Long: `Run hosted mode: one listener serving every review in a store.

  /            the inbox
  /r/<id>/     one review's pages and API, as serve mounts them at /
  /mcp         the MCP tools over Streamable HTTP
  /healthz     200 ok

State lives in --store (` + StoreEnv + `), a file:// directory by default; the
process loads every review from it at startup and is its single writer — run
exactly one instance per store. ` + mcpserver.TokenEnv + ` gates /mcp and is
required on anything but a loopback address.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token := os.Getenv(mcpserver.TokenEnv)
			if err := mcpserver.CheckBind(addr, token); err != nil {
				return err
			}
			if storeURI == "" {
				storeURI = os.Getenv(StoreEnv)
			}
			if storeURI == "" {
				storeURI = hosted.DefaultStoreURI
			}
			store, where, err := hosted.OpenStore(storeURI)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			h, err := hosted.Open(ctx, hosted.Options{
				Store: store, PublicURL: publicURL, Token: token, Log: cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			_, _ = cmd.ErrOrStderr().Write([]byte("marginalia: store " + where + "\n"))
			return h.Serve(ctx, ln)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "address to listen on (e.g. :8080 in a container)")
	cmd.Flags().StringVar(&storeURI, "store", "", "where review state lives: file://<dir> (default: $"+StoreEnv+", else "+hosted.DefaultStoreURI+")")
	cmd.Flags().StringVar(&publicURL, "public-url", "", "origin reviewers and agents reach this host at, e.g. https://review.example (default: derived from --addr, which is only right on a laptop)")
	return cmd
}
