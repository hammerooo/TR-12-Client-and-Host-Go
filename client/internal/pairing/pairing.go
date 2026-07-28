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
// Pairing manages the TR-12 pairing and authentication flow with the host service.
package pairing

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/vsf-tv/TR-12-Client-and-Host-Go/client/internal/credentials"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/client/internal/models"
	tr12models "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/TR-12-Models/generated/tr12go"
)

const maxTimeoutSec = 5

// Pairing manages the pairing process with the host service.
type Pairing struct {
	Certs        *credentials.Store
	DeviceType   string
	HostID       string
	PairingURL   string
	AuthURL      string
	StartTime    int64
	PairResponse *models.CreatePairingCodeResponseContent
	AuthResponse *models.AuthenticatePairingCodeResponseContent
	httpClient   *http.Client
}

// New creates a new Pairing instance.
func New(certs *credentials.Store, deviceType, hostID, pairingURL, authURL string) *Pairing {
	// Skip TLS verification for pairing/auth — the CA cert is received as part of
	// the auth response, so we can't verify it beforehand. The device cert received
	// is then used for all subsequent MQTT connections which DO verify against the CA.
	httpClient := &http.Client{
		Timeout: maxTimeoutSec * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	return &Pairing{
		Certs:      certs,
		DeviceType: deviceType,
		HostID:     hostID,
		PairingURL: pairingURL,
		AuthURL:    authURL,
		StartTime:  time.Now().Unix(),
		httpClient: httpClient,
	}
}

// IsExpired returns true if the pairing code has expired.
func (p *Pairing) IsExpired() bool {
	if p.PairResponse == nil {
		return false
	}
	elapsed := time.Now().Unix() - p.StartTime
	return float64(elapsed) > float64(p.PairResponse.PairingTimeoutSeconds)
}

// ExpiresIn returns seconds until the pairing code expires.
func (p *Pairing) ExpiresIn() int {
	if p.PairResponse == nil {
		return 0
	}
	remaining := float64(p.PairResponse.PairingTimeoutSeconds) - float64(time.Now().Unix()-p.StartTime)
	if remaining < 0 {
		return 0
	}
	return int(remaining)
}

// GetPairingCode returns the current pairing code.
func (p *Pairing) GetPairingCode() string {
	if p.PairResponse == nil {
		return ""
	}
	return p.PairResponse.PairingCode
}

// GetNewPairingCode requests a new pairing code from the host service.
// deviceVersion is the device-declared protocol version from the registration;
// it is sent verbatim so the host applies compatibility checks against the
// version the device claims, not against whatever the SDK's own model happens
// to be linked against.
func (p *Pairing) GetNewPairingCode(deviceVersion string) error {
	if err := p.Certs.GenerateKeysAndCSR(); err != nil {
		return fmt.Errorf("failed to generate keys: %w", err)
	}
	reqBody := models.CreatePairingCodeRequestContent{
		DeviceType:                tr12models.DeviceType(p.DeviceType),
		HostId:                    p.HostID,
		CertificateSigningRequest: p.Certs.CSR,
		Version:                   tr12models.ProtocolVersion{Version: deviceVersion},
	}
	body, _ := json.Marshal(reqBody)
	log.Printf("[PAIR] POST %s/pair  body=%s", p.PairingURL, string(body))
	resp, err := p.httpClient.Post(p.PairingURL+"/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("pairing unable to connect to the service: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[PAIR] Response status=%d body=%s", resp.StatusCode, string(respBody))

	// 400 means the host rejected the request — parse the reason and return a descriptive error
	if resp.StatusCode == http.StatusBadRequest {
		var errResp struct {
			Reason string `json:"reason"`
		}
		json.Unmarshal(respBody, &errResp)
		switch tr12models.CreatePairingCodeFailureReason(errResp.Reason) {
		case tr12models.CREATEPAIRINGCODEFAILUREREASON_VERSION_NOT_SUPPORTED:
			return fmt.Errorf("TR-12 version not supported by this host")
		case tr12models.CREATEPAIRINGCODEFAILUREREASON_DEVICE_TYPE_NOT_SUPPORTED:
			return fmt.Errorf("device type %q is not supported by this host", p.DeviceType)
		case tr12models.CREATEPAIRINGCODEFAILUREREASON_HOST_ID_MISMATCH:
			return fmt.Errorf("host ID %q does not match this host service", p.HostID)
		default:
			return fmt.Errorf("pairing rejected by host: %s", errResp.Reason)
		}
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pairing API error - StatusCode: %d - Response: %s", resp.StatusCode, string(respBody))
	}

	var pairResp models.CreatePairingCodeResponseContent
	if err := json.Unmarshal(respBody, &pairResp); err != nil {
		return fmt.Errorf("pairing service response was not valid: %w", err)
	}
	p.PairResponse = &pairResp
	p.StartTime = time.Now().Unix()
	return nil
}

// AuthenticatePairingCode polls the auth endpoint. Returns true if claimed and certs written.
func (p *Pairing) AuthenticatePairingCode() (bool, error) {
	if p.PairResponse == nil {
		return false, fmt.Errorf("no pairing code to authenticate")
	}
	reqBody := models.AuthenticatePairingCodeRequestContent{
		DeviceId:    p.PairResponse.DeviceId,
		PairingCode: p.PairResponse.PairingCode,
		AccessCode:  p.PairResponse.AccessCode,
	}
	body, _ := json.Marshal(reqBody)
	log.Printf("[AUTH] POST %s/authenticate  body=%s", p.AuthURL, string(body))
	resp, err := p.httpClient.Post(p.AuthURL+"/authenticate", "application/json", bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("auth unable to connect to the service: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[AUTH] Response status=%d body=%s", resp.StatusCode, string(respBody))
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("auth API error - StatusCode: %d - Response: %s", resp.StatusCode, string(respBody))
	}
	var authResp models.AuthenticatePairingCodeResponseContent
	if err := json.Unmarshal(respBody, &authResp); err != nil {
		return false, fmt.Errorf("auth service response was not valid: %w", err)
	}
	p.AuthResponse = &authResp
	log.Printf("[AUTH] Parsed: status=%s mqttUri=%q regionName=%q hasHostSettings=%v hasCaCertificate=%v hasDeviceCertificate=%v",
		authResp.Status, authResp.GetMqttUri(), authResp.GetRegionName(),
		authResp.HasHostSettings(), authResp.HasCaCertificate(), authResp.HasDeviceCertificate())

	switch authResp.Status {
	case tr12models.PAIRINGCODEAUTHORIZEDSTATUS_STANDBY:
		return false, nil
	case tr12models.PAIRINGCODEAUTHORIZEDSTATUS_CLAIMED:
		log.Printf("[AUTH] Device CLAIMED — writing certs to filesystem for deviceId=%s", p.PairResponse.DeviceId)
		if err := p.Certs.WriteToFilesystem(p.PairResponse.DeviceId, &authResp); err != nil {
			return false, fmt.Errorf("unable to write certs to disk: %w", err)
		}
		return true, nil
	default:
		return false, fmt.Errorf("unexpected auth status: %s", authResp.Status)
	}
}
