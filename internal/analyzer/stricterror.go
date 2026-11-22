package analyzer

import (
	"fmt"
	"strings"
)

// StrictPricingError aggregates every node whose price could only be estimated
// in strict mode. It is returned after all nodes are priced so the user sees
// the full list at once instead of one warning per node.
type StrictPricingError struct {
	Failures []error
}

func (e *StrictPricingError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "strict pricing: %d node(s) have no exact price:", len(e.Failures))
	for _, f := range e.Failures {
		b.WriteString("\n  - ")
		b.WriteString(f.Error())
	}
	return b.String()
}

func (e *StrictPricingError) Unwrap() []error { return e.Failures }
