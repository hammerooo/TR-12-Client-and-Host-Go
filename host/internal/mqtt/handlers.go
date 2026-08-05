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
	"fmt"
	"log"
	"strings"

	"github.com/vsf-tv/TR-12-Client-and-Host-Go/host/internal/broker"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/host/internal/db"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/host/internal/service"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/host/internal/version"
	tr12models "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/TR-12-Models/generated/tr12go"
	"gopkg.in/validator.v2"
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
	// Defense in depth. The SDK's /connect handler already rejected bad payloads
	// before pairing, but a custom or older SDK could bypass that. Validate the
	// registration ourselves — structural (pattern+length via validator.v2 on
	// the enriched struct tags) and semantic (version compat) — and drop the
	// registration on any violation so the device does not appear operational.
	if err := validateHostRegistration(deviceID, envelope.DeviceRegistration); err != nil {
		log.Printf("[mqtt] WARN dropping registration from %s: %v", deviceID, err)
		return
	}
	if err := h.store.UpdateDeviceRegistration(deviceID, envelope.DeviceRegistration); err != nil {
		log.Printf("[mqtt] error storing registration for %s: %v", deviceID, err)
	} else {
		log.Printf("[mqtt] registration updated for %s", deviceID)
	}
}

// validateHostRegistration parses the wire registration into the typed model
// and runs the same class of checks the SDK does before publish: struct-tag
// validation (pattern + length caps from the post-processed tags) and version
// compatibility against the host's own compiled-in TR-12 model.
func validateHostRegistration(deviceID string, raw json.RawMessage) error {
	hostVersion := tr12models.NewProtocolVersionWithDefaults().GetVersion()

	// Deserialize into the typed struct. The generated UnmarshalJSON enforces
	// @required fields (outer version, channelTemplates, channelAssignments;
	// inner version.version) and enum membership.
	var reg tr12models.DeviceRegistration
	if err := json.Unmarshal(raw, &reg); err != nil {
		return fmt.Errorf("malformed (host: %s): %v", hostVersion, err)
	}

	// @pattern + @length constraints via validator.v2.
	if err := validator.Validate(&reg); err != nil {
		return fmt.Errorf("model violation (host: %s): %v", hostVersion, err)
	}

	// Version compatibility.
	ok, err := version.IsCompatible(hostVersion, reg.Version.Version)
	if err != nil {
		return fmt.Errorf("invalid version %q (host: %s): %v", reg.Version.Version, hostVersion, err)
	}
	if !ok {
		return fmt.Errorf("incompatible version %s (host: %s)", reg.Version.Version, hostVersion)
	}
	return nil
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
