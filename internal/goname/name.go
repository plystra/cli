// Package goname derives deterministic Go identifiers shared by generated
// capability contracts, clients, providers, and adapters.
package goname

import (
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
)

// Package returns the version-specific package identifier for a capability.
func Package(identifier capabilityid.Identifier) string {
	name := strings.NewReplacer(".", "", "-", "").Replace(identifier.Name())
	return name + "v" + strconv.FormatUint(identifier.Major(), 10)
}

// Operation returns the exported method name derived from the capability's
// final dotted segment.
func Operation(identifier capabilityid.Identifier) string {
	segments := strings.Split(identifier.Name(), ".")
	words := strings.Split(segments[len(segments)-1], "-")
	for index := range words {
		words[index] = ExportedWord(words[index])
	}
	return strings.Join(words, "")
}

// ExportedWord returns one exported Go word while preserving common
// initialisms used by generated API names.
func ExportedWord(value string) string {
	lower := strings.ToLower(value)
	if _, ok := initialisms[lower]; ok {
		return strings.ToUpper(lower)
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

var initialisms = map[string]struct{}{
	"api": {}, "ascii": {}, "cpu": {}, "css": {}, "dns": {}, "eof": {},
	"guid": {}, "html": {}, "http": {}, "https": {}, "id": {}, "ip": {},
	"json": {}, "qps": {}, "ram": {}, "rpc": {}, "sla": {}, "smtp": {},
	"sql": {}, "ssh": {}, "tcp": {}, "tls": {}, "ttl": {}, "udp": {},
	"ui": {}, "uid": {}, "uri": {}, "url": {}, "utf8": {}, "uuid": {},
	"vm": {}, "xml": {}, "xmpp": {}, "xsrf": {}, "xss": {},
}
