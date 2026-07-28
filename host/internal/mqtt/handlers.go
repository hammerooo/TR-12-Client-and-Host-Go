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
package mqtt

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/vsf-tv/TR-12-Client-and-Host-Go/host/internal/broker"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/host/internal/db"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/host/internal/service"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/host/internal/version"
	tr12models "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/TR-12-Models/generated/tr12go"
)

// Handlers manages internal MQTT subscriptions.
type Handlers struct {
	store     *db.Store
	broker    *broker.Broker
	deviceSvc *service.DeviceService
}

// NewHandlers creates MQTT message handlers.
func NewHandlers(store *db.Store, b *broker.Broker, deviceSvc *service.DeviceService) *Handlers {
	return &Handlers{store: store, broker: b, deviceSvc: deviceSvc}
}

// Subscribe registers all wildcard subscriptions.
func (h *Handlers) Subscribe() {
	h.broker.Subscribe("cdd/+/registration/report", h.handleRegistration)
	h.broker.Subscribe("cdd/+/status/report", h.handleStatus)
	h.broker.Subscribe("cdd/+/config/actual/report", h.handleActualConfig)
	h.broker.Subscribe("cdd/+/deprovision/ack", h.handleDeprovision)
}

func (h *Handlers) handleRegistration(topic string, payload []byte) {
	deviceID := extractDeviceID(topic)
	if deviceID == "" {
		return
	}
	// Unwrap the envelope: {"deviceRegistration": {...}}
	var envelope struct {
		DeviceRegistration json.RawMessage `json:"deviceRegistration"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || len(envelope.DeviceRegistration) == 0 {
		log.Printf("[mqtt] invalid registration envelope from %s: %v", deviceID, err)
		return
	}
	// Defense in depth: the SDK's /connect handler already rejected incompatible
	// versions before pairing, but a custom or older SDK could bypass that. If
	// the device-declared version is not compatible with the host's model, drop
	// the registration so the device does not appear operational.
	if !checkRegistrationVersion(deviceID, envelope.DeviceRegistration) {
		return
	}
	if err := h.store.UpdateDeviceRegistration(deviceID, envelope.DeviceRegistration); err != nil {
		log.Printf("[mqtt] error storing registration for %s: %v", deviceID, err)
	} else {
		log.Printf("[mqtt] registration updated for %s", deviceID)
	}
}

// checkRegistrationVersion parses the version field from a raw registration
// payload and compares it against the host's own model version. Returns false
// (and logs a warning) if the payload is missing a version or is incompatible.
func checkRegistrationVersion(deviceID string, reg json.RawMessage) bool {
	hostVersion := tr12models.NewProtocolVersionWithDefaults().GetVersion()
	var probe struct {
		Version struct {
			Version string `json:"version"`
		} `json:"version"`
	}
	if err := json.Unmarshal(reg, &probe); err != nil || probe.Version.Version == "" {
		log.Printf("[mqtt] WARN registration from %s is missing the required version field — rejecting (host: %s)", deviceID, hostVersion)
		return false
	}
	declared := probe.Version.Version
	ok, err := version.IsCompatible(hostVersion, declared)
	if err != nil {
		log.Printf("[mqtt] WARN registration from %s has invalid version %q — rejecting (host: %s): %v", deviceID, declared, hostVersion, err)
		return false
	}
	if !ok {
		log.Printf("[mqtt] WARN Incompatible registration version %s from %s (host: %s). Ensure the device is compatible with TR12 major/minor version", declared, deviceID, hostVersion)
		return false
	}
	return true
}

func (h *Handlers) handleStatus(topic string, payload []byte) {
	deviceID := extractDeviceID(topic)
	if deviceID == "" {
		return
	}
	// Unwrap the envelope: {"deviceStatus": {...}}
	var envelope struct {
		DeviceStatus json.RawMessage `json:"deviceStatus"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || len(envelope.DeviceStatus) == 0 {
		log.Printf("[mqtt] invalid status envelope from %s: %v", deviceID, err)
		return
	}
	if err := h.store.UpdateDeviceStatus(deviceID, envelope.DeviceStatus); err != nil {
		log.Printf("[mqtt] error storing status for %s: %v", deviceID, err)
	}
}

func (h *Handlers) handleActualConfig(topic string, payload []byte) {
	deviceID := extractDeviceID(topic)
	if deviceID == "" {
		return
	}
	// Unwrap the envelope: {"actualDeviceConfiguration": {...}}
	var envelope struct {
		ActualDeviceConfiguration json.RawMessage `json:"actualDeviceConfiguration"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || len(envelope.ActualDeviceConfiguration) == 0 {
		log.Printf("[mqtt] invalid actual config envelope from %s: %v", deviceID, err)
		return
	}
	if err := h.store.UpdateDeviceActualConfig(deviceID, envelope.ActualDeviceConfiguration); err != nil {
		log.Printf("[mqtt] error storing actual config for %s: %v", deviceID, err)
	}
}

// MQTT deprovision ack — device has acknowledged. No IoT resource cleanup
// is performed here; resources persist indefinitely until a future cleanup
// mechanism is implemented. Simply log the acknowledgement.
func (h *Handlers) handleDeprovision(topic string, payload []byte) {
	deviceID := extractDeviceID(topic)
	if deviceID == "" {
		return
	}
	device, err := h.store.GetDevice(deviceID)
	if err != nil || device == nil {
		return
	}
	if device.State == "DEPROVISIONED" {
		log.Printf("[mqtt] device %s acknowledged deprovision", deviceID)
	} else {
		// Device-initiated deprovision — mark deprovisioned in DB so it no longer
		// appears in list/get. IoT resources are left intact for now.
		log.Printf("[mqtt] device %s self-deprovisioned", deviceID)
		if err := h.store.UpdateDeviceState(deviceID, "DEPROVISIONED", true); err != nil {
			log.Printf("[mqtt] error marking %s deprovisioned: %v", deviceID, err)
		}
	}
}

func extractDeviceID(topic string) string {
	parts := strings.SplitN(topic, "/", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
