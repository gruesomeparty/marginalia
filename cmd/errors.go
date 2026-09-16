package cmd

import (
	"github.com/gruesomeparty/marginalia/internal/session"
)

// The request-feature wording lives in internal/session, because the MCP
// server has to advertise the same loop and cannot import cmd. These are the
// names the CLI code already uses.

func routeSetError(err error) error { return session.RouteSetError(err) }

func advertise(err error) error { return session.Advertise(err) }
