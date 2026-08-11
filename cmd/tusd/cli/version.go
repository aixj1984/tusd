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
	fmt.Printf("Version Info:\n")
	fmt.Printf("  AppVersion:  %s\n", AppVersion)
	fmt.Printf("  Git Commit:  %s\n", GitCommit)
	fmt.Printf("  Git Branch:  %s\n", GitBranch)
	fmt.Printf("  Build Date:  %s\n", BuildDate)
	fmt.Printf("  Go Version:  %s\n", GoVersion)
}
