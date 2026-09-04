package definition

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed dotted numeric version. Definition versions in this repo
// are two-segment ("0.12"), host versions are three-segment with a leading v
// ("v0.12.0"), and the protocol version is a bare integer ("1"). All three
// normalize into the same triple so one comparator serves every
// compatible_*_versions field.
//
// This is deliberately not a full semver implementation: pre-release and build
// metadata have no meaning anywhere in this stack, and accepting them would
// imply an ordering nothing here defines. Anything beyond three numeric
// segments is a parse error rather than a silently ignored suffix.
type Version struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion accepts "1", "0.12", "0.12.0", and any of those with a leading
// "v". Missing segments are zero.
func ParseVersion(raw string) (Version, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if trimmed == "" {
		return Version{}, fmt.Errorf("%w: empty version", ErrUnsupportedRange)
	}
	segments := strings.Split(trimmed, ".")
	if len(segments) > 3 {
		return Version{}, fmt.Errorf("%w: %q has more than three segments", ErrUnsupportedRange, raw)
	}
	var version Version
	targets := []*int{&version.Major, &version.Minor, &version.Patch}
	for i, segment := range segments {
		value, err := strconv.Atoi(segment)
		if err != nil || value < 0 {
			return Version{}, fmt.Errorf("%w: %q is not a numeric version", ErrUnsupportedRange, raw)
		}
		*targets[i] = value
	}
	return version, nil
}

// Compare orders two versions: -1, 0, or 1.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{{v.Major, other.Major}, {v.Minor, other.Minor}, {v.Patch, other.Patch}} {
		switch {
		case pair[0] < pair[1]:
			return -1
		case pair[0] > pair[1]:
			return 1
		}
	}
	return 0
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Range is a conjunction of comparator clauses — every clause must hold. The
// supported grammar is the small one this stack actually authors:
//
//	"*"                   any version
//	">=0.12.0"            lower bound, inclusive
//	">0.12.0"             lower bound, exclusive
//	"<1.0.0"              upper bound, exclusive
//	"<=1.0.0"             upper bound, inclusive
//	"=1.0.0", "1.0.0"     exact
//	">=0.12.0 <1.0.0"     both, space separated
//
// Deliberately no `^`, `~`, or `||`: caret and tilde mean different things in
// different ecosystems, and a compatibility range that a reader can misread is
// worse than one they cannot write.
type Range struct {
	raw     string
	clauses []clause
}

type clause struct {
	operator string
	bound    Version
}

// ParseRange parses a compatibility range. An empty string is an error;
// "any version" must be spelled "*" so that an unauthored field and a
// deliberately open range are distinguishable.
func ParseRange(raw string) (Range, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Range{}, fmt.Errorf("%w: empty range (use %q for any version)", ErrUnsupportedRange, "*")
	}
	if trimmed == "*" {
		return Range{raw: trimmed}, nil
	}
	out := Range{raw: trimmed}
	for _, field := range strings.Fields(trimmed) {
		operator := "="
		for _, candidate := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(field, candidate) {
				operator = candidate
				field = strings.TrimPrefix(field, candidate)
				break
			}
		}
		bound, err := ParseVersion(field)
		if err != nil {
			return Range{}, err
		}
		out.clauses = append(out.clauses, clause{operator: operator, bound: bound})
	}
	return out, nil
}

// Contains reports whether version satisfies every clause.
func (r Range) Contains(version Version) bool {
	for _, c := range r.clauses {
		comparison := version.Compare(c.bound)
		satisfied := false
		switch c.operator {
		case ">=":
			satisfied = comparison >= 0
		case ">":
			satisfied = comparison > 0
		case "<=":
			satisfied = comparison <= 0
		case "<":
			satisfied = comparison < 0
		case "=":
			satisfied = comparison == 0
		}
		if !satisfied {
			return false
		}
	}
	return true
}

// String returns the range as authored.
func (r Range) String() string { return r.raw }
