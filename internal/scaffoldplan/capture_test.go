package scaffoldplan

import (
	"fmt"
	"strings"
	"testing"
)

func TestPrivateCaptureNestedFormatting(t *testing.T) {
	private := []byte("PRIVATE_CAPTURE_CONTENT")
	writeState := &capturedWrite{path: "private.yaml", after: private, afterExists: true}
	write := CapturedWrite{state: &writeState}
	state := &captureState{request: private, writes: []CapturedWrite{write}, model: "model"}
	captured := Capture{state: &state}
	values := []any{
		captured, write,
		struct{ private Capture }{captured},
		struct{ private CapturedWrite }{write},
		struct{ private any }{captured},
		struct{ private []Capture }{[]Capture{captured}},
		struct{ private [1]CapturedWrite }{[1]CapturedWrite{write}},
		struct{ private map[string]Capture }{map[string]Capture{"capture": captured}},
	}
	decimal := strings.Trim(fmt.Sprint(private), "[]")
	_, hexadecimal, _ := strings.Cut(fmt.Sprintf("%#v", private), "{")
	hexadecimal = strings.TrimSuffix(hexadecimal, "}")
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x", "%X", "%f", "%t", "%c", "%U", "%b", "%o", "%O"} {
		for _, value := range values {
			output := fmt.Sprintf(format, value)
			if strings.Contains(output, "PRIVATE_") || strings.Contains(output, decimal) || strings.Contains(output, hexadecimal) {
				t.Fatalf("nested private formatting bypass for %T with %s", value, format)
			}
		}
	}
}
