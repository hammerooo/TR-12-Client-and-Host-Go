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
package utils

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	cddsdkgo "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo"
	tr12models "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/TR-12-Models/generated/tr12go"
)

// ValidatePathExistsAndWriteable checks that a directory exists and is writable.
func ValidatePathExistsAndWriteable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("path %s does not exist: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path %s is not a directory", path)
	}
	testFile := filepath.Join(path, ".write_test")
	f, err := os.Create(testFile)
	if err != nil {
		return fmt.Errorf("directory %s is not writable: %w", path, err)
	}
	f.Close()
	os.Remove(testFile)
	return nil
}

// GetHostConfiguration reads a host configuration JSON file from the embedded host_configuration directory.
func GetHostConfiguration(hostID string, basePath string) (*tr12models.GetHostConfigResponseContent, error) {
	filePath := filepath.Join(basePath, "host_configuration", hostID+".json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("unable to locate host_configuration: %s: %w", filePath, err)
	}
	var config tr12models.GetHostConfigResponseContent
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("invalid host_configuration: %s: %w", filePath, err)
	}
	return &config, nil
}

// GenerateClientKeys generates an RSA 2048-bit key pair and returns PEM-encoded public and private keys.
func GenerateClientKeys() (publicPEM, privatePEM string, err error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate private key: %w", err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal private key: %w", err)
	}
	privatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}))

	pubBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal public key: %w", err)
	}
	publicPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}))
	return publicPEM, privatePEM, nil
}

// GenerateCSR creates a Certificate Signing Request from a PEM-encoded private key.
func GenerateCSR(privateKeyPEM string) (string, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return "", fmt.Errorf("failed to decode private key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("private key is not RSA")
	}
	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			Organization: []string{"VSF-CDD"},
			Country:      []string{"US"},
		},
	}
	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, template, rsaKey)
	if err != nil {
		return "", fmt.Errorf("failed to create CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrBytes})
	return string(csrPEM), nil
}

// SSLContext creates a TLS configuration for MQTT with ALPN protocol negotiation.
func SSLContext(caCertFile, deviceCertFile, privateKeyFile, iotProtocolName string) (*tls.Config, error) {
	caCert, err := os.ReadFile(caCertFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert: %w", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA cert")
	}
	cert, err := tls.LoadX509KeyPair(deviceCertFile, privateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load device cert/key: %w", err)
	}
	// Standard TLS with mutual auth:
	//   - RootCAs anchors the server-cert chain (contains whatever the pairing
	//     service supplied as caCertificate — Amazon Root CA 1 for AWS IoT).
	//   - Certificates presents the device cert for client authentication.
	//   - NextProtos advertises the ALPN protocol supplied by the host settings
	//     (e.g. "x-amzn-mqtt-ca" for AWS IoT MQTT on 443).
	// Go's default verification uses the dialed hostname for SNI + hostname
	// checks and uses intermediate certs from the wire to build the chain to
	// a root in RootCAs. No custom VerifyPeerCertificate needed.
	return &tls.Config{
		RootCAs:      caCertPool,
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{iotProtocolName},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// UploadFile uploads a local file to a PUT URL with optional HTTP headers.
// headers may be nil for no additional headers.
func UploadFile(localPath, remotePath string, timeout int, fileType string, headers map[string]string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("failed to read %s file %s: %w", fileType, localPath, err)
	}
	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	req, err := http.NewRequest(http.MethodPut, remotePath, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create upload request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not upload %s: %w", fileType, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("upload %s failed with status %d", fileType, resp.StatusCode)
	}
	return nil
}

// Throttle implements rate limiting for publish operations.
type Throttle struct {
	mu                  sync.Mutex
	maxPublishInWindow  int
	windowSeconds       int
	publishTimes        []int64
}

// NewThrottle creates a throttle with the given minimum publish interval.
func NewThrottle(pubMinInterval int) *Throttle {
	windowSeconds := 10
	maxPub := windowSeconds / max(pubMinInterval, 1)
	if maxPub < 5 {
		maxPub = 5
	}
	return &Throttle{
		maxPublishInWindow: maxPub,
		windowSeconds:      windowSeconds,
	}
}

// CanPublish returns true if a publish is allowed under the rate limit.
func (t *Throttle) CanPublish() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now().Unix()
	cutoff := now - int64(t.windowSeconds)
	// Remove expired timestamps
	i := 0
	for i < len(t.publishTimes) && t.publishTimes[i] <= cutoff {
		i++
	}
	t.publishTimes = t.publishTimes[i:]
	if len(t.publishTimes) >= t.maxPublishInWindow {
		return false
	}
	t.publishTimes = append(t.publishTimes, now)
	return true
}

// UpdateID generates sequential update IDs with a random base.
type UpdateID struct {
	base     string
	sequence int
	mu       sync.Mutex
}

// NewUpdateID creates a new UpdateID generator.
func NewUpdateID() *UpdateID {
	b := make([]byte, 5)
	rand.Read(b)
	base := fmt.Sprintf("%x", b)
	return &UpdateID{base: base}
}

// Get returns the next update ID.
func (u *UpdateID) Get() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.sequence++
	return fmt.Sprintf("%s_%d", u.base, u.sequence)
}

// ExceptionToErrorDetails converts a Go error to an ErrorDetails model.
func ExceptionToErrorDetails(err error) *cddsdkgo.ErrorDetails {
	if err == nil {
		return nil
	}
	return &cddsdkgo.ErrorDetails{
		Type:    fmt.Sprintf("%T", err),
		Message: err.Error(),
		Details: err.Error(),
	}
}

// max returns the larger of two ints.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
