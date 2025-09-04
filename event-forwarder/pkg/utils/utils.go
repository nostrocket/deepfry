package utils

import (
	"fmt"
	"sort"
	"strconv"
)

type KindCount struct {
	Kind  int
	Count uint64
}

// SortEventKindsByCount sorts event kinds by count (descending), then by kind (ascending)
func SortEventKindsByCount(eventsByKind map[int]uint64) []KindCount {
	var kindCounts []KindCount
	for kind, count := range eventsByKind {
		kindCounts = append(kindCounts, KindCount{Kind: kind, Count: count})
	}

	// Sort by count descending, then by kind ascending
	sort.Slice(kindCounts, func(i, j int) bool {
		if kindCounts[i].Count == kindCounts[j].Count {
			return kindCounts[i].Kind < kindCounts[j].Kind
		}
		return kindCounts[i].Count > kindCounts[j].Count
	})

	return kindCounts
}

// FormatNumber formats a number with comma separators for readability
func FormatNumber(n uint64) string {
	str := strconv.FormatUint(n, 10)
	if len(str) <= 3 {
		return str
	}

	result := ""
	for i, c := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result += ","
		}
		result += string(c)
	}
	return result
}

// GetKindName returns a human-readable name for a Nostr event kind
func GetKindName(kind int) string {
	switch kind {
	case 0:
		return "Metadata"
	case 1:
		return "Text Note"
	case 2:
		return "Relay List"
	case 3:
		return "Contacts"
	case 4:
		return "DM"
	case 5:
		return "Event Delete"
	case 6:
		return "Repost"
	case 7:
		return "Reaction"
	case 8:
		return "Badge Award"
	default:
		return fmt.Sprintf("Kind %d", kind)
	}
}
