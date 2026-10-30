package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/gruesomeparty/marginalia/internal/diagram"
	"github.com/gruesomeparty/marginalia/internal/review"
	"github.com/gruesomeparty/marginalia/internal/server"
	"github.com/gruesomeparty/marginalia/internal/session"
)

// serveOptions, buildServer and their helpers moved to internal/session when
// the MCP server became a second caller: a package cmd cannot be imported, and
// starting a review is the same pipeline whether a flag or a tool call asked
// for it. These are the thin aliases the CLI still speaks in.
type serveOptions = session.Options

func buildServer(paths []string, opts serveOptions) (*server.Server, error) {
	return session.Build(paths, opts)
}

func diagrams(mode, bin string) (*diagram.Renderer, error) { return session.Diagrams(mode, bin) }

func reviewConfig(preset, path string) (*review.Config, error) {
	return session.ReviewConfig(preset, path)
}

func presetList() string { return session.PresetList() }

func themeList() string { return session.ThemeList() }

func defaultAuthor() string { return session.DefaultAuthor() }

// requirePaths rejects an empty argument list with an error that says what to
// type. Deliberately not routed through advertise(): the request-feature
// pointer is for capability gaps — a format we cannot render, a flag that does
// not exist — and "you forgot the path" is a usage mistake. Sending those to
// the feature tracker would fill the queue with noise, the same reason a
// missing file does not advertise either.
func requirePaths(verb string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) > 0 {
			return nil
		}
		// Share mode's two verbs take something other than a set of
		// documents, so they say so rather than inheriting the wrong shape.
		switch verb {
		case "export":
			return errors.New("export needs a document — e.g. `marginalia export spec.md -o review.html`")
		case "import":
			return errors.New("import needs a review file — e.g. `marginalia import review.json`")
		}
		return fmt.Errorf("%s needs at least one document or directory — e.g. `marginalia %s spec.md` or `marginalia %s docs/`", verb, verb, verb)
	}
}

func newServeCmd() *cobra.Command {
	var (
		opts        serveOptions
		port        int
		host        string
		open        bool
		watch       bool
		author      string
		config      string
		preset      string
		theme       string
		diagramMode string
		mmdc        string
		untilDone   bool
		timeout     time.Duration
	)
	cmd := &cobra.Command{
		Use:   "serve <doc|dir>...",
		Short: "Serve one or more documents for block-anchored human review",
		Args:  requirePaths("serve"),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts = serveOptions{
				Host: host, Port: port, Open: open, Watch: watch, Author: author,
				Config: config, Review: preset, Theme: theme, Diagrams: diagramMode, MMDC: mmdc,
			}
			srv, err := buildServer(args, opts)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serveUntil(ctx, srv, untilDone, timeout, cmd.OutOrStdout())
		},
	}
	cmd.Flags().IntVar(&port, "port", 8787, "port to listen on")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "host/interface to bind (set to your Tailscale IP for remote review)")
	cmd.Flags().BoolVar(&open, "open", false, "open the review page in a browser")
	cmd.Flags().BoolVar(&watch, "watch", false, "re-parse a document when its file changes; the page offers a reload")
	cmd.Flags().StringVar(&author, "author", "", "review author (defaults to $USER)")
	cmd.Flags().StringVar(&config, "config", "", "review config: instructions, custom actions, read-only blocks (YAML)")
	cmd.Flags().StringVar(&preset, "review", "", "shipped review framing to start from: "+presetList())
	cmd.Flags().StringVar(&theme, "theme", "", "page palette: "+themeList())
	cmd.Flags().StringVar(&diagramMode, "diagrams", "auto", "draw mermaid diagrams: auto (when mermaid-cli is installed), off")
	cmd.Flags().StringVar(&mmdc, "mmdc", "", "path to mermaid-cli (default: mmdc on PATH)")
	cmd.Flags().BoolVar(&untilDone, "until-done", false, "exit 0 once every served document is marked done")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, fmt.Sprintf("stop serving after this long (e.g. 30m) and exit %d", exitTimeout))
	return cmd
}

// exitTimeout is what `serve --timeout` exits with when the deadline passes:
// GNU timeout's code, so a script can tell "the human never finished" from
// both success and a failure.
const exitTimeout = 124

var errReviewDone = errors.New("review done")

// serveUntil runs the server until it is interrupted, every document is
// marked done (with untilDone), or the timeout passes. Either way Run drains
// before returning, so a comment saved in the last instant is on disk before
// the exit code is.
func serveUntil(ctx context.Context, srv *server.Server, untilDone bool, timeout time.Duration, out io.Writer) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	ctx, finish := context.WithCancelCause(ctx)
	defer finish(nil)
	if untilDone {
		_, _ = fmt.Fprintln(out, "marginalia: will exit once every document is marked done")
		go func() {
			select {
			case <-srv.Done():
				finish(errReviewDone)
			case <-ctx.Done():
			}
		}()
	}
	if err := srv.Run(ctx); err != nil {
		return err
	}
	done, total := srv.Progress()
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, errReviewDone):
		_, _ = fmt.Fprintf(out, "marginalia: review done (%d of %d documents)\n", done, total)
		return nil
	case timeout > 0 && errors.Is(cause, context.DeadlineExceeded):
		return exitError{code: exitTimeout, err: fmt.Errorf("timed out after %s with %d of %d documents marked done", timeout, done, total)}
	}
	return nil
}
