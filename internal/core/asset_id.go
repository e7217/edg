package core

import (
	"fmt"
	"regexp"
)

// assetIDMaxLen bounds an operator-supplied asset id. It is generous for a
// plant naming convention and short enough to stay a workable filename and a
// readable VictoriaMetrics tag value.
const assetIDMaxLen = 128

// assetIDPattern constrains an operator-supplied asset id.
//
// The set is the intersection of three places the id has to survive:
//
//   - a VictoriaMetrics tag value, where the sink escapes commas, spaces and
//     equals signs but the result is what an operator has to type into PromQL;
//   - a filename, because -export-points writes one <asset-id>.yaml per list;
//   - a URL path segment in /api/v1/assets/{id}/points.
//
// A dot is allowed because the reference adapters derive their asset id from a
// host address -- modbus-127.0.0.1-1 -- and refusing that would make the
// declared id unable to match what an adapter actually publishes, which is the
// whole point of letting an operator choose it. The consequence is that an
// asset id is not necessarily a single NATS subject token, unlike adapter_id.
// Nothing puts an asset id in a subject today; anything that wants to must
// either constrain it further or use a different key.
var assetIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:\-]*$`)

// ValidateAssetID checks an operator-supplied asset id.
//
// Server-generated UUIDs satisfy it, so an existing deployment keeps working and
// every id in the system remains valid under one rule.
func ValidateAssetID(id string) error {
	if id == "" {
		return fmt.Errorf("asset id is required")
	}
	if len(id) > assetIDMaxLen {
		return fmt.Errorf("asset id is longer than %d characters", assetIDMaxLen)
	}
	// Refused rather than sanitised: a silently rewritten id would not match
	// what the adapter publishes, and the mismatch is invisible until no
	// telemetry ever joins to the asset.
	if !assetIDPattern.MatchString(id) {
		return fmt.Errorf(
			"asset id %q must start with a letter or digit and contain only letters, digits, and . _ : -", id)
	}
	if id == "." || id == ".." {
		return fmt.Errorf("asset id %q is reserved", id)
	}
	return nil
}
