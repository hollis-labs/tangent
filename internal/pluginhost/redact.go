package pluginhost

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const pluginStderrBytes = 4096

// Credential names are closed: ordinary words and metadata such as monkey,
// author, project_key, auth-mode and keyfile retain their diagnostic meaning.
const sensitiveName = `(?:(?:api|private|secret|access|db|client|session|refresh|signing|encryption)_)*(?:tokens?|secrets?|passw(?:or)?d|pwd|key|auth|credentials?|jwt|dsn)|MYTOKEN|PGPASSWORD|pass[\n ]+word`

// Each pattern captures only the secret value. Matching uses a normalized view;
// replacements apply to original byte spans, preserving unrelated escaped text.
var diagnosticSecrets = []*regexp.Regexp{
	regexp.MustCompile(`(?im)\b(?:` + sensitiveName + `)\s*=\s*([^\s;\n][^\n]*)`),
	regexp.MustCompile(`(?i)["'](?:` + sensitiveName + `)["']\s*:\s*("[^"\n]*"|'[^'\n]*'|[^\n,}]+)`),
	regexp.MustCompile(`(?im)\bAuthorization\s*:\s*(?:Basic|Bearer|Token)\s*([^\n]+)`),
	regexp.MustCompile(`(?im)\b(?:x-api-key|client_secret|cookie|set-cookie)\s*:\s*([^\n]+)`),
	regexp.MustCompile(`(?i)\bBearer\s+([a-z0-9._~+/-]+=*)`),
	regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s/@:]+:([^\s/@]+)@`),
	regexp.MustCompile(`(?i)(?:mysql|postgres(?:ql)?|redis|mongodb)://([^\s/@]+)@`),
	regexp.MustCompile(`(?i)--(?:` + sensitiveName + `)\s+([^\n]+)`),
	regexp.MustCompile(`\b((?:sk-[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9]{8,}|github_pat_[A-Za-z0-9_]{8,}|xox[baprs]-[A-Za-z0-9-]+|AKIA[A-Z0-9]{12,}))`),
}

type diagnosticSpan struct{ start, end int }

func hexDigit(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}

// Preserve raw line boundaries. Encoded newlines are value bytes, not boundaries
// at which an assignment's redaction can end. Every view byte retains its origin.
func diagnosticView(text string) (string, []diagnosticSpan) {
	decoded := make([]byte, 0, len(text))
	origins := make([]diagnosticSpan, 0, len(text))
	for i := 0; i < len(text); {
		start := i
		var value byte
		if text[i] == '%' && i+2 < len(text) {
			a, okA := hexDigit(text[i+1])
			b, okB := hexDigit(text[i+2])
			if okA && okB {
				value = a<<4 | b
				i += 3
			} else {
				value = text[i]
				i++
			}
		} else if text[i] == '\\' && i+1 < len(text) && (text[i+1] == '"' || text[i+1] == '\'') {
			value = text[i+1]
			i += 2
		} else if text[i] == '\\' && i+5 < len(text) && text[i+1] == 'u' {
			code := 0
			valid := true
			for n := i + 2; n < i+6; n++ {
				digit, ok := hexDigit(text[n])
				valid = valid && ok
				code = code*16 + int(digit)
			}
			if valid && (code == 0x22 || code == 0x3d || code == 0x3a) {
				value = byte(code)
				i += 6
			} else if valid && code == 0x200b {
				i += 6
				continue
			} else {
				value = text[i]
				i++
			}
		} else {
			value = text[i]
			i++
		}
		// Percent-encoded newline must never terminate a raw line's match.
		if value == '\n' && i-start > 1 {
			value = ' '
		}
		decoded = append(decoded, value)
		origins = append(origins, diagnosticSpan{start, i})
	}
	var view strings.Builder
	spans := make([]diagnosticSpan, 0, len(decoded))
	for i := 0; i < len(decoded); {
		r, size := utf8.DecodeRune(decoded[i:])
		origin := diagnosticSpan{origins[i].start, origins[i+size-1].end}
		i += size
		if r >= 0xff01 && r <= 0xff5e {
			r -= 0xfee0
		}
		if unicode.Is(unicode.Cf, r) {
			continue
		}
		if r != '\n' && unicode.IsSpace(r) {
			r = ' '
		}
		if r != '\n' && !unicode.IsPrint(r) {
			r = '?'
		}
		before := view.Len()
		view.WriteRune(r)
		for n := before; n < view.Len(); n++ {
			spans = append(spans, origin)
		}
	}
	return view.String(), spans
}

// redactRawDiagnostic is idempotent and preserves physical line boundaries for
// further library wrapping/redaction. Display sanitization happens afterwards.
func redactRawDiagnostic(text string) string {
	view, origins := diagnosticView(text)
	// A difference mask merges overlaps in one byte pass, without sorting or
	// repeated rewriting. Fixed RE2 passes and normalization stay linear.
	mask := make([]int, len(text)+1)
	for _, pattern := range diagnosticSecrets {
		for _, match := range pattern.FindAllStringSubmatchIndex(view, -1) {
			if match[2] < 0 || match[3] <= match[2] {
				continue
			}
			mask[origins[match[2]].start]++
			mask[origins[match[3]-1].end]--
		}
	}
	var out strings.Builder
	active, previous := 0, 0
	for i := range len(text) {
		active += mask[i]
		if active > 0 {
			if previous == 0 {
				out.WriteString("[redacted]")
			}
		} else {
			out.WriteByte(text[i])
		}
		previous = active
	}
	return out.String()
}

func redactPluginDiagnostic(text string) string {
	return sanitizePluginDiagnostic(redactRawDiagnostic(text))
}
func sanitizePluginDiagnostic(text string) string {
	return strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) || unicode.Is(unicode.Cf, r) {
			return '?'
		}
		return r
	}, text)
}
