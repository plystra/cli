package commandschema

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maximumIdentityLength = 1_024
	maximumMessageLength  = 4_096
	maximumArgumentLength = 4_096
)

func validLowerKebab(value string, maximum int) bool {
	if value == "" || len(value) > maximum || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousHyphen := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}

func validToken(value string, maximum int) bool {
	if value == "" || len(value) > maximum || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousSeparator := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousSeparator = false
		case (character == '-' || character == '_' || character == '.') && !previousSeparator:
			previousSeparator = true
		default:
			return false
		}
	}
	return !previousSeparator
}

func validSafeText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validRelativePath(value string, allowDot bool) bool {
	if value == "." {
		return allowDot
	}
	if !validSafeText(value, maximumIdentityLength) || path.IsAbs(value) || path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") || strings.Contains(value, "\\") {
		return false
	}
	return !(len(value) >= 2 && value[1] == ':' && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')))
}

func validInvocationID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	first := value[0]
	if (first < '0' || first > '9') && (first < 'a' || first > 'z') {
		return false
	}
	previousSeparator := false
	for _, character := range []byte(value[1:]) {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousSeparator = false
		case (character == '-' || character == '_' || character == '.') && !previousSeparator:
			previousSeparator = true
		default:
			return false
		}
	}
	return !previousSeparator
}

func containsPlaceholder(value string) bool {
	if strings.Contains(value, "${") || strings.Contains(value, "{{") || strings.Contains(value, "}}") {
		return true
	}
	start := strings.IndexByte(value, '<')
	if start < 0 {
		return false
	}
	return strings.IndexByte(value[start+1:], '>') >= 0
}

func validArgumentVector(arguments []string) bool {
	if len(arguments) == 0 || len(arguments) > 256 {
		return false
	}
	for _, argument := range arguments {
		if argument == "" || len(argument) > maximumArgumentLength || !utf8.ValidString(argument) || strings.ContainsRune(argument, '\x00') || strings.IndexFunc(argument, unicode.IsControl) >= 0 || containsPlaceholder(argument) {
			return false
		}
	}
	return true
}

func cloneStrings(values []string) []string {
	return append([]string(nil), values...)
}
