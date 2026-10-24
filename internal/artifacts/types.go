package artifacts

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Size ceilings. Text arrives inline in an MCP argument and binary arrives
// base64-encoded, so both are bounded well below what the transport would
// carry; a larger artifact belongs behind a file-upload route (ADR 0015).
const (
	MaxTextBytes   = 5 << 20
	MaxBinaryBytes = 25 << 20
	MaxMessageBody = 8000
)

var (
	ErrInvalidRequest   = errors.New("artifacts: invalid request")
	ErrTerminalConflict = errors.New("artifacts: another terminal outcome won")
	ErrNotFound         = errors.New("artifacts: artifact not found")
	ErrStaleRevision    = errors.New("artifacts: stale revision conflict")
	ErrTooLarge         = errors.New("artifacts: content too large")
)

// Message authors and kinds. A "changes_requested" message is the operator's
// request-changes sign-off decision: it leaves the artifact open.
const (
	AuthorOperator = "operator"
	AuthorAgent    = "agent"

	KindMessage          = "message"
	KindChangesRequested = "changes_requested"
	KindApproved         = "approved"
)

// FileInput is one revision's content, already validated and typed.
type FileInput struct {
	Filename  string
	MediaType string
	Data      []byte
	Note      string
}

// typeSpec says how one artifact type is carried and checked. The type set is
// open on the schema side and closed here: a type with no spec is refused.
type typeSpec struct {
	binary          bool
	json            bool
	defaultFilename string
	defaultMedia    string
	// allowed lists the sniffed media types a binary type accepts.
	allowed []string
}

var typeSpecs = map[string]typeSpec{
	"html":       {defaultFilename: "artifact.html", defaultMedia: "text/html"},
	"markdown":   {defaultFilename: "artifact.md", defaultMedia: "text/markdown"},
	"excalidraw": {json: true, defaultFilename: "artifact.excalidraw", defaultMedia: "application/json"},
	"flow":       {json: true, defaultFilename: "artifact.flow.json", defaultMedia: "application/json"},
	"mermaid":    {defaultFilename: "artifact.mmd", defaultMedia: "text/plain"},
	"d2":         {defaultFilename: "artifact.d2", defaultMedia: "text/plain"},
	"vega-lite":  {json: true, defaultFilename: "artifact.vl.json", defaultMedia: "application/json"},
	"tldraw":     {json: true, defaultFilename: "artifact.tldraw", defaultMedia: "application/json"},
	"figma":      {defaultFilename: "figma-link.txt", defaultMedia: "text/uri-list"},
	"image": {
		binary: true, defaultFilename: "image", defaultMedia: "image/png",
		allowed: []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml"},
	},
	"pdf": {
		binary: true, defaultFilename: "artifact.pdf", defaultMedia: "application/pdf",
		allowed: []string{"application/pdf"},
	},
	"video": {
		binary: true, defaultFilename: "artifact.mp4", defaultMedia: "video/mp4",
		allowed: []string{"video/mp4", "video/webm"},
	},
}

// KnownType reports whether the type has a spec.
func KnownType(name string) bool {
	_, ok := typeSpecs[name]
	return ok
}

// BuildFile turns the wire content fields into a validated FileInput for one
// artifact type. Exactly one of content and contentBase64 must be present.
func BuildFile(artifactType, content, contentBase64, filename, mediaType, note string) (FileInput, error) {
	spec, ok := typeSpecs[artifactType]
	if !ok {
		return FileInput{}, fmt.Errorf("%w: unsupported artifact type %q", ErrInvalidRequest, artifactType)
	}
	if (content == "") == (contentBase64 == "") {
		return FileInput{}, fmt.Errorf("%w: send exactly one of content and content_base64", ErrInvalidRequest)
	}
	var data []byte
	switch {
	case spec.binary:
		if content != "" {
			// SVG is the one text-shaped image; everything else must be base64.
			if artifactType != "image" || !looksLikeSVG([]byte(content)) {
				return FileInput{}, fmt.Errorf("%w: %s content must be base64 in content_base64", ErrInvalidRequest, artifactType)
			}
			data = []byte(content)
		} else {
			decoded, err := base64.StdEncoding.DecodeString(contentBase64)
			if err != nil {
				return FileInput{}, fmt.Errorf("%w: content_base64 is not valid base64: %w", ErrInvalidRequest, err)
			}
			data = decoded
		}
		if int64(len(data)) > MaxBinaryBytes {
			return FileInput{}, fmt.Errorf("%w: %d bytes exceeds the %d byte limit", ErrTooLarge, len(data), MaxBinaryBytes)
		}
	default:
		if contentBase64 != "" {
			return FileInput{}, fmt.Errorf("%w: %s content is text; send it in content", ErrInvalidRequest, artifactType)
		}
		data = []byte(content)
		if int64(len(data)) > MaxTextBytes {
			return FileInput{}, fmt.Errorf("%w: %d bytes exceeds the %d byte limit", ErrTooLarge, len(data), MaxTextBytes)
		}
		if !utf8.Valid(data) {
			return FileInput{}, fmt.Errorf("%w: content is not valid UTF-8", ErrInvalidRequest)
		}
		if spec.json && !json.Valid(data) {
			return FileInput{}, fmt.Errorf("%w: %s content is not valid JSON", ErrInvalidRequest, artifactType)
		}
	}
	if len(data) == 0 {
		return FileInput{}, fmt.Errorf("%w: content is empty", ErrInvalidRequest)
	}

	media := spec.defaultMedia
	if spec.binary {
		sniffed := sniffMedia(data)
		if !contains(spec.allowed, sniffed) {
			return FileInput{}, fmt.Errorf("%w: %s content sniffed as %q; allowed: %s",
				ErrInvalidRequest, artifactType, sniffed, strings.Join(spec.allowed, ", "))
		}
		media = sniffed
	}
	if artifactType == "figma" {
		if err := checkFigmaURL(strings.TrimSpace(string(data))); err != nil {
			return FileInput{}, err
		}
		data = []byte(strings.TrimSpace(string(data)))
	}
	// A caller-declared media type may only restate what the content already is.
	if mediaType != "" && mediaType != media {
		return FileInput{}, fmt.Errorf("%w: media_type %q does not match the content (%q)", ErrInvalidRequest, mediaType, media)
	}

	name := sanitizeFilename(filename)
	if name == "" {
		name = spec.defaultFilename
		if artifactType == "image" {
			name += imageExtension(media)
		}
	}
	if len(note) > MaxMessageBody {
		return FileInput{}, fmt.Errorf("%w: note exceeds %d characters", ErrInvalidRequest, MaxMessageBody)
	}
	return FileInput{Filename: name, MediaType: media, Data: data, Note: note}, nil
}

func sniffMedia(data []byte) string {
	if looksLikeSVG(data) {
		return "image/svg+xml"
	}
	sniffed := http.DetectContentType(data)
	if i := strings.Index(sniffed, ";"); i >= 0 {
		sniffed = sniffed[:i]
	}
	return sniffed
}

func looksLikeSVG(data []byte) bool {
	head := data
	if len(head) > 1024 {
		head = head[:1024]
	}
	text := strings.ToLower(strings.TrimSpace(string(head)))
	return strings.HasPrefix(text, "<svg") || (strings.HasPrefix(text, "<?xml") && strings.Contains(text, "<svg"))
}

func imageExtension(media string) string {
	switch media {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	default:
		return ""
	}
}

func checkFigmaURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" {
		return fmt.Errorf("%w: figma content must be an https figma.com URL", ErrInvalidRequest)
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "figma.com" && host != "www.figma.com" {
		return fmt.Errorf("%w: figma content must be a figma.com URL, got host %q", ErrInvalidRequest, host)
	}
	return nil
}

// sanitizeFilename keeps a base name made of safe characters. A filename is
// only ever a label for the download; the file on disk is addressed by
// revision number, never by this string.
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "." || name == "/" || name == ".." {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r == ' ':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.TrimLeft(b.String(), ".")
	if len(out) > 120 {
		out = out[len(out)-120:]
	}
	return out
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// Revision is one stored version of an artifact's content.
type Revision struct {
	Revision  int       `json:"revision"`
	Filename  string    `json:"filename"`
	MediaType string    `json:"media_type"`
	SizeBytes int64     `json:"size_bytes"`
	SHA256    string    `json:"sha256"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	relPath   string
}

// Message is one entry in an artifact's operator/agent thread.
type Message struct {
	Seq       int64     `json:"seq"`
	Author    string    `json:"author"`
	Kind      string    `json:"kind"`
	Body      string    `json:"body"`
	Revision  int       `json:"revision,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
