package buildinfo

import (
	"regexp"
	"runtime/debug"
	"testing"
)

func build(settings ...string) *debug.BuildInfo {
	bi := &debug.BuildInfo{GoVersion: "go1.25.1"}
	for i := 0; i < len(settings); i += 2 {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
	}
	return bi
}

func TestFromBuild(t *testing.T) {
	for _, tc := range []struct {
		name string
		bi   *debug.BuildInfo
		want Info
	}{
		{"no build info", nil, Info{"1.4.0", UnknownCommit, UnknownCommitTime, false, "go-running", "linux/amd64"}},
		{"no vcs", build(), Info{"1.4.0", UnknownCommit, UnknownCommitTime, false, "go1.25.1", "linux/amd64"}},
		{
			"clean checkout",
			build("vcs.revision", "abc123", "vcs.time", "2026-09-27T12:34:56Z", "vcs.modified", "false"),
			Info{"1.4.0", "abc123", "2026-09-27T12:34:56Z", false, "go1.25.1", "linux/amd64"},
		},
		{
			"modified, offset time with fraction",
			build("vcs.revision", "abc123", "vcs.time", "2026-09-27T14:34:56.9+02:00", "vcs.modified", "true"),
			Info{"1.4.0", "abc123", "2026-09-27T12:34:56Z", true, "go1.25.1", "linux/amd64"},
		},
		{
			"unparseable time",
			build("vcs.revision", "abc123", "vcs.time", "yesterday"),
			Info{"1.4.0", "abc123", UnknownCommitTime, false, "go1.25.1", "linux/amd64"},
		},
		{
			"time without revision",
			build("vcs.time", "2026-09-27T12:34:56Z", "vcs.modified", "true"),
			Info{"1.4.0", UnknownCommit, UnknownCommitTime, false, "go1.25.1", "linux/amd64"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fromBuild(tc.bi, "1.4.0", "go-running", "linux/amd64"); got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestRead(t *testing.T) {
	info := Read()
	if info.Version != Version || info.Go == "" || !regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`).MatchString(info.Platform) {
		t.Errorf("Read() = %+v", info)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`).MatchString(info.CommitTime) {
		t.Errorf("commit time %q is not a timestamp", info.CommitTime)
	}
}
