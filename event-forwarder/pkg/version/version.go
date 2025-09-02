package version

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
	return BuildInfo{Version: Version, Commit: Commit, Built: Built}
}
