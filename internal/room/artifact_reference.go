package room

import (
	"errors"
	"path"
	"strings"
	"unicode"
)

// This file holds the two rules that stop a caller-authored string from
// becoming a host-mediated effect (CW-20260825-0077, ADR 0003 §2.5).
//
// Both existed already, in one workflow each, and were absent from the others.
// `tangent.file-picker` held its selection URIs to `artifact://` and
// `tangent.whiteboard` held its asset URIs to the same rule, while
// `tangent.diff-review`, `tangent.output_render`, and `tangent.form-collect`
// carried the same field shapes with no rule at all. A validation that one
// workflow performs is a convention; the same validation in one function that
// every workflow calls is a boundary.

// ErrUnmediatedReference is returned when a reference names something this
// host would have to reach out for.
//
// The refusal is deliberately about the *class* of reference and not about the
// specific scheme: `https://`, `http://`, `file://`, `data:`, and `blob:` are
// refused for one reason, which is that resolving any of them is an effect the
// definition did not declare and this host did not grant.
var ErrUnmediatedReference = errors.New("room: reference is not a mediated artifact reference")

// NormalizeArtifactURI is the single rule for "this reference names durable
// content Tangent already holds".
//
// `artifact://<id>` is the only accepted form, and empty is accepted because a
// reference may legitimately carry a name and a size and no locator at all.
// Everything else — a remote origin, a local file path, an inline payload — is
// a request for an effect, and an effect goes through internal/effect with a
// declared capability, a handle, a participant intent, and a receipt. It does
// not go through a string in an envelope.
func NormalizeArtifactURI(uri string) (string, error) {
	trimmed := strings.TrimSpace(uri)
	if trimmed == "" {
		return "", nil
	}
	if !strings.HasPrefix(trimmed, artifactURIScheme) {
		return "", ErrUnmediatedReference
	}
	if strings.TrimPrefix(trimmed, artifactURIScheme) == "" {
		return "", ErrUnmediatedReference
	}
	return trimmed, nil
}

const artifactURIScheme = "artifact://"

// SafeExportFilename reduces a caller-declared download name to something safe
// to hand a browser.
//
// The name reaches `link.download` in ui/src/components/envelopes/OutputRender.tsx,
// which is the closest thing the shipped SPA has to `export.download`
// (ADR 0003 §2.5). Until now it was carried through with `TrimSpace` and
// nothing else, so a caller chose the name a file landed under in the
// operator's Downloads folder — including its directory separators and its
// leading dot.
//
// This does not make the download brokered; a same-origin renderer can build
// a Blob and name it whatever it likes, which is why `export.download` is
// [effect.MediationDeclared] rather than enforced. What it does is stop the
// *caller* from choosing the name through a field the host persists, which is
// the half of the problem the host actually controls.
//
// An empty or wholly-rejected name returns "", and the renderer falls back to
// its own derived name, which is already sanitized.
func SafeExportFilename(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	// A separator of either flavor, so a Windows-shaped name cannot smuggle a
	// directory past a Unix-shaped check.
	base := path.Base(strings.ReplaceAll(trimmed, `\`, "/"))
	base = strings.Map(func(r rune) rune {
		// Control characters, NUL, and the path separators that survived Base
		// are dropped rather than replaced: a filename is a label, and a
		// mangled label is better than a placeholder that reads as content.
		if r == 0 || unicode.IsControl(r) || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, base)
	base = strings.TrimSpace(base)
	// A name that is only dots is `.`, `..`, or a hidden file with no stem —
	// none of which is a download name anyone chose.
	if strings.Trim(base, ".") == "" {
		return ""
	}
	base = strings.TrimLeft(base, ".")
	if len(base) > maximumExportFilenameBytes {
		base = base[:maximumExportFilenameBytes]
	}
	return base
}

// maximumExportFilenameBytes bounds a download name. 128 is well past any name
// a person would choose and well short of the limits a filesystem or a header
// starts truncating at, so a long name is refused here rather than mangled
// somewhere with a worse error.
const maximumExportFilenameBytes = 128
