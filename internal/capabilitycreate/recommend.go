package capabilitycreate

import (
	"sort"

	"github.com/plystra/cli/internal/capabilityid"
)

// nearbyCapabilities returns the highest visible exact version for each
// typo-like name one edit from a genuinely new requested name. Similar spelling
// is advisory only and never participates in identity or contract equality.
func nearbyCapabilities(reference capabilityid.Reference, visible []capabilityid.Identifier) []capabilityid.Identifier {
	requested := reference.Name()
	if requested == "" {
		return nil
	}
	for _, candidate := range visible {
		if candidate.Name() == requested {
			return nil
		}
	}

	highest := make(map[string]capabilityid.Identifier)
	for _, candidate := range visible {
		if !oneEditApart(requested, candidate.Name()) {
			continue
		}
		current, exists := highest[candidate.Name()]
		if !exists || candidate.Major() > current.Major() {
			highest[candidate.Name()] = candidate
		}
	}
	names := make([]string, 0, len(highest))
	for name := range highest {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]capabilityid.Identifier, len(names))
	for index, name := range names {
		result[index] = highest[name]
	}
	return result
}

func oneEditApart(left, right string) bool {
	if left == right {
		return false
	}
	if len(left) == len(right) {
		first := -1
		second := -1
		for index := range len(left) {
			if left[index] == right[index] {
				continue
			}
			if first < 0 {
				first = index
				continue
			}
			if second < 0 {
				second = index
				continue
			}
			return false
		}
		if second < 0 {
			return first >= 0
		}
		return second == first+1 && left[first] == right[second] && left[second] == right[first]
	}
	if len(left)+1 == len(right) {
		return oneInsertionApart(left, right)
	}
	if len(right)+1 == len(left) {
		return oneInsertionApart(right, left)
	}
	return false
}

func oneInsertionApart(shorter, longer string) bool {
	shortIndex := 0
	longIndex := 0
	skipped := false
	for shortIndex < len(shorter) && longIndex < len(longer) {
		if shorter[shortIndex] == longer[longIndex] {
			shortIndex++
			longIndex++
			continue
		}
		if skipped {
			return false
		}
		skipped = true
		longIndex++
	}
	return true
}
