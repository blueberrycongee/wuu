package plugin

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

// modulePseudoVersion matches Go module pseudo-versions such as
// v0.18.1-0.20261101050653-675d99db5a4e or the no-base-tag form
// v0.0.0-20261101050653-675d99db5a4e. A binary installed via
// `go install module@rev` reports one instead of the product CalVer, and the
// base tag predates the CalVer era, so the reported version understates the
// actual checkout and fails compatibility gates spuriously.
var modulePseudoVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+-(?:0\.)?\d{14}-[0-9a-f]{12}`)

// CheckMinimumWuuVersion reports whether a package declaring
// minimum_wuu_version may activate on a host running current. An empty
// minimum imposes no constraint. A pre-release current (dev builds such as
// v0.15.0-dev) satisfies constraints at its base version so local
// development is not blocked by its own pre-release marker. Unknown or
// invalid values fail closed: the compatibility contract is a promise, and
// a constraint the host cannot evaluate must not silently pass.
func CheckMinimumWuuVersion(minimum, current string) error {
	minimum = strings.TrimSpace(minimum)
	if minimum == "" {
		return nil
	}
	normalizedMinimum := minimum
	if !strings.HasPrefix(normalizedMinimum, "v") {
		normalizedMinimum = "v" + normalizedMinimum
	}
	if !semver.IsValid(normalizedMinimum) {
		return fmt.Errorf("minimum_wuu_version %q is not a valid semantic version (expected major.minor.patch)", minimum)
	}

	current = strings.TrimSpace(current)
	if current == "" {
		return fmt.Errorf("host version is unknown; cannot verify minimum_wuu_version %q", minimum)
	}
	normalizedCurrent := current
	if !strings.HasPrefix(normalizedCurrent, "v") {
		normalizedCurrent = "v" + normalizedCurrent
	}
	base := normalizedCurrent
	if idx := strings.IndexByte(base, '-'); idx >= 0 {
		base = base[:idx]
	}
	if !semver.IsValid(base) {
		return fmt.Errorf("host version %q is not a valid semantic version", current)
	}
	if semver.Compare(base, normalizedMinimum) < 0 {
		err := fmt.Errorf("requires Wuu >= %s, current is %s", minimum, current)
		if modulePseudoVersion.MatchString(normalizedCurrent) {
			err = fmt.Errorf("%w (a Go module pseudo-version, not the product version; rebuild with `make` to stamp it)", err)
		}
		return err
	}
	return nil
}
