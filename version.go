package main

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// version is the release tag, set only by the release build:
//
//	go build -ldflags "-X main.version=v1.0.0" .
//
// It is empty everywhere else, because Go already records what the binary was
// built from. A `go install ...@v1.0.0` puts the tag in the module version,
// and any build from a checkout records the commit. Reading those beats
// insisting every build remembers to pass a flag.
var version string

// buildVersion describes the running binary, e.g.
//
//	v1.0.0 (b4f91e1ebbbe, 2026-09-10T11:01:45Z)
//	dev (b4f91e1ebbbe, dirty)
//
// A device log is only as good as knowing which build wrote it, so the commit
// and the dirty flag are part of the answer, not decoration.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return fallback(version)
	}
	return formatVersion(version, info.Main.Version, info.Settings)
}

// formatVersion is the whole rule, split out so it can be tested without
// building binaries at different tags.
func formatVersion(tag, moduleVersion string, settings []debug.BuildSetting) string {
	if tag == "" && isReleaseVersion(moduleVersion) {
		// Installed with `go install ...@v1.0.0`.
		tag = strings.TrimSuffix(moduleVersion, "+dirty")
	}

	var revision, when string
	dirty := false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			when = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}

	if len(revision) > 12 {
		revision = revision[:12]
	}

	var detail []string
	if revision != "" {
		detail = append(detail, revision)
	}
	if dirty {
		detail = append(detail, "dirty")
	} else if when != "" {
		// A dirty tree's commit time describes the parent commit, not the
		// binary, so it is left out rather than made to look precise.
		detail = append(detail, when)
	}

	out := fallback(tag)
	if len(detail) > 0 {
		out += " (" + strings.Join(detail, ", ") + ")"
	}
	return out
}

// pseudoVersion matches the v0.0.0-<timestamp>-<commit> that Go invents for a
// module with no tag at the commit being built.
var pseudoVersion = regexp.MustCompile(`-[0-9]{14}-[0-9a-f]{12}(\+dirty)?$`)

// isReleaseVersion reports whether the module version is a real tag someone
// chose, rather than Go's invented stand-in. A pseudo-version says nothing the
// commit below does not say better, and it reads like a release when it is
// not, so builds outside a release stay "dev".
func isReleaseVersion(v string) bool {
	return v != "" && v != "(devel)" && !pseudoVersion.MatchString(v)
}

func fallback(tag string) string {
	if tag == "" {
		return "dev"
	}
	return tag
}
