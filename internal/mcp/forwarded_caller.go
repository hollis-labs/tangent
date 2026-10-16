package mcp

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/hollis-labs/tangent/internal/interaction"
)

const (
	forwardedUserHeader    = "X-Forwarded-User-Id"
	forwardedSessionHeader = "X-Tether-Session-Id"
)

var canonicalSessionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (a *CallerAuth) parseTrustedForwarders(config string) error {
	if strings.TrimSpace(config) == "" {
		return nil
	}
	known := map[string]bool{}
	for _, credential := range a.credentials {
		known[credential.producer] = true
	}
	for index, entry := range strings.Split(config, ",") {
		name := strings.TrimSpace(entry)
		if !known[name] || a.trustedForwarders[name] {
			return fmt.Errorf("%s: entry %d must name a distinct configured producer", EnvTrustedForwarders, index+1)
		}
		a.trustedForwarders[name] = true
	}
	return nil
}

// forwardedCaller is called only after credential verification. A trusted
// producer attests the session; optional attribution never changes authority.
func (a *CallerAuth) forwardedCaller(headers http.Header, producer string) (interaction.ForwarderAttribution, bool) {
	var out interaction.ForwarderAttribution
	if a == nil || !a.trustedForwarders[producer] {
		return out, true
	}
	user, userPresent := headers[http.CanonicalHeaderKey(forwardedUserHeader)]
	session, sessionPresent := headers[http.CanonicalHeaderKey(forwardedSessionHeader)]
	if !userPresent && !sessionPresent {
		return out, true
	}
	if len(user) != 1 || len(session) != 1 || !canonicalSessionID.MatchString(session[0]) || user[0] != "session:"+session[0] {
		return out, false
	}
	out.Forwarder, out.SessionID = producer, session[0]
	for name, dest := range map[string]*string{
		"X-Tether-Agent-Urn":        &out.AgentURN,
		"X-Tether-Workstream-Id":    &out.WorkstreamID,
		"X-Tether-Launch-Id":        &out.LaunchID,
		"X-Tether-Project-Id":       &out.ProjectID,
		"X-Tether-Logical-Agent-Id": &out.LogicalAgentID,
	} {
		values, present := headers[http.CanonicalHeaderKey(name)]
		if !present {
			continue
		}
		if len(values) != 1 || !boundedForwardedLabel(values[0]) {
			return interaction.ForwarderAttribution{}, false
		}
		*dest = values[0]
	}
	return out, true
}

func boundedForwardedLabel(value string) bool {
	if len(value) == 0 || len(value) > 512 {
		return false
	}
	for i := range len(value) {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func stripForwardedIdentity(headers http.Header) {
	for name := range headers {
		if strings.EqualFold(name, forwardedUserHeader) || strings.HasPrefix(strings.ToLower(name), "x-tether-") {
			delete(headers, name)
		}
	}
}

// Each effective authority gets an independent SDK registry. References cover
// live GET streams and in-flight POSTs. Remove idle registries so trusted
// forwarders cannot accumulate an entry for every past session forever.
func scopedSSEHandler(auth *CallerAuth, create func() http.Handler) http.Handler {
	type entry struct {
		handler http.Handler
		refs    int
	}
	var mu sync.Mutex
	entries := map[string]*entry{}
	return auth.wrap(func(w http.ResponseWriter, r *http.Request, scope string) {
		mu.Lock()
		current := entries[scope]
		if current == nil {
			current = &entry{handler: create()}
			entries[scope] = current
		}
		current.refs++
		mu.Unlock()
		defer func() {
			mu.Lock()
			defer mu.Unlock()
			current.refs--
			if current.refs == 0 {
				delete(entries, scope)
			}
		}()
		current.handler.ServeHTTP(w, r)
	})
}
