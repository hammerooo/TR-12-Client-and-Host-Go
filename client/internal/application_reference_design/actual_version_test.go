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

package application_reference_design

import (
	"testing"

	cddsdkgo "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo"
)

// TestGetActualConfigurationInitialVersion pins down the rule that the very
// first actual configuration report (sent on connect, before any desired has
// been received) carries the "initial" sentinel — not an empty string. The
// host validates version as @required @length(max:80); reporting "" is a
// semantic gap that leaves the host unable to distinguish "never configured"
// from a stale or partially-applied state.
func TestGetActualConfigurationInitialVersion(t *testing.T) {
	shim := NewTr12Shim()
	reg := buildTestRegistration()

	// desired=nil, appliedChannelVersions empty — the initial report before
	// the shim has received anything from the host.
	actual := shim.GetActualConfiguration(reg, nil, map[string]string{})

	if actual.Version != "initial" {
		t.Errorf("device-level version on initial report: got %q, want %q", actual.Version, "initial")
	}
	if len(actual.Channels) == 0 {
		t.Fatal("expected at least one channel in the initial actual report")
	}
	for _, ch := range actual.Channels {
		if ch.Version != "initial" {
			t.Errorf("channel %s version on initial report: got %q, want %q", ch.Id, ch.Version, "initial")
		}
	}
}

// TestGetActualConfigurationEchoesDesiredVersion locks in the rule that once
// a desired configuration is present, the actual report echoes the desired's
// version at the device level, and applied per-channel versions come from the
// applied-versions map (which the ApplicationLoop populates from the desired
// after each channel worker completes).
func TestGetActualConfigurationEchoesDesiredVersion(t *testing.T) {
	shim := NewTr12Shim()
	reg := buildTestRegistration()

	desired := &cddsdkgo.DesiredDeviceConfiguration{
		Version: "device-v42",
		Channels: []cddsdkgo.DesiredChannelConfiguration{
			{Id: "CH01", Version: "CH01-v7", State: cddsdkgo.CHANNELSTATE_ACTIVE},
		},
	}
	applied := map[string]string{"CH01": "CH01-v7"}

	actual := shim.GetActualConfiguration(reg, desired, applied)

	if actual.Version != "device-v42" {
		t.Errorf("device-level version: got %q, want to echo desired %q", actual.Version, "device-v42")
	}
	if len(actual.Channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(actual.Channels))
	}
	if actual.Channels[0].Version != "CH01-v7" {
		t.Errorf("channel version: got %q, want applied %q", actual.Channels[0].Version, "CH01-v7")
	}
}

// TestGetActualConfigurationChannelNotYetApplied covers the mixed case: the
// device-level desired has arrived (device version echoed) but a specific
// channel's worker has not completed its apply yet — so that channel reports
// "initial" until it has a real applied version to echo.
func TestGetActualConfigurationChannelNotYetApplied(t *testing.T) {
	shim := NewTr12Shim()
	reg := buildTestRegistration()

	desired := &cddsdkgo.DesiredDeviceConfiguration{
		Version: "device-v1",
		Channels: []cddsdkgo.DesiredChannelConfiguration{
			{Id: "CH01", Version: "CH01-v1", State: cddsdkgo.CHANNELSTATE_ACTIVE},
		},
	}
	// Empty applied map — the channel worker has not yet reported completion.
	applied := map[string]string{}

	actual := shim.GetActualConfiguration(reg, desired, applied)

	if actual.Version != "device-v1" {
		t.Errorf("device-level version: got %q, want %q", actual.Version, "device-v1")
	}
	if actual.Channels[0].Version != "initial" {
		t.Errorf("un-applied channel version: got %q, want %q", actual.Channels[0].Version, "initial")
	}
}
