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
// validateRegistration is the single choke point for TR-12 registration
// payload validation on the SDK side. Called from both Connect
// (via initializeHost) and Register.
//
// Coverage in one pass:
//   1. Nil check.
//   2. validator.Validate — enforces every @pattern and @length constraint
//      the post-processed struct tags carry. See
//      models/cdd_sdk/postprocess-validate-tags.py.
//   3. Protocol version compatibility (same-major, sdk.minor >= payload.minor).
//   4. channelAssignments[n].templateId cross-reference — must map to an
//      existing channelTemplates[m].id.
//
// Note: enum membership (ChannelType, TransportProtocolName) is already
// enforced by the generated `UnmarshalJSON` on each enum type, so it does
// not need a separate walker here — a bad enum value fails at JSON bind time.
//
// All violations are collected. The returned error string is prefixed with
// "invalid registration:" and truncated to validationMsgMaxLen + "..." so
// callers can put it verbatim into the top-level ConnectResponse.Message
// without concern.
package sdk

import (
	"fmt"
	"strings"

	"github.com/vsf-tv/TR-12-Client-and-Host-Go/client/internal/version"
	cddsdkgo "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo"
	"gopkg.in/validator.v2"
)

const validationMsgMaxLen = 125

func (s *CddSdk) validateRegistration(reg *cddsdkgo.DeviceRegistration) error {
	if reg == nil {
		return fmt.Errorf("invalid registration: nil payload")
	}

	var violations []string
	if err := validator.Validate(reg); err != nil {
		violations = append(violations, err.Error())
	}
	if v := checkVersionCompat(reg); v != "" {
		violations = append(violations, v)
	}
	violations = append(violations, checkTemplateIDCrossRef(reg)...)

	if len(violations) == 0 {
		return nil
	}
	msg := "invalid registration: " + strings.Join(violations, "; ")
	return fmt.Errorf("%s", truncate(msg, validationMsgMaxLen))
}

// truncate caps a string at max characters, appending "..." if it was cut.
// Result length is capped at max+3 (128 when max is 125).
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// checkVersionCompat returns "" on OK or a short violation string on mismatch.
// Uses the SDK's own compiled-in protocol version as the compat baseline.
func checkVersionCompat(reg *cddsdkgo.DeviceRegistration) string {
	sdkVersion := cddsdkgo.NewProtocolVersionWithDefaults().GetVersion()
	declared := reg.Version.Version
	ok, err := version.IsCompatible(sdkVersion, declared)
	if err != nil {
		return fmt.Sprintf("version %q: %v", declared, err)
	}
	if !ok {
		return fmt.Sprintf("incompatible version %s (SDK: %s)", declared, sdkVersion)
	}
	return ""
}

// checkTemplateIDCrossRef flags any channelAssignment.templateId that does not
// point at a real channelTemplate.id in the same registration.
func checkTemplateIDCrossRef(reg *cddsdkgo.DeviceRegistration) []string {
	ids := make(map[string]struct{}, len(reg.ChannelTemplates))
	for _, t := range reg.ChannelTemplates {
		ids[t.Id] = struct{}{}
	}
	var out []string
	for i, a := range reg.ChannelAssignments {
		if _, ok := ids[a.TemplateId]; !ok {
			out = append(out, fmt.Sprintf("channelAssignments[%d].templateId %q references unknown template", i, a.TemplateId))
		}
	}
	return out
}
