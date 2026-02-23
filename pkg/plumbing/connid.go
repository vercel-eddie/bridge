package plumbing

import "fmt"

// ConnectionID returns a deterministic connection ID in the canonical form
// "<source ip>:<source port>-><dest ip>:<dest port>".
func ConnectionID(sourceIP string, sourcePort int, destIP string, destPort int) string {
	return fmt.Sprintf("%s:%d->%s:%d", sourceIP, sourcePort, destIP, destPort)
}
