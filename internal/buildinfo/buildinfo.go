package buildinfo

import (
	"runtime"
	"runtime/debug"
	"time"
)

// Version is the release version, set at release build time:
//
//	go build -ldflags "-X github.com/phansen314/ftask/internal/buildinfo.Version=1.4.0"
//
// A development build keeps the default, which is still semver.
var Version = "0.0.0-dev"

// Development-build values for a binary built without VCS information
// (implementation-spec.md, Toolchain).
const (
	UnknownCommit     = "unknown"
	UnknownCommitTime = "1970-01-01T00:00:00Z"
)

// timeLayout is the design spec's timestamp form: UTC, whole seconds.
const timeLayout = "2006-01-02T15:04:05Z"

// Info describes the running binary.
type Info struct {
	Version            string
	Commit             string
	CommitTime         string // UTC, whole seconds, "Z" suffix
	UncommittedChanges bool
	Go                 string
	Platform           string // GOOS/GOARCH
}

// Read reports the running binary's build information.
func Read() Info {
	bi, _ := debug.ReadBuildInfo() // nil when there is none
	return fromBuild(bi, Version, runtime.Version(), runtime.GOOS+"/"+runtime.GOARCH)
}

// fromBuild builds Info from Go's embedded build information, nil when there
// is none. goVersion and platform are the running toolchain's, used when bi
// lacks them.
func fromBuild(bi *debug.BuildInfo, version, goVersion, platform string) Info {
	info := Info{
		Version:    version,
		Commit:     UnknownCommit,
		CommitTime: UnknownCommitTime,
		Go:         goVersion,
		Platform:   platform,
	}
	if bi == nil {
		return info
	}
	if bi.GoVersion != "" {
		info.Go = bi.GoVersion
	}
	settings := map[string]string{}
	for _, s := range bi.Settings {
		settings[s.Key] = s.Value
	}
	// A commit time without its commit, or one that does not parse, would
	// describe a build it cannot identify; both keep the development values.
	if rev := settings["vcs.revision"]; rev != "" {
		info.Commit = rev
		if t, err := time.Parse(time.RFC3339, settings["vcs.time"]); err == nil {
			info.CommitTime = t.UTC().Truncate(time.Second).Format(timeLayout)
		}
		info.UncommittedChanges = settings["vcs.modified"] == "true"
	}
	return info
}
