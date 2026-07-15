package capabilitycreate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRenderSchemaValueBoundsUnicodeOutput(t *testing.T) {
	t.Parallel()

	got := renderSchemaValue(strings.Repeat("界", maximumDifferenceValueRunes), true)
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != maximumDifferenceValueRunes || !strings.HasSuffix(got, "...") {
		t.Fatalf("bounded schema value has %d runes and suffix %q", utf8.RuneCountInString(got), got[len(got)-3:])
	}
}
