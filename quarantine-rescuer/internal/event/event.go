// Package event holds the data types passed between the rescuer's
// reader and forwarder packages. Keeping the type here breaks the
// otherwise-circular dependency that would result from either side
// importing the other for it.
package event

// RawEvent is one quarantined Nostr event extracted from the LMDB.
//
// Raw is the verbatim JSON the relay stored, suitable for republishing
// without re-serialisation drift. The other fields are mirrored from
// the JSON for filtering and ordering decisions.
type RawEvent struct {
	ID        string
	PubKey    string
	Kind      int
	CreatedAt int64
	Raw       []byte
}
