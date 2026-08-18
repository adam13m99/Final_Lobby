//go:build windows

package agent

import (
	"encoding/json"
	"io"
)

// decodeJSON reads a bounded JSON document. The bound matters: the
// coordinator is trusted, but a misconfigured proxy in front of it is not,
// and the service must not be talked into allocating without limit.
func decodeJSON(r io.Reader, dst any) error {
	return json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(dst)
}
