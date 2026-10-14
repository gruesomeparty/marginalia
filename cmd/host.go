package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/gruesomeparty/marginalia/internal/hosted"
	ident "github.com/gruesomeparty/marginalia/internal/identity"
	"github.com/gruesomeparty/marginalia/internal/mcpserver"
)

// Environment variables that stand in for host's flags, so a container can
// be configured without a command line and a secret never rides on one.
const (
	StoreEnv        = "MARGINALIA_STORE"
	HumanIssuerEnv  = "MARGINALIA_HUMAN_JWT_ISSUER"
	HumanAudEnv     = "MARGINALIA_HUMAN_JWT_AUDIENCE"
	HumanJWKSEnv    = "MARGINALIA_HUMAN_JWKS_URL"
	AgentNameEnv    = "MARGINALIA_AGENT_NAME"
	OAuthIssuerEnv  = "MARGINALIA_MCP_OAUTH_ISSUER"
	OAuthAudEnv     = "MARGINALIA_MCP_OAUTH_AUDIENCE"
	OAuthJWKSEnv    = "MARGINALIA_MCP_OAUTH_JWKS_URL"
	devIdentityFlag = "insecure-dev-identity"
)

// newHostCmd is hosted mode (PRD §5.9): one always-on process serving many
// reviews out of a store, for agents that share no filesystem with the
// reviewer. Additive and opt-in — `serve` and `mcp` are untouched.
func newHostCmd() *cobra.Command {
	var (
		addr        string
		storeURI    string
		publicURL   string
		issuer      string
		audience    string
		jwksURL     string
		devIdentity string
		agentName   string
		oauthIssuer string
		oauthAud    string
		oauthJWKS   string
		limits      hosted.Limits
		diagramMode string
		mmdc        string
		notifyURLs  []string
		notifyForm  string
	)
	defaults := hosted.DefaultLimits()
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
exactly one instance per store.

Two kinds of caller, told apart on every request: a human, whose JWT from the
edge (Cloudflare Access sends one) is verified against the issuer's keys and
whose email becomes the author of what they write; and an agent, holding
` + mcpserver.TokenEnv + ` on /mcp, who may answer and claim and never decide.
` + mcpserver.TokenEnv + ` is required on anything but a loopback address.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token := os.Getenv(mcpserver.TokenEnv)
			oauthIssuer = firstOf(oauthIssuer, os.Getenv(OAuthIssuerEnv))
			// An authorization server is a credential for /mcp too: with
			// one configured, the static token is optional off loopback.
			credential := token
			if credential == "" && oauthIssuer != "" {
				credential = oauthIssuer
			}
			if err := mcpserver.CheckBind(addr, credential); err != nil {
				return err
			}
			if devIdentity != "" && !mcpserver.Loopback(addr) {
				return fmt.Errorf("--%s treats every page request as %s and is for a laptop only: bind a loopback address, or configure the edge's JWT instead", devIdentityFlag, devIdentity)
			}
			storeURI = firstOf(storeURI, os.Getenv(StoreEnv), hosted.DefaultStoreURI)
			store, where, err := hosted.OpenStore(storeURI)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			humanAuth, err := humanVerifier(ctx, firstOf(issuer, os.Getenv(HumanIssuerEnv)), firstOf(audience, os.Getenv(HumanAudEnv)), firstOf(jwksURL, os.Getenv(HumanJWKSEnv)))
			if err != nil {
				return err
			}
			oauthAud = firstOf(oauthAud, os.Getenv(OAuthAudEnv))
			var oauth *ident.Verifier
			if oauthIssuer != "" {
				if publicURL == "" && oauthAud == "" {
					return errors.New("--mcp-oauth-issuer needs --public-url (the protected resource is <public-url>/mcp) or an explicit --mcp-oauth-audience")
				}
				jwks := firstOf(oauthJWKS, os.Getenv(OAuthJWKSEnv))
				if jwks == "" {
					if jwks, err = ident.JWKSURLFor(ctx, nil, oauthIssuer); err != nil {
						return err
					}
				}
				aud := oauthAud
				if aud == "" {
					aud = strings.TrimSuffix(publicURL, "/") + "/mcp"
				}
				oauth = &ident.Verifier{Issuer: oauthIssuer, Audience: aud, JWKSURL: jwks}
			}
			if err := limitsFromEnv(&limits); err != nil {
				return err
			}
			drawer, err := diagrams(diagramMode, mmdc)
			if err != nil {
				return err
			}
			notifier, err := notifier(notifyURLs, notifyForm, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			h, err := hosted.Open(ctx, hosted.Options{
				Store: store, PublicURL: publicURL, Token: token, Log: cmd.ErrOrStderr(),
				AgentName: firstOf(agentName, os.Getenv(AgentNameEnv)),
				HumanAuth: humanAuth, DevIdentity: devIdentity,
				OAuth: oauth, OAuthIssuer: oauthIssuer, OAuthAudience: oauthAud,
				Limits: limits, Diagrams: drawer, Notify: notifier,
			})
			if err != nil {
				return err
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			_, _ = cmd.ErrOrStderr().Write([]byte("marginalia: store " + where + "\n"))
			if devIdentity != "" {
				_, _ = cmd.ErrOrStderr().Write([]byte("marginalia: INSECURE: every page request is " + devIdentity + "\n"))
			}
			return h.Serve(ctx, ln)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "address to listen on (e.g. :8080 in a container)")
	cmd.Flags().StringVar(&storeURI, "store", "", "where review state lives: file://<dir> (default: $"+StoreEnv+", else "+hosted.DefaultStoreURI+")")
	cmd.Flags().StringVar(&publicURL, "public-url", "", "origin reviewers and agents reach this host at, e.g. https://review.example (default: derived from --addr, which is only right on a laptop)")
	cmd.Flags().StringVar(&issuer, "human-jwt-issuer", "", "issuer of the edge's JWT on page requests, e.g. https://<team>.cloudflareaccess.com ($"+HumanIssuerEnv+")")
	cmd.Flags().StringVar(&audience, "human-jwt-audience", "", "audience the edge's JWT must carry, e.g. the Access application's AUD tag ($"+HumanAudEnv+")")
	cmd.Flags().StringVar(&jwksURL, "human-jwks-url", "", "where the issuer publishes its keys (default: the Access certs URL, or OpenID discovery) ($"+HumanJWKSEnv+")")
	cmd.Flags().StringVar(&devIdentity, devIdentityFlag, "", "treat every page request as this human; loopback only, for trying it out with no edge")
	cmd.Flags().StringVar(&agentName, "agent-name", "", "identity agents holding the static token write under (default: "+mcpserver.DefaultAgentName+") ($"+AgentNameEnv+")")
	cmd.Flags().StringVar(&oauthIssuer, "mcp-oauth-issuer", "", "OAuth authorization server whose access tokens /mcp accepts, e.g. for the claude.ai connector ($"+OAuthIssuerEnv+")")
	cmd.Flags().StringVar(&oauthAud, "mcp-oauth-audience", "", "audience those tokens must carry (default: <public-url>/mcp) ($"+OAuthAudEnv+")")
	cmd.Flags().StringVar(&oauthJWKS, "mcp-oauth-jwks-url", "", "where that server publishes its keys (default: OpenID discovery) ($"+OAuthJWKSEnv+")")
	cmd.Flags().Int64Var(&limits.DocumentBytes, "limit-document-bytes", 0, fmt.Sprintf("largest document an agent may publish (default %d; $MARGINALIA_LIMIT_DOCUMENT_BYTES)", defaults.DocumentBytes))
	cmd.Flags().IntVar(&limits.DocumentsPerPublish, "limit-documents-per-publish", 0, fmt.Sprintf("most documents in one publish (default %d; $MARGINALIA_LIMIT_DOCUMENTS_PER_PUBLISH)", defaults.DocumentsPerPublish))
	cmd.Flags().IntVar(&limits.RevisionsPerReview, "limit-revisions", 0, fmt.Sprintf("most revisions of one review (default %d; $MARGINALIA_LIMIT_REVISIONS)", defaults.RevisionsPerReview))
	cmd.Flags().IntVar(&limits.PublishesPerHour, "limit-publishes-per-hour", 0, fmt.Sprintf("most publishes per agent identity per hour (default %d; $MARGINALIA_LIMIT_PUBLISHES_PER_HOUR)", defaults.PublishesPerHour))
	cmd.Flags().IntVar(&limits.OpenReviews, "limit-open-reviews", 0, fmt.Sprintf("most reviews not yet marked done (default %d; $MARGINALIA_LIMIT_OPEN_REVIEWS)", defaults.OpenReviews))
	cmd.Flags().Int64Var(&limits.StoredBytes, "limit-stored-bytes", 0, fmt.Sprintf("most bytes held across every review, past which new publishes are refused — never a human's note (default %d; $MARGINALIA_LIMIT_STORED_BYTES)", defaults.StoredBytes))
	cmd.Flags().DurationVar(&limits.RetainDone, "retain-done", 0, fmt.Sprintf("how long a finished review stays after its last activity before it is archived (default %s; $MARGINALIA_RETAIN_DONE)", defaults.RetainDone))
	cmd.Flags().DurationVar(&limits.RetainOpen, "retain-open", 0, fmt.Sprintf("the same for a review never marked done (default %s; $MARGINALIA_RETAIN_OPEN)", defaults.RetainOpen))
	cmd.Flags().StringArrayVar(&notifyURLs, "notify-url", nil, "POST a notification here when an agent publishes, republishes, or replies after you finished — an ntfy topic works as is; repeatable ($"+NotifyURLEnv+", comma-separated)")
	cmd.Flags().StringVar(&notifyForm, "notify-format", "text", "notification body: text (ntfy-ready, with Title and Click headers) or json ({event, review_id, title, agent, url, at})")
	cmd.Flags().StringVar(&diagramMode, "diagrams", "off", "draw mermaid diagrams: off (the default here — an agent's input is not worth a browser per publish), auto (when mermaid-cli is installed)")
	cmd.Flags().StringVar(&mmdc, "mmdc", "", "path to mermaid-cli (default: mmdc on PATH)")
	return cmd
}

// NotifyURLEnv stands in for --notify-url: a comma-separated list.
const NotifyURLEnv = "MARGINALIA_NOTIFY_URL"

// notifier builds the notifier from the flags and the environment, or
// returns nil when nothing is configured.
func notifier(urls []string, format string, log io.Writer) (*hosted.Notifier, error) {
	if len(urls) == 0 {
		for _, u := range strings.Split(os.Getenv(NotifyURLEnv), ",") {
			if u = strings.TrimSpace(u); u != "" {
				urls = append(urls, u)
			}
		}
	}
	if len(urls) == 0 {
		return nil, nil
	}
	switch format {
	case "", "text", "json":
	default:
		return nil, fmt.Errorf("--notify-format %q: use text or json", format)
	}
	for _, u := range urls {
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return nil, fmt.Errorf("--notify-url %q is not an http(s) URL", u)
		}
	}
	return &hosted.Notifier{URLs: urls, Format: format, Log: log}, nil
}

// limitsFromEnv fills unset limits from the MARGINALIA_LIMIT_* and
// MARGINALIA_RETAIN_* variables, so a container can be capped without a
// command line. A flag that was set wins.
func limitsFromEnv(l *hosted.Limits) error {
	i64 := func(dst *int64, name string) error {
		if *dst != 0 {
			return nil
		}
		if v := os.Getenv(name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("%s=%q: %w", name, v, err)
			}
			*dst = n
		}
		return nil
	}
	i := func(dst *int, name string) error {
		var n int64
		if *dst != 0 {
			return nil
		}
		if err := i64(&n, name); err != nil {
			return err
		}
		*dst = int(n)
		return nil
	}
	d := func(dst *time.Duration, name string) error {
		if *dst != 0 {
			return nil
		}
		if v := os.Getenv(name); v != "" {
			dur, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("%s=%q: %w", name, v, err)
			}
			*dst = dur
		}
		return nil
	}
	for _, err := range []error{
		i64(&l.DocumentBytes, "MARGINALIA_LIMIT_DOCUMENT_BYTES"),
		i(&l.DocumentsPerPublish, "MARGINALIA_LIMIT_DOCUMENTS_PER_PUBLISH"),
		i(&l.RevisionsPerReview, "MARGINALIA_LIMIT_REVISIONS"),
		i(&l.PublishesPerHour, "MARGINALIA_LIMIT_PUBLISHES_PER_HOUR"),
		i(&l.OpenReviews, "MARGINALIA_LIMIT_OPEN_REVIEWS"),
		i64(&l.StoredBytes, "MARGINALIA_LIMIT_STORED_BYTES"),
		d(&l.RetainDone, "MARGINALIA_RETAIN_DONE"),
		d(&l.RetainOpen, "MARGINALIA_RETAIN_OPEN"),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

// humanVerifier builds the edge-JWT verifier from what was configured, or
// returns nil when nothing was: Open decides whether that is allowed.
func humanVerifier(ctx context.Context, issuer, audience, jwksURL string) (*ident.Verifier, error) {
	if issuer == "" && audience == "" && jwksURL == "" {
		return nil, nil
	}
	if issuer == "" || audience == "" {
		return nil, errors.New("the edge's JWT needs both an issuer (--human-jwt-issuer) and an audience (--human-jwt-audience): a token checked against one and not the other is a token from any app of that issuer")
	}
	if jwksURL == "" {
		var err error
		if jwksURL, err = ident.JWKSURLFor(ctx, nil, issuer); err != nil {
			return nil, err
		}
	}
	return &ident.Verifier{Issuer: issuer, Audience: audience, JWKSURL: jwksURL}, nil
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
