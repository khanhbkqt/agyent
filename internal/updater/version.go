package updater

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SemVer holds parsed semantic version components.
type SemVer struct {
	Major      int
	Minor      int
	Patch      int
	PreRelease string
}

var semVerRegex = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.(\d+))?(?:-([0-9A-Za-z.-]+))?$`)

// ParseVersion parses a version string like "v1.2.3", "1.2", or "0.1.0-dev".
func ParseVersion(v string) (*SemVer, error) {
	v = strings.TrimSpace(v)
	matches := semVerRegex.FindStringSubmatch(v)
	if len(matches) == 0 {
		return nil, fmt.Errorf("invalid semver format: %q", v)
	}

	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	patch := 0
	if matches[3] != "" {
		patch, _ = strconv.Atoi(matches[3])
	}
	pre := matches[4]

	return &SemVer{
		Major:      major,
		Minor:      minor,
		Patch:      patch,
		PreRelease: pre,
	}, nil
}

// Compare returns:
// - 1 if v > other
// - -1 if v < other
// - 0 if v == other
func (v *SemVer) Compare(other *SemVer) int {
	if v.Major != other.Major {
		if v.Major > other.Major {
			return 1
		}
		return -1
	}
	if v.Minor != other.Minor {
		if v.Minor > other.Minor {
			return 1
		}
		return -1
	}
	if v.Patch != other.Patch {
		if v.Patch > other.Patch {
			return 1
		}
		return -1
	}

	// Normal release is newer than pre-release (e.g. 1.0.0 > 1.0.0-dev)
	if v.PreRelease == "" && other.PreRelease != "" {
		return 1
	}
	if v.PreRelease != "" && other.PreRelease == "" {
		return -1
	}

	if v.PreRelease != other.PreRelease {
		return strings.Compare(v.PreRelease, other.PreRelease)
	}

	return 0
}

// IsNewerVersion returns true if remote version string is strictly greater than current version string.
func IsNewerVersion(remote, current string) bool {
	r, errR := ParseVersion(remote)
	c, errC := ParseVersion(current)

	if errR != nil || errC != nil {
		// Fallback string compare
		return strings.TrimPrefix(remote, "v") != strings.TrimPrefix(current, "v")
	}

	return r.Compare(c) > 0
}
