package cli

import (
	"fmt"
)

var (
	AppVersion = "n/a"
	GitCommit  = "n/a"
	BuildDate  = "n/a"
	GitBranch  = "n/a"
	GoVersion  = "n/a"
)

func ShowVersion() {
	fmt.Printf("AppVersion: %s\nGitCommit: %s\nBuildDate: %s\nGitBranch: %s\nGoVersion: %s\n", AppVersion, GitCommit, BuildDate, GitBranch, GoVersion)
}
