package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// writeConditionalJSON encodes value, tags it with an ETag (strong over the body, weak when `tagged` differs) and
// answers a matching If-None-Match with an empty 304. It is for list reads that a page
// revalidates on a timer or an event: an unchanged list costs one read and no transfer,
// parse or render. Cache-Control stays no-store so the browser never answers the
// conditional itself; the page owns revalidation and must see the real 304.
// `start` feeds a Server-Timing header covering the read that produced value.
// `tagged` is what the ETag is computed over: the same list with any per-read field (a
// read timestamp) cleared, so two reads of an unchanged list share a tag. Nil means value.
func writeConditionalJSON(w http.ResponseWriter, r *http.Request, name string, start time.Time, value, tagged any) error {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		return err
	}
	tagBytes := body.Bytes()
	if tagged != nil {
		var tagBody bytes.Buffer
		if err := json.NewEncoder(&tagBody).Encode(tagged); err != nil {
			return err
		}
		tagBytes = tagBody.Bytes()
	}
	sum := sha256.Sum256(tagBytes)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	if tagged != nil {
		// The tag covers different bytes than the body (the body carries a read time), so
		// it only promises "same list", not byte equality: that is a weak validator.
		etag = "W/" + etag
	}
	w.Header().Set("Server-Timing", name+";dur="+strconv.FormatFloat(float64(time.Since(start).Microseconds())/1000, 'f', 1, 64))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-store")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
	return nil
}

// etagMatches reports whether an If-None-Match header names etag. Compression
// proxies rewrite a strong tag to a weak one (W/"…"), which still matches here:
// the comparison is for "same list", not for byte-range safety.
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == strings.TrimPrefix(etag, "W/") || candidate == "*" {
			return true
		}
	}
	return false
}
