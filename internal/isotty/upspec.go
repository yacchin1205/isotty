package isotty

import (
	"fmt"
	"strings"
)

// SpecSteps renders the steps of up as spec/Spec/Steps.lean, where Lean
// checks their order.
func SpecSteps() string {
	var before, after []string
	list := &before
	for _, step := range upSteps() {
		switch step.kind {
		case kindPrepare:
			continue
		case kindSpecial:
			if step.special == "restoreT" {
				list = &after
				continue
			}
			*list = append(*list, step.special)
		case kindPerName:
			*list = append(*list, fmt.Sprintf("perName %s %s (fun _ => rfl) (·.%s)", step.perName[0], step.perName[1], step.perName[2]))
		case kindStep:
			term := "step [" + strings.Join(step.base, ", ") + "]"
			if len(step.each) > 0 {
				term += " (each := fun a => [" + strings.Join(step.each, ", ") + "])"
			}
			if step.cond.lean != "" {
				term += " (cond := " + step.cond.lean + ")"
			}
			if step.rerun {
				term += " (rerun := true)"
			}
			*list = append(*list, term)
		}
	}
	render := func(name, doc string, terms []string) string {
		return fmt.Sprintf("/-- %s -/\ndef %s : List (Template Config) :=\n  [ %s\n  ]\n", doc, name, strings.Join(terms, "\n  , "))
	}
	return "-- Generated from the step table in internal/isotty/upsteps.go by\n" +
		"-- `go test ./internal/isotty -run TestSpecSteps -update`. Do not edit.\n" +
		"import Spec.Model\n\n" +
		render("before", "The steps of up before the restore, in order.", before) + "\n" +
		render("after", "The steps of up after the restore, in order.", after)
}
