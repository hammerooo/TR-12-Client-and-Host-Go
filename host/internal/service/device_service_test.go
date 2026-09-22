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
package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/vsf-tv/TR-12-Client-and-Host-Go/host/internal/ca"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/host/internal/config"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/host/internal/db"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/host/internal/models"
)

// mockMQTT captures published messages for assertions.
type mockMQTT struct {
	messages []mqttMsg
}

type mqttMsg struct {
	Topic   string
	Payload []byte
	Retain  bool
}

func (m *mockMQTT) Publish(topic string, payload []byte, retain bool) error {
	m.messages = append(m.messages, mqttMsg{Topic: topic, Payload: payload, Retain: retain})
	return nil
}

func newTestDeviceService(t *testing.T) (*DeviceService, *mockMQTT, *db.Store) {
	t.Helper()
	store, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	caInst, err := ca.New(store, "127.0.0.1")
	if err != nil {
		t.Fatalf("ca.New: %v", err)
	}

	mqtt := &mockMQTT{}
	cfg := &config.Config{
		ServiceID:      "test-host",
		ServiceName:    "Test Host",
		HostAddress:    "127.0.0.1",
		MQTTPort:       8883,
		CertExpiryDays: 30,
		PairingTimeout: 1800,
	}
	svc := NewDeviceService(store, caInst, mqtt, cfg)
	return svc, mqtt, store
}

func generateTestCSR(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.CertificateRequest{
		Subject: pkix.Name{Organization: []string{"Test"}},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		t.Fatalf("create CSR: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}))
}

// doPair is a helper that pairs a device and returns the device ID, pairing code, and access code.
func doPair(t *testing.T, svc *DeviceService) (deviceID, pairingCode, accessCode string) {
	t.Helper()
	csr := generateTestCSR(t)
	resp, pairingErr, err := svc.Pair(models.CreatePairingCodeRequestContent{
		HostId:     "test-host",
		Version:    models.ProtocolVersion{Version: "1.0"},
		DeviceType: "SOURCE",
		CertificateSigningRequest: csr,
	})
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if pairingErr != nil {
		t.Fatalf("Pair rejected: %s", *pairingErr)
	}
	return resp.DeviceId, resp.PairingCode, resp.AccessCode
}

// --- Pair ---

func TestPair_Success(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	deviceID, pairingCode, accessCode := doPair(t, svc)

	if len(deviceID) != 21 {
		t.Fatalf("expected 21-char device ID, got %d: %q", len(deviceID), deviceID)
	}
	if len(pairingCode) != 6 {
		t.Fatalf("expected 6-char pairing code, got %d: %q", len(pairingCode), pairingCode)
	}
	if len(accessCode) != 32 {
		t.Fatalf("expected 32-char access code, got %d: %q", len(accessCode), accessCode)
	}
}

func TestPair_HostIDMismatch(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	_, pairingErr, err := svc.Pair(models.CreatePairingCodeRequestContent{
		HostId:     "wrong-host",
		Version:    models.ProtocolVersion{Version: "1.0"},
		DeviceType: "SOURCE",
		CertificateSigningRequest: generateTestCSR(t),
	})
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if pairingErr == nil {
		t.Fatal("expected rejection for host ID mismatch")
	}
	if *pairingErr != models.PairFailureHostIDMismatch {
		t.Fatalf("expected HOST_ID_MISMATCH, got %s", *pairingErr)
	}
}

func TestPair_BadDeviceType(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	_, pairingErr, _ := svc.Pair(models.CreatePairingCodeRequestContent{
		HostId:     "test-host",
		Version:    models.ProtocolVersion{Version: "1.0"},
		DeviceType: "INVALID",
		CertificateSigningRequest: generateTestCSR(t),
	})
	if pairingErr == nil {
		t.Fatal("expected rejection for bad device type")
	}
	if *pairingErr != models.PairFailureDeviceTypeNotSupported {
		t.Fatalf("expected DEVICE_TYPE_NOT_SUPPORTED, got %s", *pairingErr)
	}
}

func TestPair_EmptyVersion(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	_, pairingErr, _ := svc.Pair(models.CreatePairingCodeRequestContent{
		HostId:     "test-host",
		Version:    models.ProtocolVersion{Version: ""},
		DeviceType: "SOURCE",
		CertificateSigningRequest: generateTestCSR(t),
	})
	if pairingErr == nil {
		t.Fatal("expected rejection for empty version")
	}
	if *pairingErr != models.PairFailureVersionNotSupported {
		t.Fatalf("expected VERSION_NOT_SUPPORTED, got %s", *pairingErr)
	}
}

// --- Authenticate ---

func TestAuthenticate_Standby(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	deviceID, pairingCode, accessCode := doPair(t, svc)

	resp, err := svc.Authenticate(models.AuthenticatePairingCodeRequestContent{
		DeviceId:    deviceID,
		PairingCode: pairingCode,
		AccessCode:  accessCode,
	})
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if resp.GetStatus() != models.AuthStatusSTANDBY {
		t.Fatalf("expected STANDBY, got %v", resp.GetStatus())
	}
}

func TestAuthenticate_Claimed(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	deviceID, pairingCode, accessCode := doPair(t, svc)

	// Claim the device
	if err := svc.Claim(pairingCode, "acc-1", 730, 0, "", "", 365); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	resp, err := svc.Authenticate(models.AuthenticatePairingCodeRequestContent{
		DeviceId:    deviceID,
		PairingCode: pairingCode,
		AccessCode:  accessCode,
	})
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if resp.GetStatus() != models.AuthStatusCLAIMED {
		t.Fatalf("expected CLAIMED, got %v", resp.GetStatus())
	}
	if resp.GetCaCertificate() == "" {
		t.Fatal("expected CA cert in claimed response")
	}
	if resp.GetMqttUri() == "" {
		t.Fatal("expected MQTT URI in claimed response")
	}
}

func TestAuthenticate_WrongCredentials(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	deviceID, _, _ := doPair(t, svc)

	// Per spec, AuthenticatePairingCode has no error case — wrong credentials
	// return STANDBY rather than an error, to prevent device ID enumeration.
	resp, err := svc.Authenticate(models.AuthenticatePairingCodeRequestContent{
		DeviceId:    deviceID,
		PairingCode: "WRONG1",
		AccessCode:  "wrong",
	})
	if err != nil {
		t.Fatalf("expected no error for wrong credentials, got %v", err)
	}
	if resp.GetStatus() != models.AuthStatusSTANDBY {
		t.Fatalf("expected STANDBY for wrong credentials, got %v", resp.GetStatus())
	}
}

func TestAuthenticate_NotFound(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	// Per spec, AuthenticatePairingCode has no error case — unknown device ID
	// returns STANDBY rather than 404, to prevent device ID enumeration.
	resp, err := svc.Authenticate(models.AuthenticatePairingCodeRequestContent{
		DeviceId:    "nonexistent",
		PairingCode: "ABC123",
		AccessCode:  "secret",
	})
	if err != nil {
		t.Fatalf("expected no error for unknown device ID, got %v", err)
	}
	if resp.GetStatus() != models.AuthStatusSTANDBY {
		t.Fatalf("expected STANDBY for unknown device ID, got %v", resp.GetStatus())
	}
}

// --- Claim ---

func TestClaim_Success(t *testing.T) {
	svc, _, store := newTestDeviceService(t)
	_, pairingCode, _ := doPair(t, svc)

	if err := svc.Claim(pairingCode, "acc-1", 730, 0, "", "", 365); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// Verify device is now ACTIVE
	d, _ := store.GetDeviceByPairingCode(pairingCode)
	if d.State != "ACTIVE" {
		t.Fatalf("expected ACTIVE, got %q", d.State)
	}
	if d.AccountID != "acc-1" {
		t.Fatalf("expected acc-1, got %q", d.AccountID)
	}
}

func TestClaim_NotFound(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	err := svc.Claim("NOPE00", "acc-1", 730, 0, "", "", 365)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestClaim_AlreadyClaimed(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	_, pairingCode, _ := doPair(t, svc)
	svc.Claim(pairingCode, "acc-1", 730, 0, "", "", 365)

	err := svc.Claim(pairingCode, "acc-2", 730, 0, "", "", 365)
	if err != ErrConflict {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

// --- ListDevices ---

func TestListDevices(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	_, pc1, _ := doPair(t, svc)
	_, pc2, _ := doPair(t, svc)
	svc.Claim(pc1, "acc-1", 730, 0, "", "", 365)
	svc.Claim(pc2, "acc-1", 730, 0, "", "", 365)

	summaries, err := svc.ListDevices("acc-1")
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(summaries))
	}
}

// --- DescribeDevice ---

func TestDescribeDevice_Success(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	deviceID, pc, _ := doPair(t, svc)
	svc.Claim(pc, "acc-1", 730, 0, "", "", 365)

	detail, err := svc.DescribeDevice(deviceID, "acc-1")
	if err != nil {
		t.Fatalf("DescribeDevice: %v", err)
	}
	if detail.DeviceID != deviceID {
		t.Fatalf("expected %q, got %q", deviceID, detail.DeviceID)
	}
}

func TestDescribeDevice_NotFound(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	_, err := svc.DescribeDevice("nonexistent", "acc-1")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDescribeDevice_Forbidden(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	deviceID, pc, _ := doPair(t, svc)
	svc.Claim(pc, "acc-1", 730, 0, "", "", 365)

	_, err := svc.DescribeDevice(deviceID, "acc-other")
	if err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

// --- UpdateConfiguration ---

func TestUpdateConfiguration_Success(t *testing.T) {
	svc, mqtt, store := newTestDeviceService(t)
	deviceID, pc, _ := doPair(t, svc)
	svc.Claim(pc, "acc-1", 730, 0, "", "", 365)

	// Set registration so validation passes
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE","settings":[{"id":"brightness","name":"Brightness","description":"Brightness","constraint":{"enums":{"values":["low","high"],"defaultValue":"low"}}}],"profiles":[{"id":"prof1","name":"P1","description":"P1"}]}],"channelAssignments":[{"channelId":"ch1","name":"Channel 1","templateId":"tmpl1"}]}`)
	store.UpdateDeviceRegistration(deviceID, reg)

	cfg := json.RawMessage(`{"channels":[{"id":"ch1","state":"ACTIVE"}]}`)
	if err := svc.UpdateConfiguration(deviceID, "acc-1", cfg); err != nil {
		t.Fatalf("UpdateConfiguration: %v", err)
	}

	// Verify MQTT message was published
	if len(mqtt.messages) == 0 {
		t.Fatal("expected MQTT message")
	}
	expectedTopic := "cdd/" + deviceID + "/config/update"
	if mqtt.messages[0].Topic != expectedTopic {
		t.Fatalf("expected topic %q, got %q", expectedTopic, mqtt.messages[0].Topic)
	}
}

func TestUpdateConfiguration_ValidationError(t *testing.T) {
	svc, _, store := newTestDeviceService(t)
	deviceID, pc, _ := doPair(t, svc)
	svc.Claim(pc, "acc-1", 730, 0, "", "", 365)

	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE"}],"channelAssignments":[{"channelId":"ch1","name":"Channel 1","templateId":"tmpl1"}]}`)
	store.UpdateDeviceRegistration(deviceID, reg)

	// Unknown channel ID
	cfg := json.RawMessage(`{"channels":[{"id":"ch-unknown","state":"ACTIVE"}]}`)
	err := svc.UpdateConfiguration(deviceID, "acc-1", cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown channel ID") {
		t.Fatalf("expected validation error about unknown channel, got %v", err)
	}
}

// --- Deprovision ---

func TestDeprovision(t *testing.T) {
	svc, mqtt, store := newTestDeviceService(t)
	deviceID, pc, _ := doPair(t, svc)
	svc.Claim(pc, "acc-1", 730, 0, "", "", 365)

	if err := svc.Deprovision(deviceID, "acc-1"); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}

	d, _ := store.GetDevice(deviceID)
	if d.State != "DEPROVISIONED" {
		t.Fatalf("expected DEPROVISIONED, got %q", d.State)
	}

	// Verify MQTT deprovision message
	found := false
	for _, msg := range mqtt.messages {
		if strings.Contains(msg.Topic, "deprovision") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected deprovision MQTT message")
	}

	// Idempotent
	if err := svc.Deprovision(deviceID, "acc-1"); err != nil {
		t.Fatalf("second Deprovision should be idempotent: %v", err)
	}
}

func TestDeprovision_Forbidden(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	deviceID, pc, _ := doPair(t, svc)
	svc.Claim(pc, "acc-1", 730, 0, "", "", 365)

	err := svc.Deprovision(deviceID, "acc-other")
	if err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

// --- FullCleanup ---

func TestFullCleanup(t *testing.T) {
	svc, _, store := newTestDeviceService(t)
	deviceID, pc, _ := doPair(t, svc)
	svc.Claim(pc, "acc-1", 730, 0, "", "", 365)

	// Add thumbnail and log
	store.UpsertThumbnail(&db.Thumbnail{DeviceID: deviceID, ChannelID: "ch1", ImageData: []byte{1}, Timestamp: "now", ImageType: "jpeg", ImageSizeKB: 1})
	store.UpsertLog(&db.DeviceLog{DeviceID: deviceID, LogData: []byte("log"), UploadedAt: "now", LogSizeKB: 1})

	if err := svc.FullCleanup(deviceID); err != nil {
		t.Fatalf("FullCleanup: %v", err)
	}

	d, _ := store.GetDevice(deviceID)
	if d != nil {
		t.Fatal("expected device to be deleted")
	}
	thumb, _ := store.GetThumbnail(deviceID, "ch1")
	if thumb != nil {
		t.Fatal("expected thumbnail to be deleted")
	}
	log, _ := store.GetLog(deviceID)
	if log != nil {
		t.Fatal("expected log to be deleted")
	}
}

// --- RotateCredentials ---

func TestRotateCredentials(t *testing.T) {
	svc, mqtt, store := newTestDeviceService(t)
	deviceID, pc, _ := doPair(t, svc)
	svc.Claim(pc, "acc-1", 730, 0, "", "", 365)

	if err := svc.RotateCredentials(deviceID, "acc-1"); err != nil {
		t.Fatalf("RotateCredentials: %v", err)
	}

	d, _ := store.GetDevice(deviceID)
	if d.CurrentCertPEM == "" {
		t.Fatal("expected current cert to be set")
	}
	if d.PreviousCertPEM == "" {
		t.Fatal("expected previous cert to be set")
	}
	if d.LastRotationAt == "" {
		t.Fatal("expected last rotation to be set")
	}

	// Verify MQTT message
	found := false
	for _, msg := range mqtt.messages {
		if strings.Contains(msg.Topic, "certs/update") {
			found = true
			if !msg.Retain {
				t.Fatal("expected cert rotation message to be retained")
			}
			break
		}
	}
	if !found {
		t.Fatal("expected cert rotation MQTT message")
	}
}

func TestRotateCredentials_NotActive(t *testing.T) {
	svc, _, _ := newTestDeviceService(t)
	deviceID, _, _ := doPair(t, svc) // still in PAIRING state

	err := svc.RotateCredentials(deviceID, "")
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("expected 'not active' error, got %v", err)
	}
}

// --- validateConfiguration ---

func TestValidateConfiguration_ValidConfig(t *testing.T) {
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE","settings":[{"id":"brightness","name":"Brightness","description":"Brightness","constraint":{"enums":{"values":["low","high"],"defaultValue":"low"}}}],"profiles":[{"id":"prof1","name":"P1","description":"P1"}]}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	cfg := json.RawMessage(`{"channels":[{"id":"ch1","state":"ACTIVE","channelSettings":{"standardSettings":[{"id":"brightness","value":"low"}]}}]}`)
	if err := validateConfiguration(cfg, reg); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateConfiguration_UnknownChannel(t *testing.T) {
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE"}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	cfg := json.RawMessage(`{"channels":[{"id":"ch-bad"}]}`)
	err := validateConfiguration(cfg, reg)
	if err == nil || !strings.Contains(err.Error(), "unknown channel ID") {
		t.Fatalf("expected unknown channel error, got %v", err)
	}
}

func TestValidateConfiguration_UnknownSettingKey(t *testing.T) {
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE","settings":[{"id":"brightness","name":"Brightness","description":"Brightness","constraint":{"enums":{"values":["low","high"],"defaultValue":"low"}}}]}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	cfg := json.RawMessage(`{"channels":[{"id":"ch1","channelSettings":{"standardSettings":[{"id":"contrast","value":"50"}]}}]}`)
	err := validateConfiguration(cfg, reg)
	if err == nil || !strings.Contains(err.Error(), "unknown setting key") {
		t.Fatalf("expected unknown setting key error, got %v", err)
	}
}

func TestValidateConfiguration_UnknownProfile(t *testing.T) {
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE","profiles":[{"id":"prof1","name":"P1","description":"P1"}]}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	cfg := json.RawMessage(`{"channels":[{"id":"ch1","channelSettings":{"profile":{"id":"prof-bad"}}}]}`)
	err := validateConfiguration(cfg, reg)
	if err == nil || !strings.Contains(err.Error(), "unknown profile ID") {
		t.Fatalf("expected unknown profile error, got %v", err)
	}
}

func TestValidateConfiguration_InvalidChannelState(t *testing.T) {
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE"}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	cfg := json.RawMessage(`{"channels":[{"id":"ch1","state":"BROKEN"}]}`)
	err := validateConfiguration(cfg, reg)
	if err == nil || !strings.Contains(err.Error(), "invalid channel state") {
		t.Fatalf("expected invalid channel state error, got %v", err)
	}
}

func TestValidateConfiguration_DeviceLevelSettingValid(t *testing.T) {
	reg := json.RawMessage(`{"settings":[{"id":"clock_source","name":"Clock","description":"Clock","constraint":{"enums":{"values":["NTP","PTP"],"defaultValue":"NTP"}}}],"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE"}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	cfg := json.RawMessage(`{"standardSettings":[{"id":"clock_source","value":"PTP"}],"channels":[{"id":"ch1","state":"ACTIVE"}]}`)
	if err := validateConfiguration(cfg, reg); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateConfiguration_DeviceLevelSettingUnknown(t *testing.T) {
	reg := json.RawMessage(`{"settings":[{"id":"clock_source","name":"Clock","description":"Clock","constraint":{"enums":{"values":["NTP","PTP"],"defaultValue":"NTP"}}}],"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE"}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	cfg := json.RawMessage(`{"standardSettings":[{"id":"bogus_setting","value":"foo"}],"channels":[{"id":"ch1","state":"ACTIVE"}]}`)
	err := validateConfiguration(cfg, reg)
	if err == nil || !strings.Contains(err.Error(), "unknown device-level setting key") {
		t.Fatalf("expected unknown device-level setting key error, got %v", err)
	}
	if !strings.Contains(err.Error(), "bogus_setting") {
		t.Fatalf("expected error to mention bogus_setting, got %v", err)
	}
}

func TestValidateConfiguration_ConnectionValid(t *testing.T) {
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE","protocols":["SRT_CALLER"]}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	cfg := json.RawMessage(`{"channels":[{"id":"ch1","state":"ACTIVE","protocol":{"srtCaller":{"address":"1.2.3.4","port":9000,"minimumLatencyMilliseconds":200}}}]}`)
	if err := validateConfiguration(cfg, reg); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateConfiguration_ConnectionUnknownProtocolType(t *testing.T) {
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE","protocols":["SRT_CALLER"]}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	cfg := json.RawMessage(`{"channels":[{"id":"ch1","state":"ACTIVE","protocol":{"rtmpCaller":{"address":"1.2.3.4"}}}]}`)
	err := validateConfiguration(cfg, reg)
	if err == nil || !strings.Contains(err.Error(), "unknown transport protocol type") {
		t.Fatalf("expected unknown transport protocol type error, got %v", err)
	}
}

func TestValidateConfiguration_ConnectionProtocolNotRegistered(t *testing.T) {
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE","protocols":["SRT_LISTENER"]}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	cfg := json.RawMessage(`{"channels":[{"id":"ch1","state":"ACTIVE","protocol":{"srtCaller":{"address":"1.2.3.4","port":9000,"minimumLatencyMilliseconds":200}}}]}`)
	err := validateConfiguration(cfg, reg)
	if err == nil || !strings.Contains(err.Error(), "not supported by channel") {
		t.Fatalf("expected protocol not supported error, got %v", err)
	}
}

func TestValidateConfiguration_ConnectionUnknownField(t *testing.T) {
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE","protocols":["SRT_CALLER"]}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	// "address" and "ports" are wrong — "ports" should be "port"
	cfg := json.RawMessage(`{"channels":[{"id":"ch1","state":"ACTIVE","protocol":{"srtCaller":{"address":"1.2.3.4","ports":9000,"minimumLatencyMilliseconds":200}}}]}`)
	err := validateConfiguration(cfg, reg)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
	if !strings.Contains(err.Error(), "ports") {
		t.Fatalf("expected error to mention 'ports', got %v", err)
	}
}

func TestValidateConfiguration_ConnectionMissingRequiredField(t *testing.T) {
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE","protocols":["SRT_CALLER"]}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	// Missing "address" which is required for srtCaller
	cfg := json.RawMessage(`{"channels":[{"id":"ch1","state":"ACTIVE","protocol":{"srtCaller":{"port":9000,"minimumLatencyMilliseconds":200}}}]}`)
	err := validateConfiguration(cfg, reg)
	if err == nil || !strings.Contains(err.Error(), "missing required field") {
		t.Fatalf("expected missing required field error, got %v", err)
	}
	if !strings.Contains(err.Error(), `"address"`) {
		t.Fatalf("expected error to mention 'address', got %v", err)
	}
}

func TestValidateConfiguration_ConnectionSrtListenerValid(t *testing.T) {
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE","protocols":["SRT_LISTENER"]}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	cfg := json.RawMessage(`{"channels":[{"id":"ch1","state":"ACTIVE","protocol":{"srtListener":{"port":9000,"minimumLatencyMilliseconds":200}}}]}`)
	if err := validateConfiguration(cfg, reg); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateConfiguration_ConnectionWithOptionalFields(t *testing.T) {
	reg := json.RawMessage(`{"channelTemplates":[{"id":"tmpl1","channelType":"SOURCE","protocols":["SRT_CALLER"]}],"channelAssignments":[{"channelId":"ch1","name":"Ch1","templateId":"tmpl1"}]}`)
	cfg := json.RawMessage(`{"channels":[{"id":"ch1","state":"ACTIVE","protocol":{"srtCaller":{"address":"1.2.3.4","port":9000,"minimumLatencyMilliseconds":200,"streamId":"test123"}}}]}`)
	if err := validateConfiguration(cfg, reg); err != nil {
		t.Fatalf("expected valid with optional streamId, got %v", err)
	}
}

// --- UpdateChannelConfig (TC-H01 through TC-H08) ---

// fourChannelReg is a 4-channel registration used by most per-channel tests.
// It includes a device-level setting (sync_clock_source) so that pushInitialConfig
// can include standardSettings in its payload without triggering a validation error.
const fourChannelReg = `{
	"settings":[{"id":"sync_clock_source","name":"Clock Source","description":"Clock source","constraint":{"enums":{"values":["NTP","PTP"],"defaultValue":"NTP"}}}],
	"channelTemplates":[
		{"id":"tmpl1","channelType":"SOURCE","protocols":["SRT_CALLER"],"settings":[{"id":"RS01","name":"res","description":"res","constraint":{"enums":{"values":["1920x1080"],"defaultValue":"1920x1080"}}}]}
	],
	"channelAssignments":[
		{"channelId":"CH01","name":"Channel 1","templateId":"tmpl1"},
		{"channelId":"CH02","name":"Channel 2","templateId":"tmpl1"},
		{"channelId":"CH03","name":"Channel 3","templateId":"tmpl1"},
		{"channelId":"CH04","name":"Channel 4","templateId":"tmpl1"}
	]
}`

// doClaimWithReg pairs, claims, and sets registration for a device.
func doClaimWithReg(t *testing.T, svc *DeviceService, store interface {
	UpdateDeviceRegistration(string, json.RawMessage) error
}, accountID string) string {
	t.Helper()
	deviceID, pc, _ := doPair(t, svc)
	if err := svc.Claim(pc, accountID, 730, 0, "", "", 365); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.UpdateDeviceRegistration(deviceID, json.RawMessage(fourChannelReg)); err != nil {
		t.Fatalf("UpdateDeviceRegistration: %v", err)
	}
	return deviceID
}

// pushInitialConfig pushes a 4-channel config so the stored config is populated.
func pushInitialConfig(t *testing.T, svc *DeviceService, deviceID, accountID string) {
	t.Helper()
	cfg := json.RawMessage(`{
		"channels":[
			{"id":"CH01","state":"IDLE","channelSettings":{"standardSettings":[{"id":"RS01","value":"1920x1080"}]}},
			{"id":"CH02","state":"IDLE","channelSettings":{"standardSettings":[{"id":"RS01","value":"1920x1080"}]}},
			{"id":"CH03","state":"IDLE","channelSettings":{"standardSettings":[{"id":"RS01","value":"1920x1080"}]}},
			{"id":"CH04","state":"IDLE","channelSettings":{"standardSettings":[{"id":"RS01","value":"1920x1080"}]}}
		],
		"standardSettings":[{"id":"sync_clock_source","value":"NTP"}]
	}`)
	if err := svc.UpdateConfiguration(deviceID, accountID, cfg); err != nil {
		t.Fatalf("pushInitialConfig: %v", err)
	}
}

// channelVersions extracts channel versions from the DB-stored desired config
// (via the last MQTT payload's updateId, but we read from the stored config directly
// since per-channel updates only send the updated channel in MQTT).
// For tests we read back the device's desired config from the store.
func channelVersionsFromStore(t *testing.T, store *db.Store, deviceID string) map[string]string {
	t.Helper()
	device, err := store.GetDevice(deviceID)
	if err != nil || device == nil {
		t.Fatalf("channelVersionsFromStore: device not found: %v", err)
	}
	if len(device.DesiredConfig) == 0 {
		return map[string]string{}
	}
	var cfg struct {
		Channels []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(device.DesiredConfig, &cfg); err != nil {
		t.Fatalf("channelVersionsFromStore: unmarshal: %v", err)
	}
	out := map[string]string{}
	for _, ch := range cfg.Channels {
		out[ch.ID] = ch.Version
	}
	return out
}

// channelVersions reads the MQTT payload for the channel count in the MQTT message
// (for verifying isolation), but version truth comes from the DB store.
func channelVersions(t *testing.T, mqtt *mockMQTT) map[string]string {
	t.Helper()
	if len(mqtt.messages) == 0 {
		t.Fatal("no MQTT messages published")
	}
	last := mqtt.messages[len(mqtt.messages)-1]
	var env struct {
		Config struct {
			Channels []struct {
				ID      string `json:"id"`
				Version string `json:"version"`
			} `json:"channels"`
		} `json:"desiredDeviceConfiguration"`
	}
	if err := json.Unmarshal(last.Payload, &env); err != nil {
		t.Fatalf("channelVersions: unmarshal: %v", err)
	}
	out := map[string]string{}
	for _, ch := range env.Config.Channels {
		out[ch.ID] = ch.Version
	}
	return out
}

// TC-H01: Updating CH03 bumps only CH03's version.
func TestUpdateChannelConfig_OnlyTargetChannelVersionBumped(t *testing.T) {
	svc, mqtt, store := newTestDeviceService(t)
	deviceID := doClaimWithReg(t, svc, store, "acc-1")
	pushInitialConfig(t, svc, deviceID, "acc-1")

	// Record versions from DB after initial push.
	v0 := channelVersionsFromStore(t, store, deviceID)

	// Update only CH03.
	ch03cfg := json.RawMessage(`{"state":"ACTIVE","channelSettings":{"standardSettings":[{"id":"RS01","value":"1920x1080"}]}}`)
	if err := svc.UpdateChannelConfig(deviceID, "acc-1", "CH03", ch03cfg); err != nil {
		t.Fatalf("UpdateChannelConfig: %v", err)
	}

	// Read versions from DB (source of truth for all channels).
	v1 := channelVersionsFromStore(t, store, deviceID)

	if v1["CH03"] == v0["CH03"] {
		t.Errorf("TC-H01: CH03 version should have changed, still %q", v1["CH03"])
	}
	if v1["CH01"] != v0["CH01"] {
		t.Errorf("TC-H01: CH01 version changed unexpectedly: %q → %q", v0["CH01"], v1["CH01"])
	}
	if v1["CH02"] != v0["CH02"] {
		t.Errorf("TC-H01: CH02 version changed unexpectedly: %q → %q", v0["CH02"], v1["CH02"])
	}
	if v1["CH04"] != v0["CH04"] {
		t.Errorf("TC-H01: CH04 version changed unexpectedly: %q → %q", v0["CH04"], v1["CH04"])
	}

	// Verify the MQTT message contains all channels (full config). The client-side
	// ApplicationLoop is version-gated and skips unchanged channels.
	mqttVersions := channelVersions(t, mqtt)
	if len(mqttVersions) != 4 {
		t.Errorf("TC-H01: MQTT payload should contain all 4 channels (full config), got %d", len(mqttVersions))
	}
	if _, ok := mqttVersions["CH03"]; !ok {
		t.Errorf("TC-H01: MQTT payload should contain CH03, got keys: %v", mqttVersions)
	}
}

// TC-H02: Updating CH03 twice bumps CH03 version each time (monotonic).
func TestUpdateChannelConfig_VersionMonotonic(t *testing.T) {
	svc, _, store := newTestDeviceService(t)
	deviceID := doClaimWithReg(t, svc, store, "acc-1")
	pushInitialConfig(t, svc, deviceID, "acc-1")

	ch03cfg := json.RawMessage(`{"state":"ACTIVE"}`)
	svc.UpdateChannelConfig(deviceID, "acc-1", "CH03", ch03cfg)
	v1 := channelVersionsFromStore(t, store, deviceID)

	svc.UpdateChannelConfig(deviceID, "acc-1", "CH03", ch03cfg)
	v2 := channelVersionsFromStore(t, store, deviceID)

	if v2["CH03"] == v1["CH03"] {
		t.Errorf("TC-H02: CH03 version did not change on second update: %q", v2["CH03"])
	}
	if v2["CH03"] < v1["CH03"] {
		t.Errorf("TC-H02: CH03 version went backwards: %q → %q", v1["CH03"], v2["CH03"])
	}
}

// TC-H03: Updating CH03 does not alter CH01's content.
func TestUpdateChannelConfig_OtherChannelContentUnchanged(t *testing.T) {
	svc, _, store := newTestDeviceService(t)
	deviceID := doClaimWithReg(t, svc, store, "acc-1")
	pushInitialConfig(t, svc, deviceID, "acc-1")

	// Give CH01 a specific state first.
	svc.UpdateChannelConfig(deviceID, "acc-1", "CH01", json.RawMessage(`{"state":"ACTIVE"}`))

	// Now update only CH03.
	svc.UpdateChannelConfig(deviceID, "acc-1", "CH03", json.RawMessage(`{"state":"IDLE"}`))

	// Read CH01 state from DB — it should still be ACTIVE.
	device, _ := store.GetDevice(deviceID)
	var cfg struct {
		Channels []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"channels"`
	}
	json.Unmarshal(device.DesiredConfig, &cfg)
	for _, ch := range cfg.Channels {
		if ch.ID == "CH01" && ch.State != "ACTIVE" {
			t.Errorf("TC-H03: CH01 state was mutated by CH03 update, got %q", ch.State)
		}
	}
}

// TC-H04: Device settings update bumps device version, preserves all channel versions.
func TestUpdateDeviceSettings_OnlyDeviceVersionBumped(t *testing.T) {
	svc, mqtt, store := newTestDeviceService(t)
	deviceID := doClaimWithReg(t, svc, store, "acc-1")
	pushInitialConfig(t, svc, deviceID, "acc-1")

	v0 := channelVersionsFromStore(t, store, deviceID)

	settings := json.RawMessage(`{"standardSettings":[{"id":"sync_clock_source","value":"PTP"}]}`)
	if err := svc.UpdateDeviceSettings(deviceID, "acc-1", settings); err != nil {
		t.Fatalf("UpdateDeviceSettings: %v", err)
	}

	v1 := channelVersionsFromStore(t, store, deviceID)

	for _, chID := range []string{"CH01", "CH02", "CH03", "CH04"} {
		if v1[chID] != v0[chID] {
			t.Errorf("TC-H04: %s version changed after device settings update: %q → %q", chID, v0[chID], v1[chID])
		}
	}

	// Verify device version was bumped (device settings publishes full config).
	last := mqtt.messages[len(mqtt.messages)-1]
	var env struct {
		Config struct {
			Version string `json:"version"`
		} `json:"desiredDeviceConfiguration"`
	}
	json.Unmarshal(last.Payload, &env)
	if env.Config.Version == "" {
		t.Error("TC-H04: device version is empty after UpdateDeviceSettings")
	}
}

// TC-H05: Channel update on non-existent channel → ErrNotFound.
func TestUpdateChannelConfig_UnknownChannel(t *testing.T) {
	svc, _, store := newTestDeviceService(t)
	deviceID := doClaimWithReg(t, svc, store, "acc-1")

	err := svc.UpdateChannelConfig(deviceID, "acc-1", "CH99", json.RawMessage(`{"state":"IDLE"}`))
	if err == nil {
		t.Fatal("TC-H05: expected error for unknown channel")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("TC-H05: expected not found, got %v", err)
	}
}

// TC-H06: Channel update on deprovisioned device → ErrConflict.
func TestUpdateChannelConfig_DeprovisionedDevice(t *testing.T) {
	svc, _, store := newTestDeviceService(t)
	deviceID := doClaimWithReg(t, svc, store, "acc-1")
	svc.Deprovision(deviceID, "acc-1")

	err := svc.UpdateChannelConfig(deviceID, "acc-1", "CH01", json.RawMessage(`{"state":"IDLE"}`))
	if err == nil {
		t.Fatal("TC-H06: expected error for deprovisioned device")
	}
	if !strings.Contains(err.Error(), "deprovisioned") {
		t.Errorf("TC-H06: expected deprovisioned error, got %v", err)
	}
}

// TC-H07: First channel update (no prior config) creates full config with defaults for other channels.
func TestUpdateChannelConfig_FirstUpdate_CreatesFullConfig(t *testing.T) {
	svc, mqtt, store := newTestDeviceService(t)
	deviceID := doClaimWithReg(t, svc, store, "acc-1")
	// No pushInitialConfig — no prior stored config.

	ch03cfg := json.RawMessage(`{"state":"ACTIVE"}`)
	if err := svc.UpdateChannelConfig(deviceID, "acc-1", "CH03", ch03cfg); err != nil {
		t.Fatalf("TC-H07: UpdateChannelConfig: %v", err)
	}

	last := mqtt.messages[len(mqtt.messages)-1]
	var env struct {
		Config struct {
			Channels []struct {
				ID      string `json:"id"`
				Version string `json:"version"`
			} `json:"channels"`
		} `json:"desiredDeviceConfiguration"`
	}
	json.Unmarshal(last.Payload, &env)

	found := false
	for _, ch := range env.Config.Channels {
		if ch.ID == "CH03" {
			found = true
			if ch.Version == "" {
				t.Error("TC-H07: CH03 has no version")
			}
		}
	}
	if !found {
		t.Error("TC-H07: CH03 not present in published config")
	}
}

// TC-H08: Published MQTT payload is valid envelope with desiredDeviceConfiguration.
// The MQTT payload must contain the full desired config (all channels) so the SDK
// always has the complete state. The client-side ApplicationLoop is version-gated
// and will skip channels whose version hasn't changed.
func TestUpdateChannelConfig_MQTTEnvelopeValid(t *testing.T) {
	svc, mqtt, store := newTestDeviceService(t)
	deviceID := doClaimWithReg(t, svc, store, "acc-1")
	pushInitialConfig(t, svc, deviceID, "acc-1")

	svc.UpdateChannelConfig(deviceID, "acc-1", "CH01", json.RawMessage(`{"state":"ACTIVE"}`))

	last := mqtt.messages[len(mqtt.messages)-1]
	var env map[string]interface{}
	if err := json.Unmarshal(last.Payload, &env); err != nil {
		t.Fatalf("TC-H08: invalid JSON in MQTT payload: %v", err)
	}
	if _, ok := env["desiredDeviceConfiguration"]; !ok {
		t.Error("TC-H08: MQTT payload missing desiredDeviceConfiguration key")
	}
	if _, ok := env["updateId"]; !ok {
		t.Error("TC-H08: MQTT payload missing updateId key")
	}
	if !last.Retain {
		t.Error("TC-H08: MQTT message should be retained")
	}

	// Full config check: MQTT payload must contain ALL channels so the SDK has
	// complete state. The client ApplicationLoop is version-gated and skips
	// channels whose version hasn't changed.
	var typedEnv struct {
		Config struct {
			Channels []struct {
				ID string `json:"id"`
			} `json:"channels"`
		} `json:"desiredDeviceConfiguration"`
	}
	json.Unmarshal(last.Payload, &typedEnv)
	if len(typedEnv.Config.Channels) != 4 {
		t.Errorf("TC-H08: expected 4 channels in MQTT payload (full config), got %d", len(typedEnv.Config.Channels))
	}
}

// --- Helper function tests ---

func TestGenerateDeviceID_Format(t *testing.T) {
	id := generateDeviceID()
	if len(id) != 21 {
		t.Fatalf("expected 21 chars, got %d: %q", len(id), id)
	}
}

func TestGeneratePairingCode_Format(t *testing.T) {
	code := generatePairingCode()
	if len(code) != 6 {
		t.Fatalf("expected 6 chars, got %d: %q", len(code), code)
	}
	if strings.ToUpper(code) != code {
		t.Fatalf("expected uppercase, got %q", code)
	}
}

func TestFormatOnlineDetails(t *testing.T) {
	tests := []struct {
		name     string
		device   *models.Device
		contains string
	}{
		{"offline no last seen", &models.Device{Online: false}, "offline"},
		{"offline with last seen", &models.Device{Online: false, LastSeen: "2025-01-01T00:00:00Z"}, "offline since"},
		{"online no last seen", &models.Device{Online: true}, "online"},
		{"online with last seen", &models.Device{Online: true, LastSeen: time.Now().UTC().Format(time.RFC3339)}, "online:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatOnlineDetails(tt.device)
			if !strings.Contains(result, tt.contains) {
				t.Fatalf("expected %q to contain %q", result, tt.contains)
			}
		})
	}
}

func TestFormatCertExpiration(t *testing.T) {
	if formatCertExpiration("") != "unknown" {
		t.Fatal("expected unknown for empty")
	}
	if formatCertExpiration("not-a-date") != "unknown" {
		t.Fatal("expected unknown for invalid date")
	}
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	if formatCertExpiration(past) != "expired" {
		t.Fatalf("expected expired, got %q", formatCertExpiration(past))
	}
	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	result := formatCertExpiration(future)
	if strings.Contains(result, "unknown") || strings.Contains(result, "expired") {
		t.Fatalf("expected valid duration, got %q", result)
	}
}
