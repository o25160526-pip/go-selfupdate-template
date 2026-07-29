package version

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseDisplay(t *testing.T) {
	v, err := Parse("1.26.0729.1930")
	if err != nil {
		t.Fatal(err)
	}
	if v.Year != 26 || v.Month != 7 || v.Day != 29 || v.Hour != 19 || v.Minute != 30 {
		t.Fatalf("bad parse: %+v", v)
	}
	if got := v.String(); got != "1.26.0729.1930" {
		t.Fatalf("round trip: %s", got)
	}
	if got := v.Tag(); got != "v1.26.7291930" {
		t.Fatalf("tag: %s", got)
	}
}

func TestParseSemverRoundTrip(t *testing.T) {
	for _, s := range []string{"1.26.0729.1930", "1.26.0101.0000", "1.99.1231.2359", "1.26.0102.0304"} {
		v := MustParse(s)
		back, err := Parse(v.Tag())
		if err != nil {
			t.Fatalf("%s -> %s: %v", s, v.Tag(), err)
		}
		if !back.Equal(v) || back.String() != s {
			t.Fatalf("%s round tripped to %s via %s", s, back, v.Tag())
		}
	}
}

func TestParseRejects(t *testing.T) {
	bad := []string{
		"", "1.26", "1.26.0729", "1.26.0729.1930.1", "v1.26.1329.1930", // month 13
		"1.26.0732.1930", // day 32
		"1.26.0729.2530", // hour 25
		"1.26.0729.1960", // minute 60
		"1.26.7291930+run.7",
		"1.26.7291930-rc1",
		"latest",
	}
	for _, s := range bad {
		if _, err := Parse(s); err == nil {
			t.Errorf("expected error for %q", s)
		}
	}
}

func TestMonotonic(t *testing.T) {
	ordered := []string{
		"1.26.0101.0000",
		"1.26.0101.0001",
		"1.26.0101.2359",
		"1.26.0102.0000",
		"1.26.1231.2359",
		"1.27.0101.0000",
		"2.00.0101.0000",
	}
	for i := 1; i < len(ordered); i++ {
		prev, cur := MustParse(ordered[i-1]), MustParse(ordered[i])
		if !cur.After(prev) {
			t.Errorf("%s should be after %s", cur, prev)
		}
	}
}

// Every minute of a full year must produce a strictly increasing key and a
// version string that parses back to itself. This is the property the whole
// update ordering rests on.
func TestEveryMinuteOfAYear(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, 0)
	var last int64 = -1
	for tt := start; tt.Before(end); tt = tt.Add(time.Minute) {
		v := FromTime(tt)
		if k := v.Key(); k <= last {
			t.Fatalf("key not increasing at %s: %d <= %d", tt, k, last)
		} else {
			last = k
		}
		if back, err := Parse(v.String()); err != nil || !back.Equal(v) {
			t.Fatalf("round trip failed at %s (%s): %v", tt, v, err)
		}
		if back, err := Parse(v.Semver()); err != nil || !back.Equal(v) {
			t.Fatalf("semver round trip failed at %s (%s): %v", tt, v.Semver(), err)
		}
	}
}

func TestFromTimeIsUTC(t *testing.T) {
	bangkok := time.FixedZone("ICT", 7*3600)
	local := time.Date(2026, 7, 30, 2, 30, 0, 0, bangkok) // = 19:30 UTC on 07-29
	if got := FromTime(local).String(); got != "1.26.0729.1930" {
		t.Fatalf("expected UTC normalisation, got %s", got)
	}
}

func TestJSON(t *testing.T) {
	type wrap struct{ V Version }
	b, err := json.Marshal(wrap{MustParse("1.26.0729.1930")})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"V":"1.26.0729.1930"}` {
		t.Fatalf("marshal: %s", b)
	}
	var w wrap
	if err := json.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	if w.V.String() != "1.26.0729.1930" {
		t.Fatalf("unmarshal: %s", w.V)
	}
}

func TestSortDesc(t *testing.T) {
	vs := []Version{MustParse("1.26.0101.0000"), MustParse("1.26.0729.1930"), MustParse("1.26.0301.1200")}
	SortDesc(vs)
	if vs[0].String() != "1.26.0729.1930" || vs[2].String() != "1.26.0101.0000" {
		t.Fatalf("bad order: %v", vs)
	}
}
