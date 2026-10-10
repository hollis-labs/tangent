package pluginui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"

	driver "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
)

// OwnerLease binds to the actual child driver tuple and its current-owner
// predicate. A manifest, UI label or native-tool tuple cannot create this lease.
// The composition owner supplies Current; no lease is constructed from HTTP.
type OwnerLease struct {
	Owner   driver.Owner
	Current func() bool
}

func validOwner(owner driver.Owner) bool {
	return validDigest(owner.HostInstance) && token.MatchString(owner.OwnerID) && !reservedIdentifier(owner.OwnerID) && len(owner.OwnerID) <= 128 && owner.OwnerGeneration > 0
}

// MaxActiveScopes bounds sealed frame custody independently of browser input.
const MaxActiveScopes = 32

type Store struct {
	mu        sync.Mutex
	origin    string
	retention *Retention
	scopes    map[string]*Delivery
	pending   int
}

// NewStore does not install routes or enable discovery. The retention identity
// is independent of host restart epochs, so old URLs never gain new meanings.
func NewStore(origin string, retention *Retention) (*Store, error) {
	u, err := url.Parse(origin)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.String() != origin || retention == nil {
		return nil, errors.New("pluginui: invalid immutable serving boundary")
	}
	retention.mu.Lock()
	err = retention.verify()
	retention.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &Store{origin: origin, retention: retention, scopes: make(map[string]*Delivery)}, nil
}

// Response is a copy of exact sealed response bytes and response policy.
type Response struct {
	Body                              []byte
	MediaType, CSP, PermissionsPolicy string
}

// Document comes from the trusted generated FrameDocument port, not plugin HTML.
type Document struct{ FrameID, HTML, CSP, PermissionsPolicy string }
type DocumentReview func(Document) error

type Delivery struct {
	mu        sync.Mutex
	releaseMu sync.Mutex
	store     *Store
	scope     string
	lease     OwnerLease
	graph     *Graph
	document  *Document
	revoked   bool
	stop      func(context.Context) error
}

// Provision reserves the scope durably BEFORE exposing any URLs. Stop must join
// the owned frame before returning nil; uncertainty retains sealed bytes while
// refusing delivery. The host supplies this callback, never a browser receipt.
func (s *Store) Provision(scope string, lease OwnerLease, graph *Graph, stop func(context.Context) error) (*Delivery, error) {
	if !validDigest(scope) || !validOwner(lease.Owner) || lease.Current == nil || !lease.Current() || graph == nil || stop == nil {
		return nil, errors.New("pluginui: incomplete current owner admission")
	}
	s.mu.Lock()
	if len(s.scopes)+s.pending >= MaxActiveScopes {
		s.mu.Unlock()
		return nil, errors.New("pluginui: sealed scope capacity exhausted")
	}
	s.pending++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.pending--; s.mu.Unlock() }()
	record, err := json.Marshal(struct {
		Scope           string
		Owner           driver.Owner
		Manifest, Graph string
	}{scope, lease.Owner, graph.manifest, graph.digest})
	if err != nil {
		return nil, err
	}
	if err = s.retention.reserve(scope, record); err != nil {
		return nil, err
	}
	// A changed owner after the durable reservation burns the scope, never reuses it.
	if !lease.Current() {
		return nil, errors.New("pluginui: owner ended during provision")
	}
	d := &Delivery{store: s, scope: scope, lease: lease, graph: graph, stop: stop}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.scopes[scope]; exists {
		return nil, errors.New("pluginui: scope already present")
	}
	s.scopes[scope] = d
	return d, nil
}
func (d *Delivery) basePath() string {
	o := d.lease.Owner
	return "/api/plugin-ui/sealed/" + d.store.retention.identity + "/" + o.HostInstance + "/" + o.OwnerID + "/" + strconv.FormatUint(o.OwnerGeneration, 10) + "/" + d.graph.manifest + "/" + d.scope
}
func (d *Delivery) URLs() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	urls := make(map[string]string, len(d.graph.modules))
	for _, module := range d.graph.modules {
		urls[module.ID] = d.store.origin + d.basePath() + "/" + module.SHA256 + "/" + module.ID + ".js"
	}
	return urls
}
func (d *Delivery) DocumentURL() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.store.origin + d.basePath() + "/frame.html"
}

// SealDocument freezes the generated dedicated document once. The reviewer
// must validate exact generated policy/graph correspondence. No default policy,
// srcdoc, source-URL rewrite or later document replacement is provided here.
func (d *Delivery) SealDocument(document Document, review DocumentReview) error {
	if review == nil || document.FrameID != d.scope || len(document.HTML) == 0 || len(document.HTML) > MaxArtifactBytes || document.CSP == "" || document.PermissionsPolicy == "" || strings.ContainsAny(document.CSP+document.PermissionsPolicy, "\r\n") {
		return errors.New("pluginui: invalid generated document")
	}
	if err := review(document); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.document != nil || d.revoked || !d.lease.Current() {
		return errors.New("pluginui: document unavailable")
	}
	d.document = &document
	return nil
}

// Read uses the entire canonical route, not caller-selected metadata aliases.
// Modules require a sealed document and current live owner. Redirects, fallback
// paths and mutable filesystem reads do not exist in this serving boundary.
func (s *Store) Read(path string) (Response, bool) {
	s.mu.Lock()
	deliveries := make([]*Delivery, 0, len(s.scopes))
	for _, d := range s.scopes {
		deliveries = append(deliveries, d)
	}
	s.mu.Unlock()
	for _, d := range deliveries {
		d.mu.Lock()
		if !d.revoked && d.document != nil && d.lease.Current() {
			base := d.basePath()
			if path == base+"/frame.html" {
				result := Response{Body: []byte(d.document.HTML), MediaType: "text/html; charset=utf-8", CSP: d.document.CSP, PermissionsPolicy: d.document.PermissionsPolicy}
				d.mu.Unlock()
				return result, true
			}
			for _, module := range d.graph.modules {
				if path == base+"/"+module.SHA256+"/"+module.ID+".js" {
					result := Response{Body: bytes.Clone(module.Bytes), MediaType: "text/javascript"}
					d.mu.Unlock()
					return result, true
				}
			}
		}
		d.mu.Unlock()
	}
	return Response{}, false
}
func (d *Delivery) Revoke() { d.mu.Lock(); d.revoked = true; d.mu.Unlock() }

// Release first fences delivery, then joins frame teardown. Byte custody stays
// in memory on cancellation/error. Only a confirmed stopped scope is removed;
// its durable reservation remains forever. Repeated successful release is inert.
func (d *Delivery) Release(ctx context.Context) error {
	d.releaseMu.Lock()
	defer d.releaseMu.Unlock()
	d.Revoke()
	d.store.mu.Lock()
	present := d.store.scopes[d.scope] == d
	d.store.mu.Unlock()
	if !present {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.stop(ctx); err != nil {
		return err
	}
	d.store.mu.Lock()
	delete(d.store.scopes, d.scope)
	d.store.mu.Unlock()
	return nil
}
