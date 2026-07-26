// Package update keeps the client current from GitHub Releases.
//
// An update mechanism is an execution path with the same power as an installer, so the
// rules here are strict: the archive is verified against a signature made by a key the
// user never sees, the running binary is replaced atomically, and anything the client
// cannot verify is refused rather than installed.
package update

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a released version, compared the way releases are ordered rather than the
// way strings sort: 0.10.0 is newer than 0.9.0.
type Version struct {
	Major, Minor, Patch int
	// PreRelease is the part after '-'. A version with one is older than the same version
	// without, so a release candidate never replaces the release it precedes.
	PreRelease string
	// Raw is the original text, for display.
	Raw string
}

// ParseVersion accepts "v1.2.3", "1.2.3" and "1.2.3-rc1"; anything else is an error, so a
// malformed tag can never be mistaken for a newer release.
func ParseVersion(raw string) (Version, error) {
	text := strings.TrimSpace(raw)
	original := text
	text = strings.TrimPrefix(text, "v")
	if text == "" {
		return Version{}, fmt.Errorf("пустая версия")
	}

	pre := ""
	if index := strings.IndexAny(text, "-+"); index >= 0 {
		pre = text[index+1:]
		text = text[:index]
	}

	parts := strings.Split(text, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return Version{}, fmt.Errorf("некорректная версия %q", raw)
	}
	numbers := [3]int{}
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return Version{}, fmt.Errorf("некорректная версия %q", raw)
		}
		numbers[index] = value
	}
	return Version{
		Major:      numbers[0],
		Minor:      numbers[1],
		Patch:      numbers[2],
		PreRelease: pre,
		Raw:        original,
	}, nil
}

// NewerThan reports whether v is a later release than other.
func (v Version) NewerThan(other Version) bool {
	switch {
	case v.Major != other.Major:
		return v.Major > other.Major
	case v.Minor != other.Minor:
		return v.Minor > other.Minor
	case v.Patch != other.Patch:
		return v.Patch > other.Patch
	}
	switch {
	case v.PreRelease == other.PreRelease:
		return false
	case v.PreRelease == "":
		// A final release supersedes its own pre-releases.
		return true
	case other.PreRelease == "":
		return false
	default:
		return v.PreRelease > other.PreRelease
	}
}

// String renders the version as it was published.
func (v Version) String() string {
	if v.Raw != "" {
		return v.Raw
	}
	text := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.PreRelease != "" {
		text += "-" + v.PreRelease
	}
	return text
}
