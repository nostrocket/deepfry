package whitelist

import (
	"encoding/hex"
	"strings"
	"sync/atomic"
)

type Whitelist struct {
	list atomic.Pointer[map[[32]byte]struct{}]
}

func NewWhiteList(keys [][32]byte) *Whitelist {

	wl := &Whitelist{}

	wl.UpdateKeys(keys)

	return wl
}

func (wl *Whitelist) IsWhitelisted(key string) bool {
	if len(key) != 64 {
		return false
	}
	var k [32]byte
	if _, err := hex.Decode(k[:], []byte(strings.ToLower(key))); err != nil {
		return false
	}
	mp := wl.list.Load()
	if mp == nil {
		return false
	}
	_, ok := (*mp)[k]
	return ok
}

func (wl *Whitelist) UpdateKeys(keys [][32]byte) {
	nm := make(map[[32]byte]struct{}, len(keys))
	for _, k := range keys {
		nm[k] = struct{}{}
	}
	wl.list.Store(&nm)
}
