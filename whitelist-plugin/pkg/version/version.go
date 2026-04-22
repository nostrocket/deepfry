package version

import (
	"runtime/debug"
	"strings"
)

// Defaults. These can be overridden at build time via -ldflags "-X ...=val",
// but the primary source is Go's automatic VCS stamping (`go build -buildvcs=auto`,
// default since Go 1.18) which is read at runtime via debug.ReadBuildInfo.
var (
	Version = "dev"
	Commit  = "none"
	Built   = "unknown"
)

type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Built   string `json:"built"`
}

func Info() BuildInfo {
	commit := Commit
	built := Built

	if info, ok := debug.ReadBuildInfo(); ok {
		var rev, vcsTime string
		var dirty bool
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.time":
				vcsTime = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if rev != "" && (commit == "none" || commit == "unknown" || commit == "") {
			short := rev
			if len(short) > 7 {
				short = short[:7]
			}
			if dirty {
				short += "-dirty"
			}
			commit = short
		}
		if vcsTime != "" && (built == "unknown" || built == "") {
			built = vcsTime
		}
	}

	// Trim whitespace in case ldflags contained it.
	return BuildInfo{
		Version: strings.TrimSpace(Version),
		Commit:  strings.TrimSpace(commit),
		Built:   strings.TrimSpace(built),
	}
}
