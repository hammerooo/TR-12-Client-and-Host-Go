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
// TestRegistrationValidation drives PUT /connect against the SDK with a
// series of intentionally invalid DeviceRegistration payloads. Each subtest
// asserts the SDK rejects with success=false and a message that identifies
// the exact violation, without ever reaching pairing.
//
// The valid-baseline path is exercised by TestFullLifecycle in
// lifecycle_test.go; this file only covers failure cases so we can share a
// single SDK across subtests (initializeHost is re-run each time because
// hostID is only marked accepted after a successful init).

//go:build integration

package integration_test

import (
	"strings"
	"testing"
)

func TestRegistrationValidation(t *testing.T) {
	env := newTestEnv(t)
	env.startHost()
	env.startSDK("integ-validation-001")

	// mutate returns a fresh copy of the baseline registration with the given
	// function applied to it. Each subtest gets a clean map so mutations don't
	// bleed across cases.
	mutate := func(fn func(m map[string]interface{})) map[string]interface{} {
		reg := loadRegistration(t)
		fn(reg)
		return reg
	}

	cases := []struct {
		name    string
		reg     map[string]interface{}
		wantMsg string // substring expected somewhere in Message
	}{
		{
			name: "channelId contains dash",
			reg: mutate(func(m map[string]interface{}) {
				assigns := m["channelAssignments"].([]interface{})
				assigns[0].(map[string]interface{})["channelId"] = "CH-01"
			}),
			wantMsg: "regular expression",
		},
		{
			name: "channelId over max length",
			reg: mutate(func(m map[string]interface{}) {
				assigns := m["channelAssignments"].([]interface{})
				assigns[0].(map[string]interface{})["channelId"] = "ABCDEFGHIJKLMNOPQR" // 18 chars, max is 12
			}),
			wantMsg: "greater than max",
		},
		{
			name: "templateId over max length",
			reg: mutate(func(m map[string]interface{}) {
				tmpls := m["channelTemplates"].([]interface{})
				tmpls[0].(map[string]interface{})["id"] = strings.Repeat("a", 50)
				assigns := m["channelAssignments"].([]interface{})
				assigns[0].(map[string]interface{})["templateId"] = strings.Repeat("a", 50)
			}),
			wantMsg: "greater than max",
		},
		{
			name: "too many channelTemplates",
			reg: mutate(func(m map[string]interface{}) {
				tmpls := m["channelTemplates"].([]interface{})
				extra := map[string]interface{}{"id": "t2", "channelType": "SOURCE"}
				for i := 0; i < 5; i++ {
					tmpls = append(tmpls, extra)
				}
				m["channelTemplates"] = tmpls
			}),
			wantMsg: "greater than max",
		},
		{
			name: "version pattern violated",
			reg: mutate(func(m map[string]interface{}) {
				m["version"] = map[string]interface{}{"version": "eleven"}
			}),
			wantMsg: "regular expression",
		},
		{
			name: "templateId references unknown template",
			reg: mutate(func(m map[string]interface{}) {
				assigns := m["channelAssignments"].([]interface{})
				assigns[0].(map[string]interface{})["templateId"] = "nomatch"
			}),
			wantMsg: "references unknown template",
		},
		{
			name: "multiple violations at once",
			reg: mutate(func(m map[string]interface{}) {
				// dash in channelId + version bad + too many templates
				assigns := m["channelAssignments"].([]interface{})
				assigns[0].(map[string]interface{})["channelId"] = "CH-01"
				m["version"] = map[string]interface{}{"version": "eleven"}
				tmpls := m["channelTemplates"].([]interface{})
				extra := map[string]interface{}{"id": "t2", "channelType": "SOURCE"}
				for i := 0; i < 5; i++ {
					tmpls = append(tmpls, extra)
				}
				m["channelTemplates"] = tmpls
			}),
			// A collected multi-violation error should still be prefixed with
			// "invalid registration:" — enough to prove the validator ran and
			// aggregated.
			wantMsg: "invalid registration",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.sdkConnect("tr12-host", tc.reg)
			if resp.Success {
				t.Fatalf("expected success=false; got response %+v", resp)
			}
			if !strings.Contains(resp.Message, "invalid registration") {
				t.Errorf("Message %q did not carry the 'invalid registration' prefix", resp.Message)
			}
			if !strings.Contains(resp.Message, tc.wantMsg) {
				t.Errorf("Message %q did not contain expected substring %q", resp.Message, tc.wantMsg)
			}
			// Enforce the truncation cap: prefix + up to 125 + "..." = 128 max plus
			// the outer "Error in connect() " wrapper Connect() adds (18 chars).
			if len(resp.Message) > 128+len("Error in connect() ") {
				t.Errorf("Message length %d exceeds expected cap (%d): %q",
					len(resp.Message), 128+len("Error in connect() "), resp.Message)
			}
		})
	}
}
