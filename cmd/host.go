package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

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
exactly one instance per store.

Two kinds of caller, told apart on every request: a human, whose JWT from the
edge (Cloudflare Access sends one) is verified against the issuer's keys and
whose email becomes the author of what they write; and an agent, holding
` + mcpserver.TokenEnv + ` on /mcp, who may answer and claim and never decide.
` + mcpserver.TokenEnv + ` is required on anything but a loopback address.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token := os.Getenv(mcpserver.TokenEnv)
			if err := mcpserver.CheckBind(addr, token); err != nil {
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
			h, err := hosted.Open(ctx, hosted.Options{
				Store: store, PublicURL: publicURL, Token: token, Log: cmd.ErrOrStderr(),
				AgentName: firstOf(agentName, os.Getenv(AgentNameEnv)),
				HumanAuth: humanAuth, DevIdentity: devIdentity,
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
	return cmd
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
