package check

import "plumb/internal/note"

// Result is the outcome for one claim.
type Result struct {
	Claim   note.Claim `json:"claim"`
	Stale   bool       `json:"stale"`
	Reasons []string   `json:"reasons,omitempty"`
}

type handler func(c *Checker, args []string) error

// handlers has one entry per kind in note.Kinds. A new kind is one entry
// here, one entry there, and one file.
var handlers = map[string]handler{
	"exists": (*Checker).exists,
	"absent": (*Checker).absent,
	"grep":   (*Checker).grep,
	"hash":   (*Checker).hash,
	"count":  (*Checker).count,
}

// Verify checks every claim independently. A claim is stale if any of its
// evidence fails, and every failure is reported.
func (c *Checker) Verify(claims []note.Claim) []Result {
	out := make([]Result, 0, len(claims))
	for _, cl := range claims {
		r := Result{Claim: cl}
		if cl.ParseErr != "" {
			r.Stale = true
			r.Reasons = append(r.Reasons, "invalid evidence: "+cl.ParseErr)
		}
		for _, ev := range cl.Evidence {
			if err := c.check(ev); err != nil {
				r.Stale = true
				r.Reasons = append(r.Reasons, err.Error())
			}
		}
		out = append(out, r)
	}
	return out
}

func (c *Checker) check(ev note.Evidence) error {
	h, ok := handlers[ev.Kind]
	if !ok {
		return &unknownKind{ev.Kind}
	}
	return h(c, ev.Args)
}

type unknownKind struct{ kind string }

func (u *unknownKind) Error() string { return "unknown evidence " + u.kind }
