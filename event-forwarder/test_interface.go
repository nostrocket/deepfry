package main

import (
	"event-forwarder/pkg/relay"

	"github.com/nbd-wtf/go-nostr"
)

func main() {
	// Test if *nostr.Relay implements relay.Relay interface
	var _ relay.Relay = (*nostr.Relay)(nil)
	println("*nostr.Relay implements relay.Relay interface!")
}
