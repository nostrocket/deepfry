package lmdbreader

import (
	"encoding/binary"
	"fmt"

	"github.com/PowerDNS/lmdb-go/lmdb"
	"github.com/klauspost/compress/zstd"
)

// dictCache memoises one zstd.Decoder per dictionary id. Each decoder is
// initialised with WithDecoderDicts so DecodeAll can use it for payloads
// that reference that id.
type dictCache struct {
	decoders map[uint32]*zstd.Decoder
}

func newDictCache() *dictCache {
	return &dictCache{decoders: make(map[uint32]*zstd.Decoder)}
}

func (c *dictCache) decoderFor(dictID uint32, txn *lmdb.Txn, dictDB lmdb.DBI) (*zstd.Decoder, error) {
	if dec, ok := c.decoders[dictID]; ok {
		return dec, nil
	}
	// CompressionDictionary key encoding follows the golpe convention for
	// managed tables with a uint64 primary key. Strfry stores dict ids
	// as uint32 in the EventPayload prefix, which we widen to uint64 for
	// the lookup. Both little-endian; LMDB uses native byte order on the
	// key but values are opaque ubytes.
	var key [8]byte
	binary.LittleEndian.PutUint64(key[:], uint64(dictID))
	val, err := txn.Get(dictDB, key[:])
	if err != nil {
		return nil, fmt.Errorf("get dict id %d: %w", dictID, err)
	}
	// Copy the dict bytes; the LMDB-mapped memory is only valid while
	// the txn is alive, but the decoder may keep a reference.
	dictCopy := make([]byte, len(val))
	copy(dictCopy, val)
	dec, err := zstd.NewReader(nil, zstd.WithDecoderDicts(dictCopy))
	if err != nil {
		return nil, fmt.Errorf("new zstd decoder with dict %d: %w", dictID, err)
	}
	c.decoders[dictID] = dec
	return dec, nil
}

// Close releases every cached decoder. Safe to call multiple times.
func (c *dictCache) Close() {
	for _, d := range c.decoders {
		d.Close()
	}
	c.decoders = nil
}
