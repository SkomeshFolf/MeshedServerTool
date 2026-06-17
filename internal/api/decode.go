package api

import (
	"encoding/json"
	"net/http"
)

// maxBodyBytes is the upper limit for any JSON request body. 1 MiB is
// more than enough for every endpoint in this API (a single server's
// args map, an INI file with a few sections, a report text) and
// prevents a malicious client from OOMing the process by streaming
// a multi-GB body.
const maxBodyBytes = 1 << 20

// decodeJSON reads at most 1 MiB of r.Body and decodes it into v.
// Returns a 400-class error if the body is too large or malformed.
//
// Use this instead of json.NewDecoder(r.Body).Decode(&v) at every
// handler that takes a JSON body. (audit finding #13)
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	return json.NewDecoder(r.Body).Decode(v)
}
