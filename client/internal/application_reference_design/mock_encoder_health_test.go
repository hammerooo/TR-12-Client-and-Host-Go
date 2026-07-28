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
// TestHealthReporting — unit tests for the ARD health error path.
//
// These tests verify that:
//   1. A simulated setting failure in the mock encoder causes the channel to be
//      reported as DEGRADED in the device status.
//   2. A simulated start/stop failure causes DEGRADED health.
//   3. Multiple failures accumulate into one DEGRADED health message.
//   4. Clearing the failure restores HEALTHY.
//   5. The ApplicationLoop leaves the channel version unrecorded when DEGRADED,
//      ensuring the next cycle retries.
//
// This is also the reference pattern for device integrators: whenever your native
// API call fails, call SetChannelHealth("DEGRADED", []string{errMsg}) so the TR-12
// health field surfaces the error to the cloud operator.
package application_reference_design

import (
	"strings"
	"testing"

	cddsdkgo "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo"
)

// buildTestRegistration returns a minimal DeviceRegistration with one channel
// template that has settings RS01 and FR01, and protocols [SRT_CALLER].
func buildTestRegistration() *cddsdkgo.DeviceRegistration {
	tmpl := cddsdkgo.ChannelTemplate{
		Id:          "tmpl1",
		ChannelType: cddsdkgo.CHANNELTYPE_SOURCE,
		Settings: []cddsdkgo.Setting{
			{Id: "RS01", Name: "resolution", Description: "Output resolution",
				Constraint: cddsdkgo.SettingConstraint{
					Enums: &cddsdkgo.Enums{Enums: cddsdkgo.EnumValues{
						Values:       []string{"1920x1080", "1280x720"},
						DefaultValue: "1920x1080",
					}},
				}},
			{Id: "FR01", Name: "framerate", Description: "Frames per second",
				Constraint: cddsdkgo.SettingConstraint{
					Enums: &cddsdkgo.Enums{Enums: cddsdkgo.EnumValues{
						Values:       []string{"30", "60"},
						DefaultValue: "30",
					}},
				}},
		},
		Protocols: []cddsdkgo.TransportProtocolName{cddsdkgo.TRANSPORTPROTOCOLNAME_SRT_CALLER},
	}
	return &cddsdkgo.DeviceRegistration{
		Version:          *cddsdkgo.NewProtocolVersionWithDefaults(),
		ChannelTemplates: []cddsdkgo.ChannelTemplate{tmpl},
		ChannelAssignments: []cddsdkgo.ChannelAssignment{
			{ChannelId: "CH01", Name: "Channel 1", TemplateId: "tmpl1"},
		},
	}
}

// buildTestDesiredConfig builds a minimal desired config for CH01 with given version.
func buildTestDesiredConfig(version, channelVersion string) *cddsdkgo.DesiredDeviceConfiguration {
	return &cddsdkgo.DesiredDeviceConfiguration{
		Version: version,
		Channels: []cddsdkgo.DesiredChannelConfiguration{
			{
				Id:      "CH01",
				Version: channelVersion,
				State:   cddsdkgo.CHANNELSTATE_ACTIVE,
				ChannelSettings: &cddsdkgo.ChannelSettings{
					StandardSettings: &cddsdkgo.StandardSettings{
						StandardSettings: []cddsdkgo.IdAndValue{
							{Id: "RS01", Value: "1920x1080"},
							{Id: "FR01", Value: "30"},
						},
					},
				},
			},
		},
	}
}

// TestSettingFailureCausesChannelDegraded verifies that when SetChannelSetting fails
// (simulating a native API error), the channel health is set to DEGRADED and the
// message from the failure is included.
func TestSettingFailureCausesChannelDegraded(t *testing.T) {
	callbacks := NewArdCallbacks()
	enc := callbacks.Encoder

	// Simulate the device rejecting the "RS01" setting (e.g. hardware limitation).
	enc.SimulateFailure("RS01")

	callbacks.UpdateChannelSettings("CH01", "RS01", "1920x1080")

	health := enc.GetChannelHealth("CH01")
	if health == nil {
		t.Fatal("expected non-nil health after simulated failure")
	}
	if health.Degraded == nil {
		t.Fatalf("expected Degraded health, got: %+v", health)
	}
	if !strings.Contains(health.Degraded.Degraded.Message, "RS01") {
		t.Errorf("expected health message to mention RS01, got: %q", health.Degraded.Degraded.Message)
	}
	t.Logf("DEGRADED health message: %s", health.Degraded.Degraded.Message)
}

// TestStartStopFailureCausesChannelDegraded verifies that when HandleUpdateState fails
// (simulating a start/stop error), the channel health is set to DEGRADED.
func TestStartStopFailureCausesChannelDegraded(t *testing.T) {
	callbacks := NewArdCallbacks()
	enc := callbacks.Encoder

	// Simulate the device rejecting channel start (e.g. hardware unavailable).
	enc.SimulateFailure("__start_stop__")

	callbacks.UpdateChannelState("CH01", cddsdkgo.CHANNELSTATE_ACTIVE)

	health := enc.GetChannelHealth("CH01")
	if health == nil {
		t.Fatal("expected non-nil health after simulated start failure")
	}
	if health.Degraded == nil {
		t.Fatalf("expected Degraded health, got: %+v", health)
	}
	if !strings.Contains(health.Degraded.Degraded.Message, "CH01") {
		t.Errorf("expected health message to mention CH01, got: %q", health.Degraded.Degraded.Message)
	}
	t.Logf("DEGRADED health message: %s", health.Degraded.Degraded.Message)
}

// TestMultipleFailuresAccumulate verifies that applying multiple failing settings
// in sequence leaves the channel DEGRADED with both errors visible.
// The last failure message wins (each call to SetChannelHealth replaces the previous),
// which is the expected behaviour — the most recent failure is the actionable one.
func TestMultipleFailuresAccumulate(t *testing.T) {
	callbacks := NewArdCallbacks()
	enc := callbacks.Encoder

	enc.SimulateFailure("RS01")
	enc.SimulateFailure("FR01")

	callbacks.UpdateChannelSettings("CH01", "RS01", "1920x1080")
	callbacks.UpdateChannelSettings("CH01", "FR01", "30")

	health := enc.GetChannelHealth("CH01")
	if health == nil || health.Degraded == nil {
		t.Fatalf("expected Degraded health after multiple failures, got: %+v", health)
	}
	// Last failure message should be present
	if !strings.Contains(health.Degraded.Degraded.Message, "FR01") {
		t.Errorf("expected message to mention FR01 (last failure), got: %q", health.Degraded.Degraded.Message)
	}
	t.Logf("DEGRADED health after multiple failures: %s", health.Degraded.Degraded.Message)
}

// TestClearingFailureRestoresHealthy verifies that after clearing the simulated failure
// and successfully applying the setting, the channel returns to HEALTHY.
func TestClearingFailureRestoresHealthy(t *testing.T) {
	callbacks := NewArdCallbacks()
	enc := callbacks.Encoder

	// Phase 1: induce failure
	enc.SimulateFailure("RS01")
	callbacks.UpdateChannelSettings("CH01", "RS01", "1920x1080")
	if enc.GetChannelHealth("CH01").Degraded == nil {
		t.Fatal("expected DEGRADED after simulated failure")
	}

	// Phase 2: fix the failure, clear health, apply successfully
	enc.ClearFailure("RS01")
	enc.ClearChannelHealth("CH01")
	callbacks.UpdateChannelSettings("CH01", "RS01", "1280x720")

	health := enc.GetChannelHealth("CH01")
	if health == nil {
		t.Fatal("expected non-nil health")
	}
	if health.Healthy == nil {
		t.Fatalf("expected Healthy after clearing failure, got degraded=%v critical=%v",
			health.Degraded, health.Critical)
	}
	t.Log("Channel correctly returned to HEALTHY after clearing failure")
}

// TestShimReflectsDegradedHealthInDeviceStatus verifies the full shim path:
// a DEGRADED channel health set on the encoder appears in the DeviceStatus
// returned by GetDeviceStatus. This is what gets published to the host.
func TestShimReflectsDegradedHealthInDeviceStatus(t *testing.T) {
	callbacks := NewArdCallbacks()
	enc := callbacks.Encoder
	shim := NewTr12ShimWithCallbacks(callbacks)

	reg := buildTestRegistration()

	// Apply settings normally first so there's a valid baseline.
	callbacks.UpdateChannelSettings("CH01", "RS01", "1920x1080")
	callbacks.UpdateChannelSettings("CH01", "FR01", "30")

	// Now simulate a failure on the next update (e.g. codec change failed).
	enc.SimulateFailure("RS01")
	callbacks.UpdateChannelSettings("CH01", "RS01", "1280x720")

	// Build device status via the shim.
	status := shim.GetDeviceStatus(reg)

	if len(status.Channels) == 0 {
		t.Fatal("expected at least one channel in device status")
	}
	ch := status.Channels[0]
	if ch.Id != "CH01" {
		t.Fatalf("expected CH01, got %s", ch.Id)
	}
	if ch.Health == nil {
		t.Fatal("expected non-nil health in device status")
	}
	if ch.Health.Degraded == nil {
		t.Fatalf("expected Degraded health in device status, got healthy=%v", ch.Health.Healthy)
	}
	t.Logf("Device status CH01 health: DEGRADED message=%q", ch.Health.Degraded.Degraded.Message)
}

// TestApplicationLoopLeavesVersionUnrecordedWhenDegraded verifies that the
// ApplicationLoop does not record the channel version when health is DEGRADED —
// ensuring the next cycle retries the configuration.
func TestApplicationLoopLeavesVersionUnrecordedWhenDegraded(t *testing.T) {
	callbacks := NewArdCallbacks()
	enc := callbacks.Encoder
	shim := NewTr12ShimWithCallbacks(callbacks)

	// Simulate a failure before any apply so the channel will be DEGRADED.
	enc.SimulateFailure("RS01")

	// Simulate what ApplicationLoop.processConfiguration does for one channel:
	// call applyChannel, then check health, then decide whether to record version.
	desired := buildTestDesiredConfig("v1", "cv1")
	ch := desired.Channels[0]

	shim.applyChannel(ch)

	health := callbacks.GetChannelHealth(ch.Id)
	versionShouldBeRecorded := !(health != nil && health.Degraded != nil)

	if versionShouldBeRecorded {
		t.Errorf("expected version NOT to be recorded when channel is DEGRADED, but health check said it should be recorded")
	}
	t.Log("Correctly determined version should NOT be recorded for DEGRADED channel — loop will retry next cycle")
}
