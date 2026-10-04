package pluginhost

import "regexp"

// Diagnostic text is untrusted even when it is only a bounded stderr tail.
// These standard credential patterns do not require Tangent to hold plugin config.
var diagnosticSecrets = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(\b(?:[a-z][a-z0-9_]*_)?(?:token|secret|password|api[_-]?key|access[_-]?key)["']?\s*[:=]\s*["']?)[^\s"',;]+`),
	regexp.MustCompile(`(?i)(\bBearer\s+)[a-z0-9._~+/-]+=*`),
	regexp.MustCompile(`(https?://)[^\s/@]+:[^\s/@]+@`),
	regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9]{8,}|github_pat_[A-Za-z0-9_]{8,})`),
}

func redactPluginDiagnostic(text string) string {
	for i, pattern := range diagnosticSecrets {
		replacement := "${1}[redacted]"
		if i == 2 {
			replacement = "${1}[redacted]@"
		}
		if i == 3 {
			replacement = "[redacted]"
		}
		text = pattern.ReplaceAllString(text, replacement)
	}
	return text
}
