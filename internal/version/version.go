// Package version owns the app's version format and comparison rules.
//
// One version, two representations:
//
//	display: 1.26.0729.1930   what humans and the update menu see (1.YY.MMDD.HHmm)
//	semver:  1.26.7291930     what git tags and semver-aware tooling see
//
// The display format from the spec is NOT valid semver (four numeric segments),
// which breaks goreleaser, Go modules and golang.org/x/mod/semver. So the git
// tag collapses MMDD+HHmm into the patch segment: MMDDHHmm as an integer.
// Inside one year MMDDHHmm increases monotonically, and the minor segment (YY)
// bumps at new year, so ordering stays correct through 2099.
//
// Build metadata (+run.123) is deliberately NOT used to disambiguate builds:
// semver ignores build metadata when comparing, so clients could not tell which
// build is newer. Collisions are resolved by bumping the minute instead.
package version

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Injected at build time with -ldflags -X.
var (
	Current   = "1.00.0101.0000"
	Commit    = "unknown"
	BuildDate = "unknown"
	Channel   = "dev"
)

// Version is a parsed build version.
type Version struct {
	Major  int
	Year   int // two digits, 0-99
	Month  int
	Day    int
	Hour   int
	Minute int
}

var (
	displayRe = regexp.MustCompile(`^(\d+)\.(\d{2})\.(\d{2})(\d{2})\.(\d{2})(\d{2})$`)
	semverRe  = regexp.MustCompile(`^(\d+)\.(\d{1,2})\.(\d{1,8})$`)
)

// Parse accepts both representations, with or without a leading "v".
func Parse(s string) (Version, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	// Reject semver pre-release / build metadata early: this scheme has no use
	// for them and silently dropping them would hide ordering bugs.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		return Version{}, fmt.Errorf("version %q: pre-release/build metadata is not supported", s)
	}
	if m := displayRe.FindStringSubmatch(s); m != nil {
		return assemble(atoi(m[1]), atoi(m[2]), atoi(m[3]), atoi(m[4]), atoi(m[5]), atoi(m[6]), s)
	}
	if m := semverRe.FindStringSubmatch(s); m != nil {
		p := fmt.Sprintf("%08d", atoi(m[3]))
		return assemble(atoi(m[1]), atoi(m[2]), atoi(p[0:2]), atoi(p[2:4]), atoi(p[4:6]), atoi(p[6:8]), s)
	}
	return Version{}, fmt.Errorf("version %q: want 1.YY.MMDD.HHmm or 1.YY.MMDDHHmm", s)
}

func assemble(major, year, month, day, hour, minute int, raw string) (Version, error) {
	v := Version{Major: major, Year: year, Month: month, Day: day, Hour: hour, Minute: minute}
	if err := v.Validate(); err != nil {
		return Version{}, fmt.Errorf("version %q: %w", raw, err)
	}
	return v, nil
}

// MustParse panics on invalid input. Only for constants and tests.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// FromTime builds a version from a timestamp. Always converted to UTC:
// CI runners disagree about local time, which would make the same commit
// produce two different versions on GitHub and Azure.
func FromTime(t time.Time) Version {
	t = t.UTC()
	return Version{
		Major:  1,
		Year:   t.Year() % 100,
		Month:  int(t.Month()),
		Day:    t.Day(),
		Hour:   t.Hour(),
		Minute: t.Minute(),
	}
}

// Now is FromTime(time.Now()).
func Now() Version { return FromTime(time.Now()) }

// Self is the version this binary was built as.
func Self() (Version, error) { return Parse(Current) }

func (v Version) Validate() error {
	switch {
	case v.Major < 0:
		return fmt.Errorf("major must be >= 0")
	case v.Year < 0 || v.Year > 99:
		return fmt.Errorf("year must be 00-99, got %d", v.Year)
	case v.Month < 1 || v.Month > 12:
		return fmt.Errorf("month must be 01-12, got %02d", v.Month)
	case v.Day < 1 || v.Day > 31:
		return fmt.Errorf("day must be 01-31, got %02d", v.Day)
	case v.Hour > 23 || v.Hour < 0:
		return fmt.Errorf("hour must be 00-23, got %02d", v.Hour)
	case v.Minute > 59 || v.Minute < 0:
		return fmt.Errorf("minute must be 00-59, got %02d", v.Minute)
	}
	return nil
}

// String returns the display form: 1.26.0729.1930
func (v Version) String() string {
	return fmt.Sprintf("%d.%02d.%02d%02d.%02d%02d", v.Major, v.Year, v.Month, v.Day, v.Hour, v.Minute)
}

// Semver returns the semver form: 1.26.7291930
func (v Version) Semver() string {
	return fmt.Sprintf("%d.%02d.%d", v.Major, v.Year, v.stamp())
}

// Tag returns the git tag: v1.26.7291930
func (v Version) Tag() string { return "v" + v.Semver() }

func (v Version) stamp() int {
	return v.Month*1000000 + v.Day*10000 + v.Hour*100 + v.Minute
}

// Key is a monotonically increasing sort key.
func (v Version) Key() int64 {
	return int64(v.Major)*10000000000 + int64(v.Year)*100000000 + int64(v.stamp())
}

// Time reconstructs the build timestamp in UTC (year 2000 + YY).
func (v Version) Time() time.Time {
	return time.Date(2000+v.Year, time.Month(v.Month), v.Day, v.Hour, v.Minute, 0, 0, time.UTC)
}

func (v Version) IsZero() bool { return v == Version{} }

// Compare returns -1, 0 or +1.
func (v Version) Compare(o Version) int {
	switch a, b := v.Key(), o.Key(); {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (v Version) After(o Version) bool  { return v.Compare(o) > 0 }
func (v Version) Before(o Version) bool { return v.Compare(o) < 0 }
func (v Version) Equal(o Version) bool  { return v.Compare(o) == 0 }

// MarshalJSON stores versions in their display form, so cache files and
// manifests stay readable by humans.
func (v Version) MarshalJSON() ([]byte, error) { return json.Marshal(v.String()) }

func (v *Version) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		*v = Version{}
		return nil
	}
	p, err := Parse(s)
	if err != nil {
		return err
	}
	*v = p
	return nil
}

// Compare parses both strings and compares them.
func Compare(a, b string) (int, error) {
	va, err := Parse(a)
	if err != nil {
		return 0, err
	}
	vb, err := Parse(b)
	if err != nil {
		return 0, err
	}
	return va.Compare(vb), nil
}

// SortDesc sorts newest first, which is the order every menu wants.
func SortDesc(vs []Version) {
	sort.Slice(vs, func(i, j int) bool { return vs[i].After(vs[j]) })
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// Info is the payload of `app version --json`. The CI smoke test parses this,
// so treat the field names as a stable contract.
type Info struct {
	Version   string `json:"version"`
	Semver    string `json:"semver"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Channel   string `json:"channel"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}
