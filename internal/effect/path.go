package effect

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Path errors. Each maps to exactly one refusal code, and none of them ever
// carries the offending path: an error string is a log line waiting to happen
// (ADR 0002 §8).
var (
	// ErrPathInvalid means the string is not a usable root-relative path at
	// all — empty, absolute, NUL-bearing, or naming a volume.
	ErrPathInvalid = errors.New("effect: path is not a valid scoped reference")
	// ErrPathEscapes means the path leaves its root, before or after symlink
	// resolution.
	ErrPathEscapes = errors.New("effect: path escapes its root")
	// ErrRootUnmediated means no authority registered a root under that id, so
	// there is nothing to scope an effect to.
	ErrRootUnmediated = errors.New("effect: root is not mediated by this host")
	// ErrTooLarge means the target exceeds the root's read ceiling.
	ErrTooLarge = errors.New("effect: content exceeds the mediated size limit")
	// ErrNotRegularFile means the target is a directory, device, socket, or
	// symlink-to-nowhere. Only regular files are readable.
	ErrNotRegularFile = errors.New("effect: target is not a regular file")
)

// NormalizeRelative reduces a candidate root-relative path to its canonical
// form, or refuses it.
//
// This is the *first* of two defenses and deliberately not the only one. It
// catches a malformed reference early, with a typed refusal, before any
// syscall runs. What it cannot catch is a path that is well-formed now and
// points outside the root by the time it is opened — that is [OpenInRoot]'s
// job, and neither substitutes for the other.
//
// The rules:
//
//   - A backslash is a separator, not a name character. Accepting `..\..\x` as
//     a single filename on a Unix host would let a Windows-shaped traversal
//     through a Unix-shaped check.
//   - A NUL byte is refused outright. It truncates in every C-level API the
//     path eventually reaches, so a name that is safe to inspect and unsafe to
//     open must never be representable.
//   - A volume name (`C:`, `\\host\share`) is refused. A root-relative path
//     that names a volume is not root-relative.
//   - `path.Clean` collapses `.`, duplicate separators, and *interior* `..`.
//     A `..` that survives is a leading one, which is an escape by
//     construction.
//   - `.` and the empty string are refused: they name the root itself, and a
//     handle to a root is a different class than a handle to a file in it.
func NormalizeRelative(raw string) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", ErrPathInvalid
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrPathInvalid
	}
	// A volume name is refused on *every* platform, not only where
	// filepath.VolumeName recognizes one. `C:\Windows` is an absolute path on
	// Windows and an ordinary two-element relative path on Unix; a check that
	// depends on GOOS would let a handle mean different things on different
	// hosts, and the same normalized string has to mean one thing everywhere.
	if filepath.VolumeName(trimmed) != "" || hasDriveLetter(trimmed) {
		return "", ErrPathInvalid
	}
	slashed := strings.ReplaceAll(trimmed, `\`, "/")
	if strings.HasPrefix(slashed, "/") {
		return "", ErrPathEscapes
	}
	cleaned := path.Clean(slashed)
	if cleaned == "." || cleaned == "" {
		return "", ErrPathInvalid
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrPathEscapes
	}
	// Belt and braces: after Clean no interior `..` can remain, so an element
	// that still spells it means Clean's contract changed under us. Refusing
	// costs nothing and the assumption is load-bearing.
	for _, element := range strings.Split(cleaned, "/") {
		if element == ".." {
			return "", ErrPathEscapes
		}
	}
	return cleaned, nil
}

// OpenInRoot opens a root-relative path for reading, without ever trusting the
// filesystem to still look the way it did a moment ago.
//
// This is the second defense, and the one that actually holds. [os.Root]
// resolves every component relative to an open directory descriptor and
// refuses any component — including a symlink target — that leaves the root.
// That closes the two holes a string check cannot:
//
//   - **Symlink escape.** `notes/link` where `link` points at `/etc/passwd` is
//     a perfectly well-formed relative path. Only resolution catches it, and
//     resolving with [filepath.EvalSymlinks] and then opening the result is
//     precisely the race below.
//
//   - **TOCTOU.** Between a `EvalSymlinks`-and-compare and the `os.Open` that
//     follows it, the same local user can replace a checked directory with a
//     symlink. There is no amount of re-checking that fixes it, because the
//     check and the open are two syscalls against a name. [os.Root] does not
//     check a name; it walks descriptors, so there is no window to lose.
//
// The caller still passes an already-[NormalizeRelative]d path, because a
// typed early refusal is a better error than a syscall failure and because the
// normalized form is what a handle stores.
func OpenInRoot(rootPath, relative string) (*os.File, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, ErrRootUnmediated
	}
	defer func() { _ = root.Close() }()

	file, err := root.Open(relative)
	if err != nil {
		// os.Root reports an escape as a path error rather than a distinct
		// sentinel. Everything it refuses is either an escape or a missing
		// file, and telling those apart in the refusal would let a caller
		// probe for the existence of a file outside the root — which is the
		// question the root exists to refuse.
		return nil, ErrPathEscapes
	}
	info, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return nil, ErrPathEscapes
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, ErrNotRegularFile
	}
	return file, nil
}

// ReadInRoot reads at most limit bytes from a root-relative path.
//
// It reads limit+1 bytes and refuses at limit, so a file exactly one byte over
// the ceiling is refused rather than silently truncated. A truncated read that
// looks successful is worse than a refusal: the participant sees content that
// is not the content.
func ReadInRoot(rootPath, relative string, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultMaxReadBytes
	}
	file, err := OpenInRoot(rootPath, relative)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	content, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	if readErr != nil {
		return nil, ErrPathEscapes
	}
	if int64(len(content)) > limit {
		return nil, ErrTooLarge
	}
	return content, nil
}

// hasDriveLetter reports whether a path opens with a Windows drive
// specification, independent of the host it is evaluated on.
func hasDriveLetter(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	letter := value[0]
	return (letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z')
}

// isAbsolutePath reports whether a root's declared path is absolute. A
// relative root is resolved against whatever the process happens to have as a
// working directory, which is not a root anyone chose.
func isAbsolutePath(value string) bool { return filepath.IsAbs(value) }

// writeInRoot writes bytes to a root-relative path without leaving the root.
//
// It uses the same [os.Root] descriptor walk as [OpenInRoot], for the same
// reason: a write that resolves a name and then opens it can be redirected
// between the two syscalls, and a redirected write is worse than a redirected
// read.
//
// One honest limit: [os.Root] follows a symlink whose target is *inside* the
// root, so a mediated write can land on a link's target rather than on the
// link. That is contained — it cannot leave the root, which is the boundary
// the model promises — but it is not the same as "lands exactly where the
// handle says". Narrowing it needs an `O_NOFOLLOW` the os package does not
// expose portably, and a syscall-level build tag is not worth carrying for a
// capability no shipped definition declares.
func writeInRoot(rootPath, relative string, content []byte) error {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return ErrRootUnmediated
	}
	defer func() { _ = root.Close() }()

	file, err := root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return ErrPathEscapes
	}
	if _, writeErr := file.Write(content); writeErr != nil {
		_ = file.Close()
		return ErrPathEscapes
	}
	return file.Close()
}
