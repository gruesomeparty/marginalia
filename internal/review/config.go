// Package review parses the review configuration an agent hands to `serve`:
// how the review is framed, what vocabulary the reviewer answers in, and
// which blocks are not up for comment.
//
// The vocabulary matters more than it looks. Most review feedback is a
// verdict, not prose — and if saying "nit" or "blocker" means opening a
// composer and typing, a forty-block document is a slog and the reviewer
// stops being thorough. So an action with nothing to fill in is one tap,
// which is the difference on a phone between a review that gets finished and
// one that gets abandoned.
package review

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk shape of a review config file:
//
//	title: Ingest spec review
//	instructions: |
//	  Focus on §3 — the retry policy. Ignore prose.
//	actions:
//	  - type: blocker
//	    label: Blocker
//	    key: b
//	  - type: legal
//	    label: Legal review
//	    requires_text: true
//	    fields:
//	      - name: severity
//	        options: [high, low]
//	        required: true
//	readonly:
//	  - "2"
//	require_verdict: false
type Config struct {
	Title        string   `yaml:"title"`
	Instructions string   `yaml:"instructions"`
	Custom       []Action `yaml:"actions"`
	// Builtins keeps comment/suggest_edit/question/approve/reject alongside
	// the configured actions. A review framed as "tag every finding" sets it
	// false, so a bare `comment` button does not compete with the tags.
	Builtins *bool `yaml:"builtins"`
	// ReadOnly names blocks that are shown but cannot be commented on —
	// context the reviewer needs to read but is not being asked about. A
	// pattern matches a block and everything under it, so "2" covers section
	// 2 and "1/3" covers a diagram's statements.
	ReadOnly []string `yaml:"readonly"`
	// RequireVerdict withholds review_done until every commentable block
	// carries at least one event.
	RequireVerdict bool `yaml:"require_verdict"`
}

// Action is one thing the reviewer can say about a block. Type is written to
// the log verbatim, so it is the word the consuming agent will read.
type Action struct {
	Type         string  `yaml:"type" json:"type"`
	Label        string  `yaml:"label" json:"label"`
	Key          string  `yaml:"key,omitempty" json:"key,omitempty"`
	RequiresText bool    `yaml:"requires_text,omitempty" json:"requires_text,omitempty"`
	Fields       []Field `yaml:"fields,omitempty" json:"fields,omitempty"`
}

// Field is an extra value collected with an action — a severity, a category.
// Options make it a choice; without them it is free text.
type Field struct {
	Name     string   `yaml:"name" json:"name"`
	Label    string   `yaml:"label" json:"label"`
	Options  []string `yaml:"options,omitempty" json:"options,omitempty"`
	Required bool     `yaml:"required,omitempty" json:"required,omitempty"`
}

// ident is what a type or field name may look like: it becomes a key in an
// append-only log that other tools read, so it stays boring on purpose.
var ident = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Builtin actions — the compiled-in vocabulary from M1, kept here so the page
// and the server read the same list the configured ones join.
func Builtin() []Action {
	return []Action{
		{Type: "comment", Label: "Comment", RequiresText: true},
		{Type: "suggest_edit", Label: "Suggest edit", RequiresText: true},
		{Type: "question", Label: "Question", RequiresText: true},
		{Type: "approve", Label: "Approve"},
		{Type: "reject", Label: "Reject"},
	}
}

// Default is the review a plain `serve` runs: the built-in vocabulary, no
// framing, nothing read-only.
func Default() *Config { return &Config{} }

// Load reads and validates a review config. Unknown keys are an error: a
// misspelled option that silently does nothing would leave the agent thinking
// it framed a review it did not.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// validate normalizes the config and rejects one that cannot mean what it
// says.
func (c *Config) validate() error {
	c.Title = strings.TrimSpace(c.Title)
	// A block scalar keeps its trailing newline, which a pre-wrap banner
	// would render as an empty line.
	c.Instructions = strings.TrimSpace(c.Instructions)
	if len(c.Custom) == 0 && !c.keepBuiltins() {
		return fmt.Errorf("builtins are off but no actions are configured — the reviewer would have nothing to say")
	}
	// review_done is the protocol's own event, never a review action: an
	// action that wrote it would mark the review finished.
	seenType := map[string]bool{"review_done": true}
	// A custom action may reuse a built-in word only when the built-ins are
	// off; otherwise two buttons would write the same type.
	if c.keepBuiltins() {
		for _, a := range Builtin() {
			seenType[a.Type] = true
		}
	}
	seenKey := map[string]bool{}
	for i := range c.Custom {
		a := &c.Custom[i]
		a.Type = strings.TrimSpace(a.Type)
		if !ident.MatchString(a.Type) {
			return fmt.Errorf("action %q: type must be lower_snake_case, since it is written to the feedback log", a.Type)
		}
		if seenType[a.Type] {
			return fmt.Errorf("action %q: already taken", a.Type)
		}
		seenType[a.Type] = true
		if a.Label == "" {
			a.Label = label(a.Type)
		}
		if a.Key != "" {
			if len([]rune(a.Key)) != 1 {
				return fmt.Errorf("action %q: key must be a single character, got %q", a.Type, a.Key)
			}
			if seenKey[a.Key] {
				return fmt.Errorf("action %q: key %q is already taken", a.Type, a.Key)
			}
			seenKey[a.Key] = true
		}
		if err := validateFields(a); err != nil {
			return err
		}
	}
	for _, pat := range c.ReadOnly {
		if strings.TrimSpace(pat) == "" {
			return fmt.Errorf("readonly: an entry is empty")
		}
	}
	return nil
}

func validateFields(a *Action) error {
	seen := map[string]bool{}
	for i := range a.Fields {
		f := &a.Fields[i]
		f.Name = strings.TrimSpace(f.Name)
		if !ident.MatchString(f.Name) {
			return fmt.Errorf("action %q: field name %q must be lower_snake_case", a.Type, f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("action %q: field %q appears twice", a.Type, f.Name)
		}
		seen[f.Name] = true
		if f.Label == "" {
			f.Label = label(f.Name)
		}
		for _, opt := range f.Options {
			if strings.TrimSpace(opt) == "" {
				return fmt.Errorf("action %q: field %q has an empty option", a.Type, f.Name)
			}
		}
	}
	return nil
}

// label is the button text for a type that did not supply one: `suggest_edit`
// reads as "Suggest edit".
func label(name string) string {
	words := strings.ReplaceAll(name, "_", " ")
	return strings.ToUpper(words[:1]) + words[1:]
}

func (c *Config) keepBuiltins() bool { return c.Builtins == nil || *c.Builtins }

// Actions is the vocabulary the reviewer is offered: the built-ins, unless
// they were turned off, followed by the configured ones in the order they
// were written.
func (c *Config) Actions() []Action {
	var out []Action
	if c.keepBuiltins() {
		out = append(out, Builtin()...)
	}
	return append(out, c.Custom...)
}

// Action returns the configured action for an event type.
func (c *Config) Action(t string) (Action, bool) {
	for _, a := range c.Actions() {
		if a.Type == t {
			return a, true
		}
	}
	return Action{}, false
}

// Locked reports whether a block was handed over as read-only. A pattern
// matches the block it names and everything under it — a section, a list and
// its items, a diagram and its statements — because that is how the paths
// nest and how a reviewer reads "don't comment on section 2".
func (c *Config) Locked(block string) bool {
	for _, pat := range c.ReadOnly {
		pat = strings.TrimSpace(pat)
		if block == pat {
			return true
		}
		if !strings.HasPrefix(block, pat) {
			continue
		}
		switch block[len(pat)] {
		case '/', '.':
			return true
		}
	}
	return false
}

// Validate checks an event against the review's vocabulary: the type must be
// one the agent asked for, text must be there when the action needs words,
// and fields must be declared and — where they are a choice — one of the
// choices. This runs on the server, not just in the page: a stale tab must
// not be able to write vocabulary the agent never configured.
func (c *Config) Validate(typ, text string, fields map[string]string) error {
	action, ok := c.Action(typ)
	if !ok {
		return fmt.Errorf("%q is not an action of this review", typ)
	}
	if action.RequiresText && strings.TrimSpace(text) == "" {
		return fmt.Errorf("%q needs a note", typ)
	}
	declared := make(map[string]Field, len(action.Fields))
	for _, f := range action.Fields {
		declared[f.Name] = f
	}
	for name, value := range fields {
		f, ok := declared[name]
		if !ok {
			return fmt.Errorf("%q has no field %q", typ, name)
		}
		if len(f.Options) > 0 && !contains(f.Options, value) {
			return fmt.Errorf("%q field %q: %q is not one of %s", typ, name, value, strings.Join(f.Options, ", "))
		}
	}
	for _, f := range action.Fields {
		if f.Required && strings.TrimSpace(fields[f.Name]) == "" {
			return fmt.Errorf("%q needs field %q", typ, f.Name)
		}
	}
	return nil
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
