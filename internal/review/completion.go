package review

import (
	"fmt"
	"strconv"
	"strings"
)

// Completion rules: when a review as a whole is finished (issue #53). Each
// reviewer's review_done says they are finished; the rule says how many of
// those the review needs before the agent is told to go on.
const (
	CompleteAny    = "any"    // one reviewer's Done is enough (the default, and the old behaviour)
	CompleteAll    = "all"    // every reviewer
	CompleteQuorum = "quorum" // at least N of them, written quorum:N
)

// Completion is a review's completion rule, read off its config.
type Completion struct {
	Rule   string
	Quorum int
	// Reviewers, when the config names them, are who counts: the denominator
	// for "all", and the only people whose Done counts at all. Without them
	// a reviewer is anyone who has said something.
	Reviewers []string
}

// Completion is the config's rule. A config that names none completes on any
// one Done, which is what a review with one reviewer always did.
func (c *Config) Completion() Completion {
	rule, quorum, _ := parseCompletion(c.CompleteWhen)
	return Completion{Rule: rule, Quorum: quorum, Reviewers: c.Reviewers}
}

func parseCompletion(s string) (rule string, quorum int, err error) {
	s = strings.TrimSpace(s)
	switch s {
	case "", CompleteAny:
		return CompleteAny, 0, nil
	case CompleteAll:
		return CompleteAll, 0, nil
	}
	if n, ok := strings.CutPrefix(s, CompleteQuorum+":"); ok {
		q, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil || q < 1 {
			return "", 0, fmt.Errorf("complete_when: quorum needs a whole number of reviewers from 1, as quorum:2")
		}
		return CompleteQuorum, q, nil
	}
	return "", 0, fmt.Errorf("complete_when: %q is not any, all or quorum:N", s)
}

// Tally is how far a review is from complete: how many of the reviewers who
// count have said Done, and how many it needs. finished are the authors of
// review_done events; participants are everyone who has reviewed at all,
// which is who "all" means when the config names nobody.
func (c Completion) Tally(finished, participants []string) (have, need int) {
	counts := func(string) bool { return true }
	pool := participants
	if len(c.Reviewers) > 0 {
		listed := map[string]bool{}
		for _, r := range c.Reviewers {
			listed[r] = true
		}
		counts = func(a string) bool { return listed[a] }
		pool = c.Reviewers
	}
	seen := map[string]bool{}
	for _, a := range finished {
		if counts(a) && !seen[a] {
			seen[a] = true
			have++
		}
	}
	switch c.Rule {
	case CompleteAll:
		// Whoever said Done counts towards "everyone", even with no other
		// event of their own.
		everyone := map[string]bool{}
		for _, a := range pool {
			everyone[a] = true
		}
		for a := range seen {
			everyone[a] = true
		}
		need = max(len(everyone), 1)
	case CompleteQuorum:
		need = c.Quorum
	default:
		need = 1
	}
	return have, need
}

// Met reports whether the review is complete under this rule.
func (c Completion) Met(finished, participants []string) bool {
	have, need := c.Tally(finished, participants)
	return have >= need
}
