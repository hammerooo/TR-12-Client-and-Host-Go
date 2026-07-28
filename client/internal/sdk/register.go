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
// Register — re-publish DeviceRegistration while connected.
// Only changes to channelTemplates[].profiles are permitted; all other
// fields must be identical to the registration supplied at connect time.
package sdk

import (
	"encoding/json"
	"fmt"

	"github.com/vsf-tv/TR-12-Client-and-Host-Go/client/internal/models"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/client/internal/utils"
	cddsdkgo "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo"
)

// Register validates a new registration payload and, if only profiles have changed,
// updates the stored registration and re-publishes it to the host via MQTT.
//
// Rules:
//   - Must be CONNECTED; returns an error response otherwise.
//   - channelTemplates count, IDs, channelType, settings, and protocols must be
//     identical to the current registration. Only channelTemplates[n].profiles may differ.
//   - channelAssignments and device-level settings must be identical.
//
// The incoming registration is already a validated typed struct — deserialization
// happened at the HTTP boundary in server.go before this method is called.
func (s *CddSdk) Register(registration *cddsdkgo.DeviceRegistration) cddsdkgo.ReportStatusResponseContent {
	s.apiLock.Lock()
	defer s.apiLock.Unlock()
	s.logger.Info("Register")

	if !s.is(models.StateConnected) {
		return cddsdkgo.ReportStatusResponseContent{
			Success: false,
			State:   s.state,
			Message: "Register requires an active CONNECTED state — call connect() first",
		}
	}

	if registration == nil {
		return cddsdkgo.ReportStatusResponseContent{
			Success: false, State: s.state,
			Message: "registration is required",
		}
	}
	// Same version compatibility rule as initializeHost — re-check on every /register
	// call, since the incoming registration is fresh from the caller.
	if err := s.checkRegistrationVersion(registration); err != nil {
		return cddsdkgo.ReportStatusResponseContent{
			Success: false, State: s.state,
			Message: err.Error(),
			Error:   utils.ExceptionToErrorDetails(err),
		}
	}
	if len(registration.ChannelTemplates) == 0 {
		return cddsdkgo.ReportStatusResponseContent{
			Success: false, State: s.state,
			Message: "channelTemplates is required and must not be empty",
		}
	}
	if len(registration.ChannelAssignments) == 0 {
		return cddsdkgo.ReportStatusResponseContent{
			Success: false, State: s.state,
			Message: "channelAssignments is required and must not be empty",
		}
	}

	// Compare typed structs instance-to-instance — no JSON round-trip needed.
	if err := validateProfilesOnlyChange(s.registration, registration); err != nil {
		return cddsdkgo.ReportStatusResponseContent{
			Success: false, State: s.state,
			Message: err.Error(),
			Error:   utils.ExceptionToErrorDetails(err),
		}
	}

	// Validation passed — update stored typed registration and re-publish.
	s.registration = registration
	s.reportRegistration()

	return cddsdkgo.ReportStatusResponseContent{
		Success: true, State: s.state,
		Message: "Registration updated and re-published",
	}
}

// validateProfilesOnlyChange returns an error if anything other than
// channelTemplates[n].profiles differs between cur and next.
// Comparison is done on the typed struct fields directly, with JSON used
// only for slice/struct fields where Go doesn't provide deep equality.
func validateProfilesOnlyChange(cur, next *cddsdkgo.DeviceRegistration) error {
	if cur == nil {
		return fmt.Errorf("no current registration to compare against")
	}

	// channelAssignments must be identical.
	if err := compareJSON(cur.ChannelAssignments, next.ChannelAssignments, "channelAssignments"); err != nil {
		return err
	}

	// device-level settings must be identical.
	if err := compareJSON(cur.Settings, next.Settings, "settings"); err != nil {
		return err
	}

	// Template count must match.
	if len(cur.ChannelTemplates) != len(next.ChannelTemplates) {
		return fmt.Errorf("channelTemplates count changed (%d → %d): only profiles may be updated via register()",
			len(cur.ChannelTemplates), len(next.ChannelTemplates))
	}

	// For each template, compare fields individually — profiles are the only allowed change.
	for i, ct := range cur.ChannelTemplates {
		nt := next.ChannelTemplates[i]

		if ct.Id != nt.Id {
			return fmt.Errorf("channelTemplates[%d].id changed (%q → %q): only profiles may be updated via register()", i, ct.Id, nt.Id)
		}
		if ct.ChannelType != nt.ChannelType {
			return fmt.Errorf("channelTemplates[%d].channelType changed: only profiles may be updated via register()", i)
		}
		if err := compareJSON(ct.Settings, nt.Settings, fmt.Sprintf("channelTemplates[%d].settings", i)); err != nil {
			return err
		}
		if err := compareJSON(ct.Protocols, nt.Protocols, fmt.Sprintf("channelTemplates[%d].protocols", i)); err != nil {
			return err
		}
		// profiles are allowed to differ — no comparison.
	}

	return nil
}

// compareJSON marshals both values to canonical JSON and compares them.
// Used for slice/struct fields where Go doesn't provide structural equality.
func compareJSON(a, b interface{}, field string) error {
	aj, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("cannot marshal current %s: %w", field, err)
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("cannot marshal new %s: %w", field, err)
	}
	if string(aj) != string(bj) {
		return fmt.Errorf("%s changed: only channelTemplates[n].profiles may be updated via register()", field)
	}
	return nil
}
