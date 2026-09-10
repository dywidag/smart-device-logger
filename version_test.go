package main

import (
	"runtime/debug"
	"testing"
)

func vcs(revision, when, modified string) []debug.BuildSetting {
	return []debug.BuildSetting{
		{Key: "vcs.revision", Value: revision},
		{Key: "vcs.time", Value: when},
		{Key: "vcs.modified", Value: modified},
	}
}

func TestFormatVersion(t *testing.T) {
	const sha = "b4f91e1ebbbe28d2b9b1279bb7e72ade57d74a10"
	const when = "2026-09-10T11:01:45Z"

	tests := []struct {
		name          string
		tag           string
		moduleVersion string
		settings      []debug.BuildSetting
		want          string
	}{
		{
			name:          "release build uses the stamped tag",
			tag:           "v1.0.0",
			moduleVersion: "(devel)",
			settings:      vcs(sha, when, "false"),
			want:          "v1.0.0 (b4f91e1ebbbe, " + when + ")",
		},
		{
			name:          "go install falls back to the module version",
			moduleVersion: "v1.2.3",
			settings:      vcs(sha, when, "false"),
			want:          "v1.2.3 (b4f91e1ebbbe, " + when + ")",
		},
		{
			name:          "a stamped tag wins over the module version",
			tag:           "v2.0.0",
			moduleVersion: "v1.2.3",
			settings:      vcs(sha, when, "false"),
			want:          "v2.0.0 (b4f91e1ebbbe, " + when + ")",
		},
		{
			// Go invents this for an untagged commit. It looks like a
			// release and is not one.
			name:          "a pseudo-version is not a release",
			moduleVersion: "v0.0.0-20260910110145-b4f91e1ebbbe",
			settings:      vcs(sha, when, "false"),
			want:          "dev (b4f91e1ebbbe, " + when + ")",
		},
		{
			name:          "a dirty pseudo-version is not a release either",
			moduleVersion: "v0.0.0-20260910110145-b4f91e1ebbbe+dirty",
			settings:      vcs(sha, when, "true"),
			want:          "dev (b4f91e1ebbbe, dirty)",
		},
		{
			name:          "a real tag keeps its number when the tree is dirty",
			moduleVersion: "v1.2.3+dirty",
			settings:      vcs(sha, when, "true"),
			want:          "v1.2.3 (b4f91e1ebbbe, dirty)",
		},
		{
			name:          "a local build is dev with its commit",
			moduleVersion: "(devel)",
			settings:      vcs(sha, when, "false"),
			want:          "dev (b4f91e1ebbbe, " + when + ")",
		},
		{
			name:          "an uncommitted build says dirty and drops the time",
			moduleVersion: "(devel)",
			settings:      vcs(sha, when, "true"),
			want:          "dev (b4f91e1ebbbe, dirty)",
		},
		{
			name: "no vcs information still names a version",
			tag:  "v1.0.0",
			want: "v1.0.0",
		},
		{
			name: "nothing at all is dev",
			want: "dev",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatVersion(tc.tag, tc.moduleVersion, tc.settings)
			if got != tc.want {
				t.Errorf("formatVersion = %q, want %q", got, tc.want)
			}
		})
	}
}
