package pluginhost

import (
	"regexp"
	"strings"
	"unicode"
)

const pluginStderrBytes = 4096
const sensitiveName = `(?:token|secret|passw(?:or)?d|key|auth|credential)`

// Best-effort diagnostic scrubbing is deliberately independent of plugin config.
// RE2 patterns and fixed-pass normalization keep work linear in retained bytes.
// This cannot detect arbitrary secrets or replace the plugin's own log hygiene.
var diagnosticSecrets = []*regexp.Regexp{
	regexp.MustCompile(`(?im)(\b[a-z0-9_-]*` + sensitiveName + `[a-z0-9_-]*["']?\s*=\s*)[^\s;\n][^\n]*`),
	regexp.MustCompile(`(?i)(["'][^"'\n]*` + sensitiveName + `[^"'\n]*["']\s*:\s*)(?:"[^"\n]*"|'[^'\n]*'|[^\n,}]+)`),
	regexp.MustCompile(`(?im)(\bAuthorization\s*:\s*(?:Basic|Bearer|Token)\s+)[^\n]+`),
	regexp.MustCompile(`(?im)((?:x-[a-z0-9_-]*key|client_secret)\s*:\s*)[^\n]+`),
	regexp.MustCompile(`(?i)(\bBearer\s+)[a-z0-9._~+/-]+=*`),
	regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/@]+@`),
	regexp.MustCompile(`(?i)(--[a-z0-9_-]*` + sensitiveName + `[a-z0-9_-]*\s+)[^\n]+`),
	regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9]{8,}|github_pat_[A-Za-z0-9_]{8,}|xox[baprs]-[A-Za-z0-9-]+|AKIA[A-Z0-9]{12,})`),
}

var diagnosticEscapes = strings.NewReplacer(`\"`, `"`, `\'`, `'`, `\u0022`, `"`, `\u003d`, `=`, `\u003D`, `=`, `\u003a`, `:`, `\u003A`, `:`, `\u200b`, "")

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

func normalizeDiagnostic(text string) string {
	// Decode individual percent escapes rather than abandoning the whole line
	// when one unrelated malformed escape is present.
	var decoded strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '%' && i+2 < len(text) {
			a, okA := hexDigit(text[i+1])
			b, okB := hexDigit(text[i+2])
			if okA && okB {
				decoded.WriteByte(a<<4 | b)
				i += 2
				continue
			}
		}
		decoded.WriteByte(text[i])
	}
	return strings.Map(func(r rune) rune {
		if r >= 0xff01 && r <= 0xff5e {
			r -= 0xfee0
		}
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		if r != '\n' && !unicode.IsPrint(r) {
			return '?'
		}
		return r
	}, diagnosticEscapes.Replace(decoded.String()))
}

func redactPluginDiagnostic(text string) string {
	// The driver retains at most this many raw stderr bytes. At a full window
	// we cannot know whether its first line is complete, so discard it. A tail
	// with no subsequent newline conveys no safe diagnostic context.
	if len(text) >= pluginStderrBytes {
		if end := strings.IndexByte(text, '\n'); end >= 0 {
			text = text[end+1:]
		} else {
			text = ""
		}
	}
	text = normalizeDiagnostic(text)
	for i, pattern := range diagnosticSecrets {
		replacement := "${1}[redacted]"
		if i == 5 {
			replacement = "${1}[redacted]@"
		}
		if i == 7 {
			replacement = "[redacted]"
		}
		text = pattern.ReplaceAllString(text, replacement)
	}
	return strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) || unicode.Is(unicode.Cf, r) {
			return '?'
		}
		return r
	}, text)
}
