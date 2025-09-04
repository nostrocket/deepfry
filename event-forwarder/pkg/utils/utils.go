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

// kindNames maps specific event kinds to their English descriptions.
var kindNames = map[int]string{
	0:     "User Metadata",
	1:     "Short Text Note",
	2:     "Recommend Relay",
	3:     "Follows",
	4:     "Encrypted Direct Messages",
	5:     "Event Deletion Request",
	6:     "Repost",
	7:     "Reaction",
	8:     "Badge Award",
	9:     "Chat Message",
	10:    "Group Chat Threaded Reply",
	11:    "Thread",
	12:    "Group Thread Reply",
	13:    "Seal",
	14:    "Direct Message",
	15:    "File Message",
	16:    "Generic Repost",
	17:    "Reaction to a website",
	20:    "Picture",
	21:    "Video Event",
	22:    "Short-form Portrait Video Event",
	30:    "internal reference",
	31:    "external web reference",
	32:    "hardcopy reference",
	33:    "prompt reference",
	40:    "Channel Creation",
	41:    "Channel Metadata",
	42:    "Channel Message",
	43:    "Channel Hide Message",
	44:    "Channel Mute User",
	62:    "Request to Vanish",
	64:    "Chess (PGN)",
	818:   "Merge Requests",
	1018:  "Poll Response",
	1021:  "Bid",
	1022:  "Bid confirmation",
	1040:  "OpenTimestamps",
	1059:  "Gift Wrap",
	1063:  "File Metadata",
	1068:  "Poll",
	1111:  "Comment",
	1311:  "Live Chat Message",
	1337:  "Code Snippet",
	1617:  "Patches",
	1621:  "Issues",
	1622:  "Git Replies (deprecated)",
	1971:  "Problem Tracker",
	1984:  "Reporting",
	1985:  "Label",
	1986:  "Relay reviews",
	1987:  "AI Embeddings / Vector lists",
	2003:  "Torrent",
	2004:  "Torrent Comment",
	2022:  "Coinjoin Pool",
	4550:  "Community Post Approval",
	7000:  "Job Feedback",
	7374:  "Reserved Cashu Wallet Tokens",
	7375:  "Cashu Wallet Tokens",
	7376:  "Cashu Wallet History",
	9041:  "Zap Goal",
	9321:  "Nutzap",
	9467:  "Tidal login",
	9734:  "Zap Request",
	9735:  "Zap",
	9802:  "Highlights",
	10000: "Mute list",
	10001: "Pin list",
	10002: "Relay List Metadata",
	10003: "Bookmark list",
	10004: "Communities list",
	10005: "Public chats list",
	10006: "Blocked relays list",
	10007: "Search relays list",
	10009: "User groups",
	10012: "Favorite relays list",
	10013: "Private event relay list",
	10015: "Interests list",
	10019: "Nutzap Mint Recommendation",
	10020: "Media follows",
	10030: "User emoji list",
	10050: "Relay list to receive DMs",
	10063: "User server list",
	10096: "File storage server list",
	10166: "Relay Monitor Announcement",
	13194: "Wallet Info",
	17375: "Cashu Wallet Event",
	21000: "Lightning Pub RPC",
	22242: "Client Authentication",
	23194: "Wallet Request",
	23195: "Wallet Response",
	24133: "Nostr Connect",
	24242: "Blobs stored on mediaservers",
	27235: "HTTP Auth",
	30000: "Follow sets",
	30001: "Generic lists (deprecated)",
	30002: "Relay sets",
	30003: "Bookmark sets",
	30004: "Curation sets",
	30005: "Video sets",
	30007: "Kind mute sets",
	30008: "Profile Badges",
	30009: "Badge Definition",
	30015: "Interest sets",
	30017: "Create or update a stall",
	30018: "Create or update a product",
	30019: "Marketplace UI/UX",
	30020: "Product sold as an auction",
	30023: "Long-form Content",
	30024: "Draft Long-form Content",
	30030: "Emoji sets",
	30040: "Curated Publication Index",
	30041: "Curated Publication Content",
	30063: "Release artifact sets",
	30078: "Application-specific Data",
	30166: "Relay Discovery",
	30267: "App curation sets",
	30311: "Live Event",
	30315: "User Statuses",
	30388: "Slide Set",
	30402: "Classified Listing",
	30403: "Draft Classified Listing",
	30617: "Repository announcements",
	30618: "Repository state announcements",
	30818: "Wiki article",
	30819: "Redirects",
	31234: "Draft Event",
	31388: "Link Set",
	31890: "Feed",
	31922: "Date-Based Calendar Event",
	31923: "Time-Based Calendar Event",
	31924: "Calendar",
	31925: "Calendar Event RSVP",
	31989: "Handler recommendation",
	31990: "Handler information",
	32267: "Software Application",
	34550: "Community Definition",
	38383: "Peer-to-peer Order events",
	39089: "Starter packs",
	39092: "Media starter packs",
	39701: "Web bookmarks",
}

// GetKindDescription returns a human-readable string for a given Nostr event kind.
// It handles specific kinds, ranged kinds, and unknown kinds.
func GetKindName(kind int) string {
	// First, check the map for a direct match.
	if name, ok := kindNames[kind]; ok {
		return name
	}

	// If not in the map, check for known ranges.
	switch {
	case kind >= 1630 && kind <= 1633:
		return "Status"
	case kind >= 5000 && kind <= 5999:
		return "Job Request"
	case kind >= 6000 && kind <= 6999:
		return "Job Result"
	case kind >= 9000 && kind <= 9030:
		return "Group Control Events"
	case kind >= 39000 && kind <= 39009: // Interpreted from "39000-9"
		return "Group metadata events"
	default:
		return fmt.Sprintf("Kind (%d)", kind)
	}
}
