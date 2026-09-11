package almanac

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// This file is the plugin's Almanac dependency, and it must stay the only file
// in this repository that knows Almanac exists.
//
// That placement is the decision, not an accident of layout. A future reader
// asking "does Tangent know about Almanac" should get one file back. Tangent
// core stays domain-free, Almanac stays an engine that is called and returns,
// and the dependency lives in userland — a plugin — because that is the one
// place a dependency is a feature rather than a leak.
//
// # Do not extract a shared HTTP client out of this file
//
// The two application plugins that came before this one have clients that look
// alike and are not interchangeable: a distinguishable unavailability error, an
// optional bearer token, and a readiness probe are each spelled differently per
// application. Factoring them into a shared base would put a second
// application's name in a file that is not this one, and the grep above stops
// answering. Duplication here IS the boundary.
//
// # Verify these routes against the RUNNING service
//
// Not against its tool descriptions, its README, or a task's brief. Both
// existing plugins were handed route names that were wrong, and both found out
// by calling. An HTTP door's request shape may also differ from the same
// application's MCP tool: different field names, nested objects, keys that are
// Go field names rather than wire names, and unknown top-level fields rejected
// rather than ignored. live_test.go is how you check the service; the byte
// assertions in plugin_test.go are how you keep what you learned.
//
// # A board reads. Reading is not using.
//
// If Almanac has any signal derived from access — a hit counter, a "last
// viewed", a recency rank, an LRU — do NOT feed it from here. The records on a
// board are the ones Almanac's own query chose; reinforcing them teaches that
// query its own guess. Displaying a record is not a person using it.
//
// **If Almanac is down, this reports it and nothing else in Tangent notices.**
// Every call returns an error a tool result can carry rather than panicking,
// retrying forever, or caching a stale answer that would be indistinguishable
// from a fresh one.

// DefaultBaseURL is where Almanac listens on this machine.
const DefaultBaseURL = "http://127.0.0.1:8099"

// BaseURLEnv overrides DefaultBaseURL. The plugin reads it itself rather than
// asking the host: GetConfig is deliberately unimplemented (plugin
// configuration has no owner in Tangent yet), and a plugin reading its own
// environment is userland doing userland's job.
const BaseURLEnv = "TANGENT_ALMANAC_API_URL"

// TokenEnv optionally supplies a bearer token. It is sent only when set, so a
// loopback Almanac that wants no auth header does not get one.
const TokenEnv = "TANGENT_ALMANAC_TOKEN"

// requestTimeout bounds one call. A board is opened by a person waiting for it,
// so a hung dependency has to become a visible refusal quickly rather than a
// spinner.
const requestTimeout = 10 * time.Second

// maximumResponseBytes bounds one answer. A board of a few hundred cards is
// well inside it; a response that is not is a symptom, not a payload.
const maximumResponseBytes = 8 << 20

// ErrUnavailable reports that Almanac could not be reached or answered
// unusably. It is distinguishable so a tool result can say "Almanac is down"
// rather than "something failed", which is the difference between an operator
// restarting a service and an operator reading Tangent's logs.
var ErrUnavailable = errors.New("almanac: Almanac is unavailable")

// Record is the subset of a Almanac record this board renders. It is
// deliberately not every field Almanac returns: a field nothing displays is a
// field that would silently become part of this plugin's contract.
type Record struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Status    string   `json:"status"`
	Tags      []string `json:"tags"`
	UpdatedAt string   `json:"updated_at"`
}

// ListFilters are the facets Almanac's own list surface already takes. They
// are passed through verbatim; this plugin invents no filter vocabulary of its
// own — an agent that knows how to list Almanac records already knows how to
// open a board of them.
type ListFilters struct {
	Statuses []string `json:"statuses,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Search   string   `json:"search,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

// RecordPage is one bounded page, and whether Almanac held more.
//
// It is a struct rather than a slice for one field: More. A board that sent a
// cut set and reported only a count reads as the whole set, and a participant
// who then filters it and finds nothing cannot tell an absence from a scope.
// A bounded card set says it was bounded — see ScopeSentence.
//
// Whether a total belongs beside it is your application's answer. If its list
// surface reports one, carry it and say "N of M". If it does not, the extra row
// this asks for is the whole truncation signal and "and there are more" is the
// honest sentence; do not add a counting call on the path of a person waiting
// for a board without deciding the cost is worth it.
type RecordPage struct {
	Records []Record
	More    bool
}

// Client is a Almanac HTTP client.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient builds a client against baseURL, falling back to the local default.
func NewClient(baseURL string, token string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// BaseURL is where this client points. Reported in the board's `source` block
// so a reader can tell which Almanac they are looking at.
func (c *Client) BaseURL() string { return c.baseURL }

// List returns the records matching filters, bounded by the card limit.
//
// The limit sent to Almanac is one higher than the caller's. That extra row is
// never rendered; it is the whole truncation signal, and it costs one record on
// a request already in flight where a count would cost a second round trip.
func (c *Client) List(ctx context.Context, filters ListFilters) (RecordPage, error) {
	limit := clampCards(filters.Limit)
	query := url.Values{}
	if len(filters.Statuses) > 0 {
		query.Set("status", strings.Join(filters.Statuses, ","))
	}
	if len(filters.Tags) > 0 {
		query.Set("tags", strings.Join(filters.Tags, ","))
	}
	if filters.Search != "" {
		query.Set("search", filters.Search)
	}
	query.Set("limit", strconv.Itoa(limit+1))

	var response struct {
		Records []Record `json:"records"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/records?"+query.Encode(), nil, &response); err != nil {
		return RecordPage{}, err
	}
	if len(response.Records) > limit {
		return RecordPage{Records: response.Records[:limit], More: true}, nil
	}
	return RecordPage{Records: response.Records}, nil
}

// Apply moves one record into another status.
//
// ONE WRITE, NOT A WRITE SURFACE. This is the only method here that changes
// anything in Almanac, and keeping it one call wide is deliberate. Adding a
// second is a decision about Almanac — whether the act it performs is
// mechanical in Almanac's own terms — and not a decision about this plugin.
//
// The test is not "does the API have an endpoint for it". Tesseract has a write
// endpoint for promotion and promotion is still authored: in an append-only
// store it is a new immutable revision with `supersedes`, which is the same act
// as a reword. An API that will do a thing is not the same as a thing being
// mechanical.
func (c *Client) Apply(ctx context.Context, recordID, status string) error {
	body := map[string]any{"status": status}
	return c.do(ctx, http.MethodPost,
		"/api/v1/records/"+url.PathEscape(recordID)+"/status", body, nil)
}

// Health reports whether Almanac is answering at all. It is what the board's
// `source` block and a refusal message are built from.
//
// Use Almanac's own readiness route if it has one. This is a cheap idempotent
// read standing in for one, which is honest only as long as it stays cheap.
func (c *Client) Health(ctx context.Context) error {
	var ignored json.RawMessage
	return c.do(ctx, http.MethodGet, "/api/v1/records?limit=1", nil, &ignored)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("almanac: encode request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}
	// #nosec G704 -- the URL is `c.baseURL` (operator configuration, from
	// TANGENT_ALMANAC_API_URL or the loopback default) joined to a `path`
	// literal from this file. gosec's taint analysis flags it because the base
	// reaches here from os.Getenv, which is true and is the point: pointing at
	// a named Almanac is what the environment override is for. Caller-supplied
	// values reach `path` only as url.Values.Encode() output, percent-encoded
	// into the query of a fixed route, so none of them can move the host, the
	// scheme or the path.
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return fmt.Errorf("almanac: build request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	response, err := c.http.Do(request) // #nosec G704 -- see the request above
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, c.baseURL, err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes))
	if err != nil {
		return fmt.Errorf("%w: reading %s: %w", ErrUnavailable, path, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Almanac's own message is carried through rather than replaced. A
		// refusal that names the remedy is worth more than the status code,
		// and a message that said only "422" would throw that away.
		return fmt.Errorf("almanac refused %s %s: %s",
			method, path, refusalMessage(response.StatusCode, raw))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: decoding %s: %w", ErrUnavailable, path, err)
	}
	return nil
}

// refusalMessage extracts Almanac's error text, falling back to the status line.
func refusalMessage(status int, raw []byte) string {
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &body); err == nil {
		if body.Error != "" {
			return body.Error
		}
		if body.Message != "" {
			return body.Message
		}
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return http.StatusText(status)
	}
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	return text
}
