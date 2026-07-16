package generation

import (
	"fmt"
	"sort"
)

const maximumContributionTokens = 4096

// GenerationPoint identifies one versioned application-integration boundary.
type GenerationPoint string

const (
	// GenerationPointHTTPIngress runs after external request validation and
	// before application invocation preparation.
	GenerationPointHTTPIngress GenerationPoint = "http.ingress"
	// GenerationPointInvocationPrepare runs before canonical Kernel dispatch.
	GenerationPointInvocationPrepare GenerationPoint = "invocation.prepare"
	// GenerationPointInvocationComplete runs after canonical Kernel dispatch.
	GenerationPointInvocationComplete GenerationPoint = "invocation.complete"
	// GenerationPointHTTPEgress runs before external response serialization.
	GenerationPointHTTPEgress GenerationPoint = "http.egress"
)

// ContributionToken names one semantic dependency edge. Tokens carry no
// discovery-order or priority meaning.
type ContributionToken string

// String returns the exact stable token.
func (t ContributionToken) String() string { return string(t) }

// Contribution declares one selected extension's semantic generation unit and
// its explicit ordering dependencies. Namespace and Source identify the exact
// normalized metadata input that caused the contribution.
type Contribution struct {
	ID        string              `json:"id"`
	Namespace string              `json:"namespace"`
	Source    CapabilityID        `json:"source"`
	Point     GenerationPoint     `json:"point"`
	Requires  []ContributionToken `json:"requires"`
	Provides  []ContributionToken `json:"provides"`
	_         struct{}
}

// NormalizedContribution is one immutable dependency declaration ready for
// CLI-owned graph validation. Its canonical serialization position carries no
// semantic execution-order meaning.
type NormalizedContribution struct {
	id        string
	namespace string
	source    CapabilityID
	point     GenerationPoint
	requires  []ContributionToken
	provides  []ContributionToken
}

// ID returns the globally stable contribution identifier.
func (c NormalizedContribution) ID() string { return c.id }

// Namespace returns the interpreted extension namespace.
func (c NormalizedContribution) Namespace() string { return c.namespace }

// Source returns the required canonical Capability whose metadata matched.
func (c NormalizedContribution) Source() CapabilityID { return c.source }

// Point returns the exact versioned application-integration boundary.
func (c NormalizedContribution) Point() GenerationPoint { return c.point }

// Requires returns defensive dependency tokens in canonical order.
func (c NormalizedContribution) Requires() []ContributionToken {
	return append([]ContributionToken{}, c.requires...)
}

// Provides returns defensive dependency tokens in canonical order.
func (c NormalizedContribution) Provides() []ContributionToken {
	return append([]ContributionToken{}, c.provides...)
}

func normalizeContributions(context Context, inputs []Contribution) ([]NormalizedContribution, error) {
	contributions := make([]NormalizedContribution, 0, len(inputs))
	seen := make(map[string]int, len(inputs))
	for index, input := range inputs {
		field := fmt.Sprintf("contributions[%d]", index)
		if !validStableID(input.ID) {
			return nil, invalidOutput("%s.id %q is not a stable lower-kebab identifier", field, input.ID)
		}
		if previous, duplicate := seen[input.ID]; duplicate {
			return nil, invalidOutput("%s.id duplicates contribution %q from contributions[%d]", field, input.ID, previous)
		}
		if err := validateOutputSource(context, field, input.Namespace, input.Source); err != nil {
			return nil, err
		}
		if !validGenerationPoint(input.Point) {
			return nil, invalidOutput("%s.point %q is not supported", field, input.Point)
		}
		requires, err := normalizeContributionTokens(field+".requires", input.Requires)
		if err != nil {
			return nil, err
		}
		provides, err := normalizeContributionTokens(field+".provides", input.Provides)
		if err != nil {
			return nil, err
		}
		if overlap := firstTokenOverlap(requires, provides); overlap != "" {
			return nil, invalidOutput("%s both requires and provides token %q", field, overlap)
		}
		seen[input.ID] = index
		contributions = append(contributions, NormalizedContribution{
			id:        input.ID,
			namespace: input.Namespace,
			source:    input.Source,
			point:     input.Point,
			requires:  requires,
			provides:  provides,
		})
	}
	sort.Slice(contributions, func(left, right int) bool {
		return contributions[left].id < contributions[right].id
	})
	return contributions, nil
}

func normalizeContributionTokens(field string, inputs []ContributionToken) ([]ContributionToken, error) {
	if len(inputs) > maximumContributionTokens {
		return nil, invalidOutput("%s contains %d tokens; maximum is %d", field, len(inputs), maximumContributionTokens)
	}
	tokens := make([]ContributionToken, len(inputs))
	copy(tokens, inputs)
	for index, token := range tokens {
		if !validStableID(token.String()) {
			return nil, invalidOutput("%s[%d] %q is not a stable lower-kebab token", field, index, token)
		}
	}
	sort.Slice(tokens, func(left, right int) bool { return tokens[left] < tokens[right] })
	for index := 1; index < len(tokens); index++ {
		if tokens[index] == tokens[index-1] {
			return nil, invalidOutput("%s duplicates token %q", field, tokens[index])
		}
	}
	return tokens, nil
}

func firstTokenOverlap(left, right []ContributionToken) ContributionToken {
	for leftIndex, rightIndex := 0, 0; leftIndex < len(left) && rightIndex < len(right); {
		switch {
		case left[leftIndex] < right[rightIndex]:
			leftIndex++
		case left[leftIndex] > right[rightIndex]:
			rightIndex++
		default:
			return left[leftIndex]
		}
	}
	return ""
}

func validGenerationPoint(point GenerationPoint) bool {
	switch point {
	case GenerationPointHTTPIngress, GenerationPointInvocationPrepare, GenerationPointInvocationComplete, GenerationPointHTTPEgress:
		return true
	default:
		return false
	}
}
