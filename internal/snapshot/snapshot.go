// Package snapshot attempts an OS-level restore point or snapshot before a
// move. It is best effort: the verified file backup is the real safety net.
package snapshot

import (
	"fmt"

	"github.com/midas-cli/midas/internal/manifest"
)

// Policy values accepted by Create.
const (
	PolicyAuto    = "auto"
	PolicyRequire = "require"
	PolicySkip    = "skip"
)

// Create applies the configured policy and returns the record for the
// manifest. An error is returned only for `require` failures (exit code 8).
func Create(policy string) (manifest.RestorePoint, error) {
	rp := manifest.RestorePoint{}
	switch policy {
	case PolicySkip:
		rp.Reason = "skipped by configuration"
		return rp, nil
	case PolicyAuto, PolicyRequire:
	default:
		return rp, fmt.Errorf("unknown restore point policy %q", policy)
	}
	rp.Attempted = true
	name, seq, err := createSnapshot()
	if err != nil {
		rp.Created = false
		rp.Reason = err.Error()
		if policy == PolicyRequire {
			return rp, fmt.Errorf("a restore point was required but could not be created: %w", err)
		}
		return rp, nil
	}
	rp.Created = true
	rp.Name = name
	rp.Sequence = seq
	return rp, nil
}
