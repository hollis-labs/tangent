package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/access"
)

// answer is a refusal's code ("scope_required" from the auth guard, "forbidden" from the browser
// routes' user check) with the status; empty for any other response.
type answer struct {
	status int
	code   string
}

func (a answer) refused() bool {
	return a.status == http.StatusUnauthorized || a.code == "scope_required" || a.code == "forbidden"
}

// routeAs sends one request to a route, as the owner's session or with a key, and reads the answer.
func (h *host) routeAs(key string, method, path string) answer {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.base+path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	client := h.owner
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
		client = &http.Client{Timeout: 20 * time.Second}
	} else {
		req.Header.Set("X-Requested-With", "test")
		req.Header.Set("Origin", h.base)
	}
	res, err := client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		return answer{status: res.StatusCode}
	}
	raw, _ := io.ReadAll(res.Body)
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &body)
	return answer{status: res.StatusCode, code: body.Code}
}

// Every route is registered with the one scope it needs, and that scope has to be one the person
// the browser is signed in as holds, or the route is dead to the only caller it is for (the
// channel send that shipped gated on a capability no participant held). The route table is read
// back, not a list kept here.
func TestEveryRoutesScopeIsOneTheOwnerSessionHolds(t *testing.T) {
	h := newHost(t)
	routes := h.tg.table.Load().Routes()
	if len(routes) < 40 {
		t.Fatalf("the table has %d routes; this check would pass over too little to mean anything", len(routes))
	}
	for _, route := range routes {
		if got := h.routeAs("", route.Method(), concrete(route.Path())); got.refused() {
			t.Errorf("%s (scope %s) refused the owner's session: %+v", route.Pattern, route.Scope, got)
		}
	}
}

// A key holding only a route's scope is admitted by the scope guard, and one holding some other
// scope is not: the guard enforces the table's scope and nothing broader. What a key that gets
// past the guard may then do is the route's own business: a route that decides, and the browser's
// own routes, serve a user session and refuse the key with "forbidden"; a route made for a service
// credential (an agent enqueuing or acknowledging a turn) answers it.
func TestAKeyHoldingOnlyARoutesScopeIsAdmittedByTheScopeGuard(t *testing.T) {
	h := newHost(t)
	keys := map[string]string{}
	keyFor := func(scope string) string {
		if keys[scope] == "" {
			keys[scope] = h.key("only-"+strings.TrimPrefix(scope, "tangent."), scope)
		}
		return keys[scope]
	}
	var served int
	for _, route := range h.tg.table.Load().Routes() {
		path := concrete(route.Path())
		got := h.routeAs(keyFor(route.Scope), route.Method(), path)
		if got.status == http.StatusUnauthorized || got.code == "scope_required" {
			t.Errorf("%s: a key holding exactly %s was refused by the scope guard: %+v", route.Pattern, route.Scope, got)
		}
		if route.UserOnly && got.code != "forbidden" {
			t.Errorf("%s decides, and a key holding %s got %+v, want forbidden", route.Pattern, route.Scope, got)
		}
		if !got.refused() {
			served++
		}
		other := access.View
		if route.Scope == access.View {
			other = access.Draft
		}
		if wrong := h.routeAs(keyFor(other), route.Method(), path); wrong.code != "scope_required" {
			t.Errorf("%s: a key holding only %s got %+v, want scope_required", route.Pattern, other, wrong)
		}
	}
	if served == 0 {
		t.Error("no route admitted a key: the control that the guard lets a right key through is missing")
	}
}
