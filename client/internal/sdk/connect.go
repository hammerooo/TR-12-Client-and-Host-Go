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
package sdk

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/vsf-tv/TR-12-Client-and-Host-Go/client/internal/credentials"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/client/internal/models"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/client/internal/utils"
	cddsdkgo "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo"
	tr12models "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/TR-12-Models/generated/tr12go"
)

// Connect implements the TR-12 connect state machine.
func (s *CddSdk) Connect(registration *cddsdkgo.DeviceRegistration, hostID string) cddsdkgo.ConnectResponseContent {
	s.apiLock.Lock()
	defer s.apiLock.Unlock()
	s.logger.Info("Connecting to host:" + hostID)

	// Initialize if needed
	if s.hostID == "" || s.hostID != hostID {
		s.logger.Infof("Initializing host: %s (current: %s)", hostID, s.hostID)
		s.reset()
		if err := s.initializeHost(registration, hostID); err != nil {
			s.logger.Errorf("Failed to initialize host %s: %v", hostID, err)
			s.reset()
			return cddsdkgo.ConnectResponseContent{
				Success: false, State: s.state,
				Message: fmt.Sprintf("Error in connect() %v", err),
				Error:   utils.ExceptionToErrorDetails(err),
			}
		}
		s.logger.Infof("Host initialized: %s", hostID)
	}

	var resp cddsdkgo.ConnectResponseContent
	var err error

	switch s.state {
	case models.StateConnecting:
		resp = cddsdkgo.ConnectResponseContent{
			Success: true, State: s.state, Message: "Connecting to the service",
		}
	case models.StateConnected:
		resp = cddsdkgo.ConnectResponseContent{
			Success:    true, State: s.state, Message: "Connected",
			DeviceId:   tr12models.PtrString(s.certs.GetDeviceID()),
			RegionName: tr12models.PtrString(s.certs.GetRegion()),
		}
	case models.StateReconnecting:
		resp = cddsdkgo.ConnectResponseContent{
			Success:    true, State: s.state, Message: "Reconnecting...",
			DeviceId:   tr12models.PtrString(s.certs.GetDeviceID()),
			RegionName: tr12models.PtrString(s.certs.GetRegion()),
		}
	case models.StatePairing:
		resp, err = s.handlePairingState()
	case models.StateDisconnected:
		resp, err = s.handleDisconnectedState()
	}

	if err != nil {
		s.logger.Errorf("Exception: %v", err)
		s.reset()
		return cddsdkgo.ConnectResponseContent{
			Success: false, State: s.state,
			Message: fmt.Sprintf("Error in connect() %v", err),
			Error:   utils.ExceptionToErrorDetails(err),
		}
	}
	return resp
}

func (s *CddSdk) handlePairingState() (cddsdkgo.ConnectResponseContent, error) {
	loaded, err := s.loadCerts()
	if err != nil {
		return cddsdkgo.ConnectResponseContent{}, err
	}
	if loaded {
		return s.startConnect()
	}
	// Expired?
	if s.pairer.IsExpired() {
		s.reset()
		return cddsdkgo.ConnectResponseContent{
			Success: false, State: s.state,
			Message: "Pairing code expired. Reconnect to get a new one.",
		}, nil
	}
	// Poll for credentials
	claimed, err := s.pairer.AuthenticatePairingCode()
	if err != nil {
		return cddsdkgo.ConnectResponseContent{}, err
	}
	if claimed {
		loaded, err := s.loadCerts()
		if err != nil {
			return cddsdkgo.ConnectResponseContent{}, err
		}
		if loaded {
			hs, _ := s.certs.GetHostSettings()
			if hs != nil {
				s.initThrottles(int(hs.MinimumIntervalPublishSeconds))
			}
			return s.startConnect()
		}
		s.reset()
		return cddsdkgo.ConnectResponseContent{}, fmt.Errorf("device was authenticated, but couldn't load certs")
	}
	// Still waiting
	expiresSeconds := tr12models.PtrFloat32(float32(s.pairer.ExpiresIn()))
	return cddsdkgo.ConnectResponseContent{
		Success:        true, State: models.StatePairing,
		Message:        "Waiting for device to be claimed",
		PairingCode:    tr12models.PtrString(s.pairer.GetPairingCode()),
		ExpiresSeconds: expiresSeconds,
	}, nil
}

func (s *CddSdk) handleDisconnectedState() (cddsdkgo.ConnectResponseContent, error) {
	loaded, err := s.loadCerts()
	if err != nil {
		return cddsdkgo.ConnectResponseContent{}, err
	}
	if loaded {
		hs, _ := s.certs.GetHostSettings()
		if hs != nil {
			s.statusThrottle = utils.NewThrottle(int(hs.MinimumIntervalPublishSeconds))
		}
		return s.startConnect()
	}
	// Need to pair. Use the device-declared version from the registration —
	// initializeHost validated it before we get here, so the string is guaranteed
	// to match MAJOR.MINOR.PATCH.
	if err := s.pairer.GetNewPairingCode(s.registration.Version.Version); err != nil {
		return cddsdkgo.ConnectResponseContent{}, err
	}
	s.transition(models.StatePairing)
	expiresSeconds := tr12models.PtrFloat32(float32(s.pairer.ExpiresIn()))
	return cddsdkgo.ConnectResponseContent{
		Success:        true, State: models.StatePairing,
		Message:        "Connecting pending. Waiting for device to be claimed",
		PairingCode:    tr12models.PtrString(s.pairer.GetPairingCode()),
		ExpiresSeconds: expiresSeconds,
	}, nil
}

// Disconnect gracefully disconnects from the host service.
func (s *CddSdk) Disconnect() cddsdkgo.DisconnectResponseContent {
	s.apiLock.Lock()
	defer s.apiLock.Unlock()
	s.logger.Info("Disconnect")
	s.reset()
	return cddsdkgo.DisconnectResponseContent{
		Success: true, State: models.StateDisconnected, Message: "Disconnected",
	}
}

// GetConnectionStatus returns the current connection state.
func (s *CddSdk) GetConnectionStatus() cddsdkgo.GetConnectionStatusResponseContent {
	s.logger.Info("Get Connection Status")
	return cddsdkgo.GetConnectionStatusResponseContent{
		Success: true, State: s.state, Message: "",
	}
}

// Deprovision removes the device from the host service and deletes local credentials.
// Credentials are always deleted regardless of connection state or the force flag.
// The force parameter is accepted for API compatibility but ignored — deprovision
// always proceeds. If the SDK is currently connected, it notifies the host first;
// if offline, the notification is skipped and credentials are deleted directly.
func (s *CddSdk) Deprovision(hostID string, _ bool) cddsdkgo.DeprovisionResponseContent {
	s.apiLock.Lock()
	defer s.apiLock.Unlock()
	s.logger.Info("Deprovision")

	if err := s.handleDeprovision(hostID); err != nil {
		return cddsdkgo.DeprovisionResponseContent{
			Success: false, State: s.state,
			Message: fmt.Sprintf("Error in Deprovision: %v", err),
			Error:   utils.ExceptionToErrorDetails(err),
		}
	}
	return cddsdkgo.DeprovisionResponseContent{
		Success: true, State: s.state,
		Message: fmt.Sprintf("Deprovisioned credentials for host: %s", hostID),
	}
}

// GetConfiguration returns the latest cached configuration.
func (s *CddSdk) GetConfiguration() cddsdkgo.GetConfigurationResponseContent {
	s.apiLock.Lock()
	defer s.apiLock.Unlock()
	s.logger.Info("Get Configuration")
	var configData *cddsdkgo.ConfigurationData
	if s.configPayload != nil {
		configData = cddsdkgo.NewConfigurationData()
		configData.Payload = s.configPayload
	}
	resp := cddsdkgo.NewGetConfigurationResponseContent(true, s.state, "Latest configuration provided")
	resp.Configuration = configData
	return *resp
}

// ReportStatus publishes a status payload to the host service.
// The payload is already a validated DeviceStatus — deserialised at the HTTP boundary.
func (s *CddSdk) ReportStatus(payload *cddsdkgo.DeviceStatus) cddsdkgo.ReportStatusResponseContent {
	s.apiLock.Lock()
	defer s.apiLock.Unlock()
	s.logger.Info("Report Status")

	// Truncate any health message exceeding the model limit before publishing.
	// A long message from a device API error could push the MQTT payload over the
	// 128KB broker limit. The spec defines max 128 chars on HealthError.message.
	const healthMessageMaxLen = 128
	if payload != nil {
		if payload.Health != nil {
			truncateHealthMessage(payload.Health, healthMessageMaxLen)
		}
		for i := range payload.Channels {
			if payload.Channels[i].Health != nil {
				truncateHealthMessage(payload.Channels[i].Health, healthMessageMaxLen)
			}
		}
	}

	if err := s.canPublishNow(s.statusThrottle); err != nil {
		return cddsdkgo.ReportStatusResponseContent{
			Success: false, State: s.state, Message: err.Error(),
			Error: utils.ExceptionToErrorDetails(err),
		}
	}
	hs, err := s.certs.GetHostSettings()
	if err != nil {
		return cddsdkgo.ReportStatusResponseContent{
			Success: false, State: s.state, Message: err.Error(),
			Error: utils.ExceptionToErrorDetails(err),
		}
	}
	envelope := map[string]interface{}{"deviceStatus": payload}
	if err := s.doPublishMessage(envelope, hs.DevicePublishesStatusTopic); err != nil {
		return cddsdkgo.ReportStatusResponseContent{
			Success: false, State: s.state,
			Message: fmt.Sprintf("Status update not sent: %v", err),
			Error:   utils.ExceptionToErrorDetails(err),
		}
	}
	return cddsdkgo.ReportStatusResponseContent{
		Success: true, State: s.state, Message: "Status update sent",
	}
}

func (s *CddSdk) ReportConfiguration(payload *cddsdkgo.ActualDeviceConfiguration) cddsdkgo.ReportActualConfigurationResponseContent {
	s.apiLock.Lock()
	defer s.apiLock.Unlock()

	// Store the actual configuration for thumbnail path resolution
	s.actualConfig.Store(payload)

	if err := s.canPublishNow(s.configThrottle); err != nil {
		resp := cddsdkgo.NewReportActualConfigurationResponseContent(false, s.state, err.Error())
		return *resp
	}
	hs, err := s.certs.GetHostSettings()
	if err != nil {
		resp := cddsdkgo.NewReportActualConfigurationResponseContent(false, s.state, err.Error())
		return *resp
	}
	// Wrap in envelope — this is what actually goes on the wire.
	envelope := map[string]interface{}{"actualDeviceConfiguration": payload}
	// Log the wire payload so debugging matches what the host receives.
	if envBytes, err := json.Marshal(envelope); err == nil {
		s.logger.Infof("Report Configuration payload: %s", string(envBytes))
	} else {
		s.logger.Info("Report Configuration")
	}
	if err := s.doPublishMessage(envelope, hs.DevicePublishesActualConfigurationTopic); err != nil {
		resp := cddsdkgo.NewReportActualConfigurationResponseContent(false, s.state, fmt.Sprintf("Configuration update not sent: %v", err))
		return *resp
	}
	resp := cddsdkgo.NewReportActualConfigurationResponseContent(true, s.state, "Configuration update sent")
	return *resp
}

func (s *CddSdk) handleDeprovision(hostID string) error {
	// Inform host before resetting so connectedTo() check still works
	if s.connectedTo(hostID) {
		s.informHostServiceDeprovision(hostID)
	}
	err := s.deleteCredentials(hostID)
	// Reset last — mirrors Python's finally: self._reset()
	// This ensures connect() cannot sneak in and start pairing before certs are deleted.
	s.reset()
	return err
}

func (s *CddSdk) informHostServiceDeprovision(hostID string) {
	if !s.connectedTo(hostID) {
		return
	}
	s.logger.Infof("Publishing deprovision message to host service: %s", hostID)
	hs, err := s.certs.GetHostSettings()
	if err != nil {
		return
	}
	reason := tr12models.DEPROVISIONREASON_DEPROVISIONED
	t := time.Now().UTC()
	deprov := tr12models.DevicePublishesDeprovisionAcknowledgementRequestContent{
		Reason:    &reason,
		Timestamp: t,
	}
	data, _ := json.Marshal(deprov)
	if s.mqttClient != nil {
		token := s.mqttClient.Publish(hs.DevicePublishesDeprovisionAcknowledgementTopic, 1, false, data)
		token.WaitTimeout(3 * time.Second)
	}
}

func (s *CddSdk) deleteCredentials(hostID string) error {
	if s.hostID == "" || s.hostID == hostID {
		s.logger.Infof("Deleting credentials for %s", hostID)
		store, err := credentials.NewStore(s.certsPath, s.deviceLocalID, hostID)
		if err != nil {
			return err
		}
		return store.Deprovision()
	}
	return nil
}

// truncateHealthMessage truncates the message field in a Health union to maxLen characters.
// This prevents oversized health messages from bloating MQTT payloads.
func truncateHealthMessage(h *cddsdkgo.Health, maxLen int) {
	if h == nil {
		return
	}
	if h.Degraded != nil && len(h.Degraded.Degraded.Message) > maxLen {
		h.Degraded.Degraded.Message = h.Degraded.Degraded.Message[:maxLen]
	}
	if h.Critical != nil && len(h.Critical.Critical.Message) > maxLen {
		h.Critical.Critical.Message = h.Critical.Critical.Message[:maxLen]
	}
}
