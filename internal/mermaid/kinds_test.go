package mermaid

import (
	"strings"
	"testing"
)

func check(t *testing.T, d *Diagram, want ...string) {
	t.Helper()
	if got := strings.Join(flat(d.Stmts), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("statements:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

// Issue #153: a message is named by its sender and receiver, a group's
// messages hang under it, and a message's text never splits it.
func TestParseSequence(t *testing.T) {
	d := parse(t, `sequenceDiagram
  participant C as Client
  actor U
  %% the happy path
  C->>+A: POST /orders; retry once
  A-->>-C: 201 Created
  Note right of A: idempotent
  loop every minute
    C-)A: poll
  else on error
    A--xC: 503
  end
  autonumber
`)
	if d.Type != "sequence" {
		t.Errorf("Type = %q", d.Type)
	}
	check(t, d,
		"diagram sequencediagram",
		"participant C",
		"participant U",
		"message C->A",
		"message A->C",
		"note note",
		"group loop",
		"  message C->A",
		"  branch else",
		"  message A->C",
		"autonumber autonumber",
	)
	if m := d.Stmts[3]; m.Comment != "the happy path" || !strings.HasSuffix(m.Text, "retry once") {
		t.Errorf("message %q with comment %q", m.Text, m.Comment)
	}
}

// Transitions are named by what they connect, composite states hold their
// own, and a note that runs to `end note` is one statement.
func TestParseState(t *testing.T) {
	d := parse(t, `stateDiagram-v2
  [*] --> Pending
  Pending --> Paid : charge
  Paid : money received
  state "Shipping it" as Ship {
    [*] --> Packed
    --
    Packed --> Sent
  }
  note right of Paid
    refunds go back here
  end note
  Ship --> [*]
`)
	if d.Type != "state" {
		t.Errorf("Type = %q", d.Type)
	}
	check(t, d,
		"diagram statediagram-v2",
		"transition [*]-->Pending",
		"transition Pending-->Paid",
		"state Paid",
		"state Ship",
		"  transition [*]-->Packed",
		"  concurrency --",
		"  transition Packed-->Sent",
		"note note",
		"transition Ship-->[*]",
	)
}

// A relationship is named by its pair of entities, whichever way its
// cardinality is spelled; an entity's attributes hang under it.
func TestParseER(t *testing.T) {
	d := parse(t, `erDiagram
  CUSTOMER ||--o{ ORDER : places
  ORDER }|..|{ LINE-ITEM : "contains"
  CUSTOMER only one to zero or more INVOICE : receives
  CUSTOMER["Customer"] {
    string name PK "full name"
    string email
  }
  PRODUCT
`)
	if d.Type != "er" {
		t.Errorf("Type = %q", d.Type)
	}
	check(t, d,
		"diagram erdiagram",
		"relationship CUSTOMER--ORDER",
		"relationship ORDER--LINE-ITEM",
		"relationship CUSTOMER--INVOICE",
		"entity CUSTOMER",
		"  attribute name",
		"  attribute email",
		"entity PRODUCT",
	)
}

// A stray closer is kept, not dropped, and an unclosed block keeps what is in it.
func TestParseOtherTolerant(t *testing.T) {
	check(t, parse(t, "sequenceDiagram\n  end\n  opt x\n    A->>B: hi\n"),
		"diagram sequencediagram", "statement end", "group opt", "  message A->B")
	check(t, parse(t, "erDiagram\n  }\n  A {\n    int id\n"),
		"diagram erdiagram", "statement }", "entity A", "  attribute id")
	check(t, parse(t, "sequenceDiagram\n  web-xray->>B: hi\n  B--xweb-xray: no\n"),
		"diagram sequencediagram", "message web-xray->B", "message B->web-xray")
	check(t, parse(t, "sequenceDiagram; A->>B: hi\n"),
		"diagram sequencediagram", "message A->B")
	check(t, parse(t, "stateDiagram\n  note left of A\n  A --> B\n"),
		"diagram statediagram", "note note", "transition A-->B")
}
