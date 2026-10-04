package pluginhost

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const pluginStderrBytes = 4096

// Names are matched as underscore, hyphen or camelCase segments. A prefixed
// key requires a credential prefix; bare key/auth/pwd values are also scrubbed.
var diagnosticAssignment = regexp.MustCompile(`(?im)(?:^|[^a-z0-9_-])(pass[\n ]+word|[a-z_][a-z0-9_-]*)[ ]*=[ ]*`)
var diagnosticJSONName = regexp.MustCompile(`["']([A-Za-z0-9_.-]+)["'][ ]*:[ ]*`)
var diagnosticFlag = regexp.MustCompile(`--([a-zA-Z][a-zA-Z0-9_-]*)[ ]+`)
var diagnosticNextField = regexp.MustCompile(`^[ ]+[a-zA-Z_][a-zA-Z0-9_-]*[ ]*=`)

// Fixed patterns capture only the credential bytes. Matches are mapped back to
// original offsets so ordinary percent escapes and diagnostic text stay intact.
var diagnosticSecrets = []*regexp.Regexp{
	regexp.MustCompile(`(?im)\bAuthorization[ ]*:[ ]*([^\n]+)`),
	regexp.MustCompile(`(?im)\b(?:x-api-key|client_secret|cookie|set-cookie)[ ]*:[ ]*([^\n]+)`),
	regexp.MustCompile(`(?i)\bBearer[ ]+([a-z0-9._~+/-]+=*)`),
	regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://([^\s/@]+)@`),
	regexp.MustCompile(`\b[a-zA-Z0-9._-]+:([^\s@]+)@(?:tcp|unix)\(`),
	regexp.MustCompile(`\b((?:sk-[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9]{8,}|github_pat_[A-Za-z0-9_]{8,}|xox[baprs]-[A-Za-z0-9-]+|(?:AKIA|ASIA)[A-Z0-9]{12,}|glpat-[A-Za-z0-9_-]{8,}|AIza[A-Za-z0-9_-]{8,}|npm_[A-Za-z0-9_-]{8,}|SG\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}))`),
	regexp.MustCompile(`\b(eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`),
	regexp.MustCompile(`(?s)(-----BEGIN (?:[A-Z0-9]+ )?PRIVATE KEY-----.*?(?:-----END (?:[A-Z0-9]+ )?PRIVATE KEY-----|$))`),
}

func credentialName(name string) bool {
	if strings.EqualFold(strings.Join(strings.Fields(name), ""), "password") {
		return true
	}
	// Historical compact spellings have no case/segment boundary.
	switch strings.ToLower(name) {
	case "key", "auth", "pwd", "mytoken", "pgpassword", "apikey", "token", "tokens", "secret", "secrets", "password", "passwd", "pass", "passphrase", "credential", "credentials", "jwt", "dsn":
		return true
	}
	var segmented strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' && i > 0 {
			previous := name[i-1]
			nextLower := i+1 < len(name) && name[i+1] >= 'a' && name[i+1] <= 'z'
			if previous >= 'a' && previous <= 'z' || previous >= 'A' && previous <= 'Z' && nextLower {
				segmented.WriteByte('_')
			}
		}
		segmented.WriteRune(unicode.ToLower(r))
	}
	parts := strings.FieldsFunc(segmented.String(), func(r rune) bool { return r == '_' || r == '-' })
	if len(parts) == 0 {
		return false
	}
	last := parts[len(parts)-1]
	if last == "base" && len(parts) >= 3 && parts[len(parts)-2] == "key" && parts[len(parts)-3] == "secret" {
		return true // conventional SECRET_KEY_BASE
	}
	switch last {
	case "token", "tokens", "secret", "secrets", "password", "passwd", "pass", "passphrase", "credential", "credentials", "jwt", "dsn":
		return true
	case "key":
		for _, prefix := range parts[:len(parts)-1] {
			switch prefix {
			case "api", "private", "secret", "access", "db", "client", "session", "refresh", "signing", "encryption":
				return true
			}
		}
	}
	return false
}

// Values never consume a physical newline. Quoted values end at their quote;
// query/form values end at &, and logfmt values end before the next field.
// A standalone unquoted assignment still covers spaces/commas in a secret.
func diagnosticValueEnd(view string, start, end int, inline bool) int {
	if start >= len(view) || view[start] == '\n' || view[start] == ';' {
		return start
	}
	if view[start] == '"' || view[start] == '\'' {
		if offset := strings.IndexByte(view[start+1:end], view[start]); offset >= 0 {
			return start + offset + 2
		}
	}
	for i := start; i < end; i++ {
		if view[i] == '&' || view[i] == '"' || view[i] == '\'' || inline && view[i] == ' ' {
			return i
		}
		if view[i] == ' ' {
			if diagnosticNextField.FindStringIndex(view[i:end]) != nil {
				return i
			}
			// Do not re-scan a run of spaces once per byte.
			for i+1 < end && view[i+1] == ' ' {
				i++
			}
		}
	}
	return end
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
			value = '?'
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
	addRange := func(start, end int) {
		if end > start {
			mask[origins[start].start]++
			mask[origins[end-1].end]--
		}
	}
	for _, named := range []struct {
		pattern *regexp.Regexp
		flag    bool
	}{
		{diagnosticAssignment, false}, {diagnosticJSONName, false}, {diagnosticFlag, true},
	} {
		lineStart, lineEnd, queryAt := 0, -1, -1
		for _, match := range named.pattern.FindAllStringSubmatchIndex(view, -1) {
			if !credentialName(view[match[2]:match[3]]) {
				continue
			}
			start := match[1]
			if start > lineEnd {
				lineStart = strings.LastIndexByte(view[:start], '\n') + 1
				lineEnd = len(view)
				if offset := strings.IndexByte(view[start:], '\n'); offset >= 0 {
					lineEnd = start + offset
				}
				line := view[lineStart:lineEnd]
				queryAt = -1
				for _, marker := range []string{"://", "?", "&"} {
					if offset := strings.Index(line, marker); offset >= 0 && (queryAt < 0 || offset < queryAt) {
						queryAt = offset
					}
				}
			}
			name := strings.ToLower(view[match[2]:match[3]])
			valueStart := start
			if valueStart < lineEnd && (view[valueStart] == '"' || view[valueStart] == '\'') {
				valueStart++
			}
			if name == "pwd" && valueStart < lineEnd && view[valueStart] == '/' {
				continue // absolute working directories are diagnostic context
			}
			bareValue := name == "key" || name == "auth" || name == "pwd" || name == "pass"
			inline := named.flag || bareValue || queryAt >= 0 && lineStart+queryAt < match[2]
			addRange(start, diagnosticValueEnd(view, start, lineEnd, inline))
		}
	}
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
