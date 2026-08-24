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
// TR-12 Model Shim — walks registration/configuration structures and dispatches
// to device-specific callbacks. Mirrors tr12_shim.py.
package application_reference_design

import (
	"fmt"

	cddsdkgo "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo"
)

// initialVersion is the sentinel value used in ActualDeviceConfiguration.version
// (and per-channel version) when the device is reporting state before it has
// received any DesiredDeviceConfiguration to echo back. TR-12 marks these
// fields @required @length(max:80); reporting an empty string is a valid-model
// but semantically-vacant report — hosts read the version field to correlate
// with what they asked for, and empty carries no information. "initial" makes
// the "before any desired" state explicit so operators and the host can
// distinguish it from stale or partially-applied reports.
const initialVersion = "initial"

// Tr12Shim bridges TR-12 model structures to device callbacks.
type Tr12Shim struct {
	CB DeviceCallbacks
}

// NewTr12Shim creates a new shim with ARD callbacks (for the ARD binary).
func NewTr12Shim() *Tr12Shim {
	return &Tr12Shim{CB: NewArdCallbacks()}
}

// NewTr12ShimWithCallbacks creates a shim with a custom DeviceCallbacks implementation.
func NewTr12ShimWithCallbacks(cb DeviceCallbacks) *Tr12Shim {
	return &Tr12Shim{CB: cb}
}

// resolvedChannel pairs a channel ID with its resolved template.
type resolvedChannel struct {
	channelID string
	template  cddsdkgo.ChannelTemplate
}

// resolveChannels expands channelAssignments → channelTemplates, returning one
// entry per assigned channel in assignment order. Unresolvable assignments are skipped.
func resolveChannels(registration *cddsdkgo.DeviceRegistration) []resolvedChannel {
	templateByID := make(map[string]cddsdkgo.ChannelTemplate, len(registration.ChannelTemplates))
	for _, tmpl := range registration.ChannelTemplates {
		templateByID[tmpl.Id] = tmpl
	}
	var result []resolvedChannel
	for _, assignment := range registration.ChannelAssignments {
		tmpl, ok := templateByID[assignment.TemplateId]
		if !ok {
			continue
		}
		result = append(result, resolvedChannel{channelID: assignment.ChannelId, template: tmpl})
	}
	return result
}

// ApplyDesiredConfiguration walks a DesiredDeviceConfiguration and pushes all values to the device.
// For selective per-channel application, use applyChannel directly from ApplicationLoop.
func (s *Tr12Shim) ApplyDesiredConfiguration(desired *cddsdkgo.DesiredDeviceConfiguration) bool {
	if desired == nil {
		return false
	}
	for _, kv := range desired.StandardSettings {
		s.CB.UpdateDeviceKeyValue(kv.Id, kv.Value)
	}
	for _, ch := range desired.Channels {
		s.applyChannel(ch)
	}
	return true
}

// applyChannel applies a single channel's desired configuration to the device.
func (s *Tr12Shim) applyChannel(chCfg cddsdkgo.DesiredChannelConfiguration) {
	chID := chCfg.Id

	// ChannelSettings (StandardSettings or Profile via oneOf union)
	if chCfg.ChannelSettings != nil {
		settings := chCfg.ChannelSettings
		if settings.StandardSettings != nil {
			for _, kv := range settings.StandardSettings.StandardSettings {
				s.CB.UpdateChannelSettings(chID, kv.Id, kv.Value)
			}
		} else if settings.Profile != nil {
			s.CB.UpdateChannelProfile(chID, settings.Profile.Profile.Id)
		}
	}

	// Protocol
	if chCfg.Protocol != nil {
		s.CB.UpdateChannelConnection(chID, chCfg.Protocol)
	}

	// State (apply last so settings/protocol are in place first)
	s.CB.UpdateChannelState(chID, chCfg.State)
}

// GetActualConfiguration reads back current values using the registration as a template.
// appliedChannelVersions contains the version last applied per channel by the ARD.
// The device-level version is echoed from desired.
func (s *Tr12Shim) GetActualConfiguration(registration *cddsdkgo.DeviceRegistration, desired *cddsdkgo.DesiredDeviceConfiguration, appliedChannelVersions map[string]string) *cddsdkgo.ActualDeviceConfiguration {
	result := &cddsdkgo.ActualDeviceConfiguration{}

	// Device-level version: echo desired's version when we have one, otherwise
	// mark the report as "initial" (device state prior to any host configuration).
	if desired != nil {
		result.Version = desired.Version
	} else {
		result.Version = initialVersion
	}

	// Device-level standard settings
	var deviceSettings []cddsdkgo.IdAndValue
	for _, setting := range registration.Settings {
		if val, found := s.CB.GetDeviceUpdatedValue(setting.Id); found {
			deviceSettings = append(deviceSettings, cddsdkgo.IdAndValue{
				Id: setting.Id, Value: val,
			})
		}
	}
	if len(deviceSettings) > 0 {
		result.StandardSettings = deviceSettings
	}

	// Build a lookup of desired channel settings keyed by channel ID.
	// This drives whether actual reports a profile or standardSettings — the
	// desired config is the single source of truth for which union branch to use.
	desiredChannelSettings := make(map[string]*cddsdkgo.ChannelSettings)
	if desired != nil {
		for _, ch := range desired.Channels {
			desiredChannelSettings[ch.Id] = ch.ChannelSettings
		}
	}

	// Channels — resolve assignments → templates, echo back the applied version per channel.
	// A channel that has not yet applied any desired configuration reports version=initial
	// so the host can distinguish "never configured" from a stale applied version.
	var channels []cddsdkgo.ActualChannelConfiguration
	for _, rc := range resolveChannels(registration) {
		chCfg := s.buildChannelConfig(rc.channelID, rc.template, desiredChannelSettings[rc.channelID])
		if v := appliedChannelVersions[rc.channelID]; v != "" {
			chCfg.Version = v
		} else {
			chCfg.Version = initialVersion
		}
		channels = append(channels, chCfg)
	}
	result.Channels = channels
	return result
}

func (s *Tr12Shim) buildChannelConfig(channelID string, tmpl cddsdkgo.ChannelTemplate, desiredSettings *cddsdkgo.ChannelSettings) cddsdkgo.ActualChannelConfiguration {
	if cb, ok := s.CB.(interface{ BeginGetActualConfiguration(string) }); ok {
		cb.BeginGetActualConfiguration(channelID)
	}

	chCfg := cddsdkgo.ActualChannelConfiguration{
		Id:    channelID,
		State: s.CB.GetChannelState(channelID),
	}

	// The desired config is the single source of truth for which channelSettings union
	// branch to report in actual. If desired has a profile, ask the device what profile
	// it currently has active. If desired has standardSettings (or is absent), read back
	// individual values from the device.
	if desiredSettings != nil && desiredSettings.Profile != nil {
		// Desired is profile mode — ask the device for the actual profile ID.
		if profileID, found := s.CB.GetChannelProfileValue(channelID); found {
			chCfg.ChannelSettings = &cddsdkgo.ChannelSettings{
				Profile: &cddsdkgo.Profile{
					Profile: cddsdkgo.ChannelProfile{Id: profileID},
				},
			}
		}
		// If GetChannelProfileValue returns false, the device hasn't confirmed the
		// profile yet (e.g. native API not implemented) — omit channelSettings from actual.
	} else if len(tmpl.Settings) > 0 {
		// Desired is standardSettings (or no desired yet) — read back from device.
		var kvList []cddsdkgo.IdAndValue
		for _, setting := range tmpl.Settings {
			if val, found := s.CB.GetChannelUpdatedValue(channelID, setting.Id); found {
				kvList = append(kvList, cddsdkgo.IdAndValue{
					Id: setting.Id, Value: val,
				})
			}
		}
		if len(kvList) > 0 {
			chCfg.ChannelSettings = &cddsdkgo.ChannelSettings{
				StandardSettings: &cddsdkgo.StandardSettings{
					StandardSettings: kvList,
				},
			}
		}
	}

	// Protocol
	proto := s.CB.GetChannelConnection(channelID)
	if proto != nil {
		chCfg.Protocol = proto
	}

	// Thumbnail local path
	if path, ok := s.CB.GetChannelThumbnailLocalPath(channelID); ok && path != "" {
		chCfg.ThumbnailLocalPath = &path
	}

	return chCfg
}

// GetDeviceStatus builds the typed device status payload using registration channels.
func (s *Tr12Shim) GetDeviceStatus(registration *cddsdkgo.DeviceRegistration) *cddsdkgo.DeviceStatus {
	var channelStatuses []cddsdkgo.ChannelStatus
	for _, rc := range resolveChannels(registration) {
		chStatus := cddsdkgo.ChannelStatus{
			Id:     rc.channelID,
			State:  s.CB.GetChannelState(rc.channelID),
			Status: s.CB.GetChannelStatus(rc.channelID),
		}
		if health := s.CB.GetChannelHealth(rc.channelID); health != nil {
			chStatus.Health = health
		}
		channelStatuses = append(channelStatuses, chStatus)
	}
	result := &cddsdkgo.DeviceStatus{
		Status:   s.CB.GetDeviceStatus(),
		Channels: channelStatuses,
	}
	if health := s.CB.GetDeviceHealth(); health != nil {
		result.Health = health
	}
	return result
}

// PrintActualConfig is a debug helper.
func (s *Tr12Shim) PrintActualConfig(registration *cddsdkgo.DeviceRegistration) {
	actual := s.GetActualConfiguration(registration, nil, map[string]string{})
	fmt.Printf("[SHIM TEST] get_actual_configuration: %+v\n", actual)
}
