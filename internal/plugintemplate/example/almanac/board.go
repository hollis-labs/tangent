package almanac

import (
	"fmt"
	"sort"
	"strings"
)

// This file maps Almanac records onto the domain-free board shape. Every
// mapping in it is mechanical, and that is the point of the whole pattern
// (ADR 0007 §6): the model never shapes a payload, because there is nothing
// here a model would be better at than a `for` loop.
//
// The direction of the dependency is what keeps Tangent domain-free while a
// Almanac board renders: `tangent.app-board` describes a board of cards in columns,
// this file knows that a Almanac status is a column and a Almanac tag is a
// badge, and nothing in Tangent core learns either fact.

// DefaultCards and MaximumCards bound one board.
//
// MEASURE THESE; do not keep the scaffold's numbers. The ceiling is the kind's
// `inline_payload_limit_bytes` divided by what one of YOUR cards costs, and the
// two existing boards differ by four times on exactly that: a Tesseract summary
// card is ~700 bytes and a Torque card is ~2.8 KB, because a Torque description
// becomes the card body verbatim. Marshal a realistic card, divide, and write
// the measurement into this comment so the next reader can check it.
//
// A cap is what makes the truncation sentence below necessary, and the sentence
// is what makes the cap honest. Neither works alone.
const (
	DefaultCards = 60
	MaximumCards = 80
)

// clampCards resolves a requested card limit. It is applied where the tool
// input becomes filters — so the number recorded on the envelope is the
// effective one — and again inside the client, so a board opened before a cap
// changed still re-queries bounded.
func clampCards(limit int) int {
	if limit <= 0 {
		return DefaultCards
	}
	if limit > MaximumCards {
		return MaximumCards
	}
	return limit
}

// ActiveStatuses is the default column set: the statuses a person is looking at
// when they say "my work". Terminal statuses are excluded by default because a
// board that opens with every finished record is a board nobody scrolls.
var ActiveStatuses = []string{"open", "active", "review"}

// statusLabels renders Almanac's status vocabulary for a column header.
// Anything not named here is title-cased from its own slug, so a status
// Almanac adds later still gets a readable column instead of being dropped.
var statusLabels = map[string]string{
	"open":   "Open",
	"active": "Active",
	"review": "Review",
	"done":   "Done",
}

// Card is one board card. It mirrors the kind's request schema rather than
// Almanac's record shape — a card has a title and badges, not a status and a
// workspace.
type Card struct {
	ID       string         `json:"id"`
	Title    string         `json:"title"`
	Subtitle string         `json:"subtitle,omitempty"`
	Badges   []Badge        `json:"badges,omitempty"`
	Body     string         `json:"body,omitempty"`
	Fields   map[string]any `json:"fields,omitempty"`
}

// Badge is one card badge.
type Badge struct {
	ID    string `json:"id,omitempty"`
	Label string `json:"label"`
	Tone  string `json:"tone,omitempty"`
}

// Column is one board column.
type Column struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	CardIDs []string `json:"card_ids"`
}

// Filter is one filter-bar facet. Filters are a VIEW over the cards this plugin
// sent, never a query the host re-runs — see Sync.Scope for what the board tells
// the participant about that.
type Filter struct {
	ID       string         `json:"id"`
	Label    string         `json:"label"`
	Kind     string         `json:"kind"`
	Field    string         `json:"field,omitempty"`
	Options  []FilterOption `json:"options,omitempty"`
	Selected []string       `json:"selected,omitempty"`
}

// FilterOption is one selectable facet value.
type FilterOption struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
	Count int    `json:"count"`
}

// Source labels which application supplied the cards. It is a label for the
// reader; Tangent calls nothing through it.
type Source struct {
	App   string `json:"app"`
	Label string `json:"label"`
}

// Sync is what the board needs in order to talk back to this plugin without an
// agent turn.
//
// Endpoint is a same-origin path under the reserved plugin prefix. The renderer
// refuses anything else — a caller-supplied absolute URL would make the board a
// general-purpose fetch surface, which is what the host-mediated effect broker
// exists to prevent.
type Sync struct {
	Enabled bool `json:"enabled"`
	// Endpoint is the path the sync button POSTs to.
	Endpoint string `json:"endpoint"`
	// Label names the button.
	Label string `json:"label,omitempty"`
	// StageLabel names the control that stages a card into another column.
	// Empty means staging is not offered even when Enabled is true, which is
	// how a read-only board is expressed.
	StageLabel string `json:"stage_label,omitempty"`
	// Scope says, in the board's own words, which records were sent.
	Scope string `json:"scope,omitempty"`
}

// BoardData is the envelope's `data` block.
type BoardData struct {
	BoardID   string   `json:"board_id"`
	Title     string   `json:"title,omitempty"`
	Source    Source   `json:"source"`
	Columns   []Column `json:"columns"`
	Cards     []Card   `json:"cards"`
	Filters   []Filter `json:"filters,omitempty"`
	Sync      *Sync    `json:"sync,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

// BuildBoard maps a record list onto the board shape.
//
// statuses is the column order. A record whose status is outside it still
// becomes a card and lands in a column of its own appended at the end, because
// dropping it would make the board quietly disagree with Almanac about how much
// work exists.
func BuildBoard(
	boardID string,
	title string,
	records []Record,
	statuses []string,
	source Source,
	sync *Sync,
	updatedAt string,
) BoardData {
	if len(statuses) == 0 {
		statuses = ActiveStatuses
	}
	cards := make([]Card, 0, len(records))
	byStatus := map[string][]string{}
	order := append([]string(nil), statuses...)
	known := map[string]bool{}
	for _, status := range order {
		known[status] = true
		// An allocated empty slice, not nil. A nil slice marshals to `null`,
		// and the kind's schema requires `card_ids` to be an array — so an
		// empty column would fail validation at the surface, a defect that only
		// appears when a board is opened on a status nobody is in.
		byStatus[status] = []string{}
	}
	for _, record := range records {
		cards = append(cards, CardFor(record))
		status := record.Status
		if !known[status] {
			known[status] = true
			order = append(order, status)
			byStatus[status] = []string{}
		}
		byStatus[status] = append(byStatus[status], record.ID)
	}

	columns := make([]Column, 0, len(order))
	for _, status := range order {
		columns = append(columns, Column{
			ID:      status,
			Label:   statusLabel(status),
			CardIDs: byStatus[status],
		})
	}

	return BoardData{
		BoardID:   boardID,
		Title:     title,
		Source:    source,
		Columns:   columns,
		Cards:     cards,
		Filters:   BuildFilters(records),
		Sync:      sync,
		UpdatedAt: updatedAt,
	}
}

// CardFor maps one record.
//
// The body goes through the kind's own markdown rendering and nothing here
// re-implements it. Anywhere an agent's or an application's prose is displayed,
// Tangent routes it through the shared <Markdown> component; a second markdown
// path in a plugin is how one surface starts disagreeing with the rest about
// what a list looks like. Say which of your fields are markdown in the tool
// schema (schemas.go), not in the kind's request schema — that file's bytes are
// hashed into the pin.
func CardFor(record Record) Card {
	badges := make([]Badge, 0, len(record.Tags))
	for _, tag := range record.Tags {
		badges = append(badges, Badge{ID: "tag:" + tag, Label: tag, Tone: "info"})
	}
	fields := map[string]any{
		"id":         record.ID,
		"status":     record.Status,
		"updated_at": record.UpdatedAt,
	}
	return Card{
		ID:       record.ID,
		Title:    record.Title,
		Subtitle: record.ID,
		Badges:   badges,
		Body:     record.Body,
		Fields:   fields,
	}
}

// BuildFilters derives the facet options from the cards actually sent.
//
// Counting from the sent set rather than from Almanac is deliberate and is the
// same rule the filters themselves follow: an option whose count came from a
// query the board cannot re-run would promise records the board does not hold.
func BuildFilters(records []Record) []Filter {
	tags := map[string]int{}
	for _, record := range records {
		for _, tag := range record.Tags {
			tags[tag]++
		}
	}
	filters := make([]Filter, 0, 2)
	filters = append(filters, Filter{
		ID: "search", Label: "Search", Kind: "text", Field: "title",
	})
	if options := facetOptions(tags); len(options) > 0 {
		filters = append(filters, Filter{
			ID: "tag", Label: "Tag", Kind: "multi", Field: "badges", Options: options,
		})
	}
	return filters
}

// facetOptions renders a count map as sorted options: most common first, ties
// broken by name so the order is stable across two syncs of the same data.
func facetOptions(counts map[string]int) []FilterOption {
	options := make([]FilterOption, 0, len(counts))
	for value, count := range counts {
		options = append(options, FilterOption{Value: value, Label: value, Count: count})
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].Count != options[j].Count {
			return options[i].Count > options[j].Count
		}
		return options[i].Value < options[j].Value
	})
	return options
}

// ScopeSentence is what the board tells the participant about its own card set.
//
// Two things have to come out of it, and both are defects someone has already
// shipped:
//
//   - A filter that finds nothing reads as a SCOPE rather than an emptiness.
//     The filter bar is a view over what was sent, not a query Tangent re-runs,
//     and without a sentence saying so a record that plainly exists in Almanac
//     looks missing.
//   - A cut set never reads as the whole set. "60 record(s)" alone is what a
//     participant filters against, finds nothing in, and cannot tell from an
//     absence.
//
// If Almanac's list surface reports a total, say "60 of 137" — it is strictly
// more useful. If it does not, say "and there are more" and do not invent one.
func ScopeSentence(filters ListFilters, page RecordPage) string {
	scope := fmt.Sprintf("%d record(s)", len(page.Records))
	if len(filters.Statuses) > 0 {
		scope += " in " + strings.Join(filters.Statuses, ", ")
	}
	if len(filters.Tags) > 0 {
		scope += ", tagged " + strings.Join(filters.Tags, "/")
	}
	if filters.Search != "" {
		scope += ", matching " + strconvQuote(filters.Search)
	}
	if page.More {
		scope += ", and there are more. This is NOT the whole set — the card " +
			"limit cut it. Raise the limit or narrow your filters to see the rest."
	} else {
		scope += "."
	}
	return scope + " Filters below narrow this set; press Sync to re-query Almanac."
}

// strconvQuote is strconv.Quote under a name this file can explain: the search
// term is the participant's own words and quoting it keeps a trailing space or
// an empty-looking term visible in the sentence.
func strconvQuote(value string) string { return "\"" + value + "\"" }

func statusLabel(status string) string {
	if label, ok := statusLabels[status]; ok {
		return label
	}
	if status == "" {
		return "Unset"
	}
	return strings.ToUpper(status[:1]) + strings.ReplaceAll(status[1:], "_", " ")
}

func (s *Sync) scopeOrEmpty() string {
	if s == nil {
		return ""
	}
	return s.Scope
}
