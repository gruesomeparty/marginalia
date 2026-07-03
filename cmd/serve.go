package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/suTerminus/marginalia/internal/document"
	"github.com/suTerminus/marginalia/internal/feedback"
	"github.com/suTerminus/marginalia/internal/server"
)

func buildServer(path, host string, port int, open bool, author string) (*server.Server, error) {
	if err := checkSupported(path); err != nil {
		return nil, err
	}
	doc, err := document.Parse(path)
	if err != nil {
		return nil, err
	}
	if author == "" {
		author = defaultAuthor()
	}
	return server.New(server.Options{
		Doc:    doc,
		Store:  feedback.NewStore(path),
		Author: author,
		Host:   host,
		Port:   port,
		Open:   open,
	}), nil
}

func defaultAuthor() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "reviewer"
}

func newServeCmd() *cobra.Command {
	var (
		port   int
		host   string
		open   bool
		author string
	)
	cmd := &cobra.Command{
		Use:   "serve <doc>",
		Short: "Serve a document for block-anchored human review",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := buildServer(args[0], host, port, open, author)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx)
		},
	}
	cmd.Flags().IntVar(&port, "port", 8787, "port to listen on")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "host/interface to bind (set to your Tailscale IP for remote review)")
	cmd.Flags().BoolVar(&open, "open", false, "open the review page in a browser")
	cmd.Flags().StringVar(&author, "author", "", "review author (defaults to $USER)")
	return cmd
}
