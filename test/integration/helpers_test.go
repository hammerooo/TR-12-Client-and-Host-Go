//go:build integration

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

package integration_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Port allocation
// ---------------------------------------------------------------------------

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// ---------------------------------------------------------------------------
// Repo root resolution
// ---------------------------------------------------------------------------

func repoRoot(t *testing.T) string {
	t.Helper()
	// test/integration/ -> repo root is two levels up
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine repo root")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// ---------------------------------------------------------------------------
// Binary building
// ---------------------------------------------------------------------------

func buildBinary(t *testing.T, moduleDir, outputName string, outputDir string) string {
	t.Helper()
	if outputDir == "" {
		outputDir = moduleDir
	}
	outPath := filepath.Join(outputDir, outputName)
	cmd := exec.Command("go", "build", "-o", outPath, "./cmd/"+outputName)
	cmd.Dir = moduleDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s failed: %v\n%s", outputName, err, string(out))
	}
	return outPath
}

// ---------------------------------------------------------------------------
// Process management
// ---------------------------------------------------------------------------

type process struct {
	cmd  *exec.Cmd
	name string
}

func startProcess(t *testing.T, name string, binPath string, args []string, env []string, workDir string) *process {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	p := &process{cmd: cmd, name: name}
	t.Cleanup(func() { p.stop(t) })
	return p
}

func (p *process) stop(t *testing.T) {
	t.Helper()
	if p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
	}
}

// ---------------------------------------------------------------------------
// Health check polling
// ---------------------------------------------------------------------------

func waitForHTTP(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("waitForHTTP: %s not ready after %v", url, timeout)
}

// ---------------------------------------------------------------------------
// Response types
// ---------------------------------------------------------------------------

type genericResponse struct {
	Success bool        `json:"success"`
	State   string      `json:"state"`
	Message string      `json:"message"`
	Error   interface{} `json:"error,omitempty"`
}

type connectResponse struct {
	Success     bool    `json:"success"`
	State       string  `json:"state"`
	Message     string  `json:"message"`
	PairingCode string  `json:"pairingCode,omitempty"`
	Expires     float64 `json:"expires,omitempty"`
	DeviceID    string  `json:"deviceId,omitempty"`
	Region      string  `json:"region,omitempty"`
}

type accountInner struct {
	AccountID   string `json:"account_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

type accountResponse struct {
	Account *accountInner `json:"account"`
	Token   string        `json:"token"`
}

type deviceSummary struct {
	DeviceID      string `json:"device_id"`
	Message       string `json:"message"`
	Online        bool   `json:"online"`
	OnlineDetails string `json:"online_details"`
}

type deviceMetadata struct {
	Online         bool   `json:"online"`
	OnlineDetails  string `json:"online_details"`
	CertExpiration string `json:"cert_expiration"`
	SourceIP       string `json:"source_ip"`
	DeviceType     string `json:"device_type"`
	AccountID      string `json:"account_id"`
	PairedAt       string `json:"paired_at"`
}

type deviceDetail struct {
	DeviceID            string          `json:"device_id"`
	Message             string          `json:"message"`
	Errors              []string        `json:"errors"`
	Registration        json.RawMessage `json:"registration"`
	Configuration       json.RawMessage `json:"configuration"`
	ActualConfiguration json.RawMessage `json:"actual_configuration"`
	Status              json.RawMessage `json:"status"`
	Online              bool            `json:"online"`
	OnlineDetails       string          `json:"online_details"`
	CertExpiration      string          `json:"cert_expiration"`
	DeviceMetadata      deviceMetadata  `json:"device_metadata"`
}

type thumbnailResponse struct {
	Message string         `json:"message"`
	Image   *thumbnailImage `json:"image,omitempty"`
}

type thumbnailImage struct {
	Base64Image string `json:"base64_image"`
	Timestamp   string `json:"timestamp"`
	ImageType   string `json:"image_type"`
	MaxSizeKB   int    `json:"max_size_KB"`
	ImageSizeKB int    `json:"image_size_KB"`
}

type configResponse struct {
	Success       bool                   `json:"success"`
	State         string                 `json:"state"`
	Message       string                 `json:"message"`
	Configuration map[string]interface{} `json:"configuration,omitempty"`
	UpdateID      string                 `json:"updateId,omitempty"`
}

type hostUpdateResponse struct {
	DeviceID string `json:"device_id"`
	Message  string `json:"message"`
	Error    string `json:"error"`
}

type hostErrorResponse struct {
	Error string `json:"error"`
	Code  int    `json:"code"`
}

// ---------------------------------------------------------------------------
// Test environment — holds ports, paths, and running processes
// ---------------------------------------------------------------------------

type testEnv struct {
	t            *testing.T
	root         string // repo root
	hostHTTPPort int
	hostMQTTPort int
	sdkPort      int
	dbPath       string
	certsPath    string
	logPath      string
	sdkWorkDir   string // temp dir with host_configuration/ for the SDK binary
	hostBin      string
	sdkBin       string
	hostProc     *process
	sdkProc      *process
	hostURL      string
	sdkURL       string
	httpClient   *http.Client
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	root := repoRoot(t)

	httpPort := freePort(t)
	mqttPort := freePort(t)
	sdkPort := freePort(t)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	certsPath := filepath.Join(tmpDir, "certs")
	logPath := filepath.Join(tmpDir, "logs")
	os.MkdirAll(certsPath, 0755)
	os.MkdirAll(logPath, 0755)

	// Create a temp working directory for the SDK with a host_configuration/ folder.
	// Must be created before building the SDK binary so we can place the binary here.
	sdkWorkDir := filepath.Join(tmpDir, "sdk-work")
	hostCfgDir := filepath.Join(sdkWorkDir, "host_configuration")
	os.MkdirAll(hostCfgDir, 0755)

	// Write a dynamic host config pointing to our ephemeral ports
	hostCfg := fmt.Sprintf(`{
  "serviceId": "tr12-host",
  "serviceName": "Integration Test Host",
  "deviceTypes": ["SOURCE", "DESTINATION", "BOTH"],
  "createPairingCodeUrl": "http://127.0.0.1:%d/pair",
  "authenticatePairingCodeUrl": "http://127.0.0.1:%d/authenticate",
  "thumbnailMaximumSizeKB": 100,
  "logFileMaximumSizeKB": 500
}`, httpPort, httpPort)
	os.WriteFile(filepath.Join(hostCfgDir, "tr12-host.json"), []byte(hostCfg), 0644)

	// Build binaries (or use env overrides).
	// SDK binary is built into sdkWorkDir so os.Executable() resolves basePath
	// to sdkWorkDir, where host_configuration/ lives.
	hostBin := os.Getenv("TR12_HOST_BINARY")
	if hostBin == "" {
		hostBin = buildBinary(t, filepath.Join(root, "host"), "tr12-host", "")
	}
	sdkBin := os.Getenv("TR12_SDK_BINARY")
	if sdkBin == "" {
		sdkBin = buildBinary(t, filepath.Join(root, "client"), "cdd-sdk", sdkWorkDir)
	}

	return &testEnv{
		t:            t,
		root:         root,
		hostHTTPPort: httpPort,
		hostMQTTPort: mqttPort,
		sdkPort:      sdkPort,
		dbPath:       dbPath,
		certsPath:    certsPath,
		logPath:      logPath,
		sdkWorkDir:   sdkWorkDir,
		hostBin:      hostBin,
		sdkBin:       sdkBin,
		hostURL:      fmt.Sprintf("http://127.0.0.1:%d", httpPort),
		sdkURL:       fmt.Sprintf("http://127.0.0.1:%d", sdkPort),
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (e *testEnv) startHost() {
	e.t.Helper()
	args := []string{
		"--host-address", "127.0.0.1",
		"--http-port", fmt.Sprintf("%d", e.hostHTTPPort),
		"--mqtt-port", fmt.Sprintf("%d", e.hostMQTTPort),
		"--db-path", e.dbPath,
		"--service-id", "tr12-host",
		"--pairing-timeout", "300",
		"--cert-expiry-days", "30",
	}
	e.hostProc = startProcess(e.t, "host", e.hostBin, args, nil, e.root)
	waitForHTTP(e.t, e.hostURL+"/host-config", 10*time.Second)
	e.t.Log("Host service ready")
}

func (e *testEnv) startSDK(deviceID string) {
	e.t.Helper()
	args := []string{
		"--internal_device_id", deviceID,
		"--certs_path", e.certsPath,
		"--log_path", e.logPath,
		"--ip", "127.0.0.1",
		"--port", fmt.Sprintf("%d", e.sdkPort),
		"--device_type", "SOURCE",
	}
	e.sdkProc = startProcess(e.t, "sdk", e.sdkBin, args, nil, e.sdkWorkDir)
	waitForHTTP(e.t, e.sdkURL+"/get_state", 10*time.Second)
	e.t.Log("SDK ready")
}

// ---------------------------------------------------------------------------
// SDK API helpers
// ---------------------------------------------------------------------------

func (e *testEnv) sdkConnect(hostID string, registration map[string]interface{}) connectResponse {
	e.t.Helper()
	body := map[string]interface{}{
		"hostId":       hostID,
		"registration": registration,
	}
	var resp connectResponse
	e.doPut(e.sdkURL+"/connect", body, &resp, 0)
	return resp
}

func (e *testEnv) sdkGetState() genericResponse {
	e.t.Helper()
	var resp genericResponse
	e.doGet(e.sdkURL+"/get_state", "", &resp, 0)
	return resp
}

func (e *testEnv) sdkGetConfiguration() configResponse {
	e.t.Helper()
	var resp configResponse
	e.doGet(e.sdkURL+"/get_configuration", "", &resp, 0)
	return resp
}

func (e *testEnv) sdkReportStatus(status map[string]interface{}) genericResponse {
	e.t.Helper()
	body := map[string]interface{}{"status": status}
	var resp genericResponse
	e.doPut(e.sdkURL+"/report_status", body, &resp, 0)
	return resp
}

func (e *testEnv) sdkReportActualConfig(config map[string]interface{}) genericResponse {
	e.t.Helper()
	body := map[string]interface{}{"configuration": config}
	var resp genericResponse
	e.doPut(e.sdkURL+"/report_actual_configuration", body, &resp, 0)
	return resp
}

func (e *testEnv) sdkDisconnect() genericResponse {
	e.t.Helper()
	var resp genericResponse
	e.doPut(e.sdkURL+"/disconnect", nil, &resp, 0)
	return resp
}

func (e *testEnv) sdkDeprovision(hostID string) genericResponse {
	e.t.Helper()
	body := map[string]interface{}{"hostId": hostID}
	var resp genericResponse
	e.doPut(e.sdkURL+"/deprovision", body, &resp, 0)
	return resp
}

func (e *testEnv) sdkRegister(registration map[string]interface{}) genericResponse {
	e.t.Helper()
	body := map[string]interface{}{"registration": registration}
	var resp genericResponse
	e.doPut(e.sdkURL+"/register", body, &resp, 0)
	return resp
}

// ---------------------------------------------------------------------------
// Host API helpers
// ---------------------------------------------------------------------------

func (e *testEnv) hostRegisterAccount(username, password, displayName string) accountResponse {
	e.t.Helper()
	body := map[string]interface{}{
		"username":     username,
		"password":     password,
		"display_name": displayName,
	}
	var resp accountResponse
	e.doPost(e.hostURL+"/account/register", body, &resp, 0)
	return resp
}

func (e *testEnv) hostClaim(pairingCode, token string) int {
	e.t.Helper()
	url := fmt.Sprintf("%s/authorize/%s", e.hostURL, pairingCode)
	req, _ := http.NewRequest("PUT", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("hostClaim: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (e *testEnv) hostListDevices(token string) []deviceSummary {
	e.t.Helper()
	var devices []deviceSummary
	e.doGet(e.hostURL+"/devices", token, &devices, 0)
	return devices
}

func (e *testEnv) hostDescribeDevice(deviceID, token string) deviceDetail {
	e.t.Helper()
	var detail deviceDetail
	e.doGet(e.hostURL+"/device/"+deviceID, token, &detail, 0)
	return detail
}

// hostUpdateConfig sends a PUT /device/{id} and returns (statusCode, responseBody).
func (e *testEnv) hostUpdateConfig(deviceID, token string, config json.RawMessage) (int, string) {
	e.t.Helper()
	url := fmt.Sprintf("%s/device/%s", e.hostURL, deviceID)
	req, _ := http.NewRequest("PUT", url, bytes.NewReader(config))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("hostUpdateConfig: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(bodyBytes)
}

func (e *testEnv) hostGetThumbnail(deviceID, channelID, token string) (int, thumbnailResponse) {
	e.t.Helper()
	url := fmt.Sprintf("%s/thumbnail/%s?channel=%s", e.hostURL, deviceID, channelID)
	var thumb thumbnailResponse
	code := e.doGetRaw(url, token, &thumb)
	return code, thumb
}

func (e *testEnv) hostRotateCredentials(deviceID, token string) int {
	e.t.Helper()
	url := fmt.Sprintf("%s/credentials/%s", e.hostURL, deviceID)
	req, _ := http.NewRequest("PUT", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("hostRotateCredentials: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (e *testEnv) hostDeprovision(deviceID, token string) int {
	e.t.Helper()
	url := fmt.Sprintf("%s/deprovision/%s", e.hostURL, deviceID)
	req, _ := http.NewRequest("PUT", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("hostDeprovision: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// ---------------------------------------------------------------------------
// Polling helpers
// ---------------------------------------------------------------------------

func (e *testEnv) waitForSDKState(desired string, timeout time.Duration) {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state := e.sdkGetState()
		if strings.EqualFold(state.State, desired) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	e.t.Fatalf("SDK did not reach state %q within %v", desired, timeout)
}

// waitForSDKConnected drives the SDK state machine by calling PUT /connect
// while in PAIRING state (which triggers AuthenticatePairingCode), and
// polls GET /get_state for other transitional states until CONNECTED.
func (e *testEnv) waitForSDKConnected(hostID string, registration map[string]interface{}, timeout time.Duration) {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state := e.sdkGetState()
		switch strings.ToUpper(state.State) {
		case "CONNECTED":
			return
		case "PAIRING":
			// Drive the state machine — each /connect call triggers handlePairingState
			e.sdkConnect(hostID, registration)
		case "CONNECTING", "RECONNECTING":
			// Transitional — just wait
		default:
			// DISCONNECTED or unexpected — try /connect to re-enter the state machine
			e.sdkConnect(hostID, registration)
		}
		time.Sleep(500 * time.Millisecond)
	}
	finalState := e.sdkGetState()
	e.t.Fatalf("SDK did not reach CONNECTED within %v (last state: %s)", timeout, finalState.State)
}

func (e *testEnv) waitForPairingCode(hostID string, registration map[string]interface{}, timeout time.Duration) string {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp := e.sdkConnect(hostID, registration)
		if resp.PairingCode != "" {
			return resp.PairingCode
		}
		time.Sleep(500 * time.Millisecond)
	}
	e.t.Fatalf("SDK did not return a pairing code within %v", timeout)
	return ""
}

// ---------------------------------------------------------------------------
// Low-level HTTP helpers
// ---------------------------------------------------------------------------

func (e *testEnv) doGet(url, token string, out interface{}, expectedStatus int) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if expectedStatus > 0 && resp.StatusCode != expectedStatus {
		body, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("GET %s: expected %d, got %d: %s", url, expectedStatus, resp.StatusCode, string(body))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			e.t.Fatalf("GET %s: decode: %v", url, err)
		}
	}
}

func (e *testEnv) doGetRaw(url, token string, out interface{}) int {
	e.t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (e *testEnv) doPost(url string, body interface{}, out interface{}, expectedStatus int) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequest("POST", url, r)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if expectedStatus > 0 && resp.StatusCode != expectedStatus {
		respBody, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("POST %s: expected %d, got %d: %s", url, expectedStatus, resp.StatusCode, string(respBody))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			e.t.Fatalf("POST %s: decode: %v", url, err)
		}
	}
}

func (e *testEnv) doPut(url string, body interface{}, out interface{}, expectedStatus int) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequest("PUT", url, r)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("PUT %s: %v", url, err)
	}
	defer resp.Body.Close()
	if expectedStatus > 0 && resp.StatusCode != expectedStatus {
		respBody, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("PUT %s: expected %d, got %d: %s", url, expectedStatus, resp.StatusCode, string(respBody))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			e.t.Fatalf("PUT %s: decode: %v", url, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Test data helpers
// ---------------------------------------------------------------------------

func loadRegistration(t *testing.T) map[string]interface{} {
	return loadRegistrationFrom(t, "")
}

func loadRegistrationFrom(t *testing.T, subdir string) map[string]interface{} {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	var path string
	if subdir == "" {
		path = filepath.Join(filepath.Dir(thisFile), "testdata", "registration.json")
	} else {
		path = filepath.Join(filepath.Dir(thisFile), "testdata", subdir, "registration.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("load registration.json from %s: %v", path, err)
	}
	var reg map[string]interface{}
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("parse registration.json: %v", err)
	}
	return reg
}

// createTestJPEG writes a minimal valid JPEG file to the given path.
func createTestJPEG(t *testing.T, path string) {
	t.Helper()
	// Minimal valid JPEG: SOI + APP0 (JFIF) + SOF0 + SOS + EOI
	// This is a 1x1 white pixel JPEG.
	jpeg := []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01,
		0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xFF, 0xDB, 0x00, 0x43,
		0x00, 0x08, 0x06, 0x06, 0x07, 0x06, 0x05, 0x08, 0x07, 0x07, 0x07, 0x09,
		0x09, 0x08, 0x0A, 0x0C, 0x14, 0x0D, 0x0C, 0x0B, 0x0B, 0x0C, 0x19, 0x12,
		0x13, 0x0F, 0x14, 0x1D, 0x1A, 0x1F, 0x1E, 0x1D, 0x1A, 0x1C, 0x1C, 0x20,
		0x24, 0x2E, 0x27, 0x20, 0x22, 0x2C, 0x23, 0x1C, 0x1C, 0x28, 0x37, 0x29,
		0x2C, 0x30, 0x31, 0x34, 0x34, 0x34, 0x1F, 0x27, 0x39, 0x3D, 0x38, 0x32,
		0x3C, 0x2E, 0x33, 0x34, 0x32, 0xFF, 0xC0, 0x00, 0x0B, 0x08, 0x00, 0x01,
		0x00, 0x01, 0x01, 0x01, 0x11, 0x00, 0xFF, 0xC4, 0x00, 0x1F, 0x00, 0x00,
		0x01, 0x05, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0xFF, 0xC4, 0x00, 0xB5, 0x10, 0x00, 0x02, 0x01, 0x03,
		0x03, 0x02, 0x04, 0x03, 0x05, 0x05, 0x04, 0x04, 0x00, 0x00, 0x01, 0x7D,
		0x01, 0x02, 0x03, 0x00, 0x04, 0x11, 0x05, 0x12, 0x21, 0x31, 0x41, 0x06,
		0x13, 0x51, 0x61, 0x07, 0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xA1, 0x08,
		0x23, 0x42, 0xB1, 0xC1, 0x15, 0x52, 0xD1, 0xF0, 0x24, 0x33, 0x62, 0x72,
		0x82, 0x09, 0x0A, 0x16, 0x17, 0x18, 0x19, 0x1A, 0x25, 0x26, 0x27, 0x28,
		0x29, 0x2A, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3A, 0x43, 0x44, 0x45,
		0x46, 0x47, 0x48, 0x49, 0x4A, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59,
		0x5A, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6A, 0x73, 0x74, 0x75,
		0x76, 0x77, 0x78, 0x79, 0x7A, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89,
		0x8A, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9A, 0xA2, 0xA3,
		0xA4, 0xA5, 0xA6, 0xA7, 0xA8, 0xA9, 0xAA, 0xB2, 0xB3, 0xB4, 0xB5, 0xB6,
		0xB7, 0xB8, 0xB9, 0xBA, 0xC2, 0xC3, 0xC4, 0xC5, 0xC6, 0xC7, 0xC8, 0xC9,
		0xCA, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7, 0xD8, 0xD9, 0xDA, 0xE1, 0xE2,
		0xE3, 0xE4, 0xE5, 0xE6, 0xE7, 0xE8, 0xE9, 0xEA, 0xF1, 0xF2, 0xF3, 0xF4,
		0xF5, 0xF6, 0xF7, 0xF8, 0xF9, 0xFA, 0xFF, 0xDA, 0x00, 0x08, 0x01, 0x01,
		0x00, 0x00, 0x3F, 0x00, 0x7B, 0x94, 0x11, 0x00, 0x00, 0x00, 0x00, 0xFF,
		0xD9,
	}
	if err := os.WriteFile(path, jpeg, 0644); err != nil {
		t.Fatalf("createTestJPEG: %v", err)
	}
}

// doPostRaw posts to url and returns the HTTP status code, decoding the response into out.
func (e *testEnv) doPostRaw(url string, body interface{}, out interface{}) int {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequest("POST", url, r)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// generateMinimalCSR returns a PEM-encoded CSR for use in pairing rejection tests.
func generateMinimalCSR(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generateMinimalCSR: generate key: %v", err)
	}
	template := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "test-device"},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		t.Fatalf("generateMinimalCSR: create CSR: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}))
}

// hostUpdateChannelConfig sends a per-channel config update via
// PUT /device/:deviceId/channel/:channelId.
func (e *testEnv) hostUpdateChannelConfig(deviceID, channelID, token string, config json.RawMessage) (int, string) {
	e.t.Helper()
	url := fmt.Sprintf("%s/device/%s/channel/%s", e.hostURL, deviceID, channelID)
	req, _ := http.NewRequest("PUT", url, bytes.NewReader(config))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("hostUpdateChannelConfig: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(bodyBytes)
}
