// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// Package version implements the TR-12 protocol compatibility check used by
// the SDK to accept/reject a device-declared registration version.
//
// Rule (matches the semver notes in common.smithy):
//   compatible if receiver.major == payload.major && receiver.minor >= payload.minor
//
// The receiver is the party doing the check (SDK or host) — a receiver is
// considered a strict superset of any payload with the same major and an
// equal-or-lower minor.
package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse parses a "MAJOR.MINOR.PATCH" string. Patch is ignored for compat, but
// must be numeric if present. Returns major and minor.
func Parse(s string) (major, minor int, err error) {
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, fmt.Errorf("invalid version %q: expected MAJOR.MINOR[.PATCH]", s)
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid major in %q: %w", s, err)
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid minor in %q: %w", s, err)
	}
	if len(parts) == 3 {
		if _, err := strconv.Atoi(parts[2]); err != nil {
			return 0, 0, fmt.Errorf("invalid patch in %q: %w", s, err)
		}
	}
	return major, minor, nil
}

// IsCompatible reports whether a payload version is compatible with a receiver
// version. Same-major, and receiver.minor >= payload.minor.
func IsCompatible(receiver, payload string) (bool, error) {
	rMaj, rMin, err := Parse(receiver)
	if err != nil {
		return false, fmt.Errorf("receiver: %w", err)
	}
	pMaj, pMin, err := Parse(payload)
	if err != nil {
		return false, fmt.Errorf("payload: %w", err)
	}
	return rMaj == pMaj && rMin >= pMin, nil
}
