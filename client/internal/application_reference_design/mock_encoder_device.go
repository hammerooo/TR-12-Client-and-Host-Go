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
// Simple encoder simulation — mirrors the Python ARD's simple_encoder.py.
// Manages per-channel ffmpeg subprocesses for SRT streaming.
package application_reference_design

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	cddsdkgo "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo"
)

// FfmpegPath can be overridden for different systems.
var FfmpegPath = "/opt/homebrew/bin/ffmpeg"

// channelState holds per-channel ffmpeg process, SRT config, and health.
type channelState struct {
	process            *exec.Cmd
	srtIP              string
	srtPort            int
	srtStreamID        string
	srtMinLatencyMs    int
	srtEncryption      *cddsdkgo.SrtEncryption
	health             *cddsdkgo.Health
}

// Encoder manages per-channel ffmpeg SRT caller processes and device/channel settings.
type Encoder struct {
	mu              sync.Mutex
	channels        map[string]*channelState   // per-channel process + SRT state
	deviceSettings  map[string]string
	channelSettings map[string]map[string]string // channelID -> key -> value
	channelProfiles map[string]string            // channelID -> active profile ID

	// simulateFailureKeys contains setting keys (and the special key "__start_stop__")
	// that will fail when applied. This is used exclusively by tests to simulate
	// device-side failures so the health reporting path can be exercised.
	// Real device integrations should remove this field and use actual API error returns.
	simulateFailureKeys map[string]bool
}

// NewEncoder creates a new encoder instance with default settings.
// Channel settings are initialized lazily when first set.
func NewEncoder() *Encoder {
	return &Encoder{
		deviceSettings:      map[string]string{"sync_clock_source": "NTP"},
		channelSettings:     make(map[string]map[string]string),
		channelProfiles:     make(map[string]string),
		channels:            make(map[string]*channelState),
		simulateFailureKeys: make(map[string]bool),
	}
}

// SimulateFailure marks a setting key (or "__start_stop__" for channel start/stop)
// as failing. Subsequent calls to SetDeviceSetting, SetChannelSetting, or
// HandleUpdateState for that key will return an error string instead of applying.
// This is the test hook that exercises the health reporting path — see TestHealthReporting.
//
// Device integrators: replace this pattern with real error returns from your native API.
func (e *Encoder) SimulateFailure(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.simulateFailureKeys[key] = true
}

// ClearFailure removes a simulated failure for the given key.
func (e *Encoder) ClearFailure(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.simulateFailureKeys, key)
}

func (e *Encoder) getOrCreateChannel(channelID string) *channelState {
	if e.channels[channelID] == nil {
		e.channels[channelID] = &channelState{}
	}
	return e.channels[channelID]
}

// RunningChannel returns true if the given channel's ffmpeg process is active.
func (e *Encoder) RunningChannel(channelID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch, ok := e.channels[channelID]
	if !ok || ch.process == nil {
		return false
	}
	if ch.process.ProcessState != nil {
		return false
	}
	if ch.process.Process != nil {
		return ch.process.Process.Signal(syscall.Signal(0)) == nil
	}
	return false
}

// Running returns true if ANY channel's ffmpeg process is active.
func (e *Encoder) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ch := range e.channels {
		if ch.process != nil && ch.process.ProcessState == nil && ch.process.Process != nil {
			if ch.process.Process.Signal(syscall.Signal(0)) == nil {
				return true
			}
		}
	}
	return false
}

// StartChannel launches ffmpeg for the given channel.
func (e *Encoder) StartChannel(channelID, ip string, port int, streamID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch := e.getOrCreateChannel(channelID)

	if ch.process != nil && ch.process.Process != nil {
		if ch.process.Process.Signal(syscall.Signal(0)) == nil {
			fmt.Printf("[%s] Already running\n", channelID)
			return
		}
	}

	fmt.Printf("[%s] ************* Starting *****************\n", channelID)
	srtURL := fmt.Sprintf("srt://%s:%d/%s", ip, port, streamID)
	redactedURL := srtURL
	if ch.srtEncryption != nil {
		query := fmt.Sprintf("passphrase=%s", ch.srtEncryption.Passphrase)
		redactedQuery := "passphrase=REDACTED"
		if keyLen := pbKeyLen(ch.srtEncryption.KeyLength); keyLen > 0 {
			query += fmt.Sprintf("&pbkeylen=%d", keyLen)
			redactedQuery += fmt.Sprintf("&pbkeylen=%d", keyLen)
		}
		srtURL += "?" + query
		redactedURL += "?" + redactedQuery
	}
	cmd := exec.Command(FfmpegPath,
		"-f", "avfoundation", "-framerate", "30", "-video_size", "640x480",
		"-i", "0", "-vcodec", "libx264", "-f", "mpegts", srtURL,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	redactedArgs := append(append([]string{}, cmd.Args[1:len(cmd.Args)-1]...), redactedURL)
	fmt.Printf("[%s] command: %s %v\n", channelID, FfmpegPath, redactedArgs)
	if err := cmd.Start(); err != nil {
		fmt.Printf("[%s] Failed to start ffmpeg: %v\n", channelID, err)
		return
	}
	ch.process = cmd
	ch.srtIP = ip
	ch.srtPort = port
	ch.srtStreamID = streamID
	go func() { _ = cmd.Wait() }()
}

// StopChannel terminates the ffmpeg process for the given channel.
func (e *Encoder) StopChannel(channelID string) {
	e.mu.Lock()
	ch, ok := e.channels[channelID]
	if !ok || ch.process == nil || ch.process.Process == nil {
		e.mu.Unlock()
		fmt.Printf("[%s] Already stopped\n", channelID)
		return
	}
	proc := ch.process
	ch.process = nil
	e.mu.Unlock()

	fmt.Printf("[%s] ************* Stopping *****************\n", channelID)
	if err := proc.Process.Signal(syscall.SIGINT); err != nil {
		fmt.Printf("[%s] SIGINT failed: %v\n", channelID, err)
	} else {
		fmt.Printf("[%s] Sent SIGINT to process %d\n", channelID, proc.Process.Pid)
	}

	// Wait outside the lock so other channels can proceed.
	time.Sleep(3 * time.Second)
	if proc.Process.Signal(syscall.Signal(0)) == nil {
		fmt.Printf("[%s] Process didn't respond to SIGINT, trying SIGTERM...\n", channelID)
		_ = proc.Process.Signal(syscall.SIGTERM)
		time.Sleep(2 * time.Second)
	}
}

// Stop stops all channel processes (used on shutdown).
func (e *Encoder) Stop() {
	e.mu.Lock()
	channelIDs := make([]string, 0, len(e.channels))
	for id := range e.channels {
		channelIDs = append(channelIDs, id)
	}
	e.mu.Unlock()
	for _, id := range channelIDs {
		e.StopChannel(id)
	}
}

// SetDeviceSetting stores a device-level setting.
// Returns an error string if a simulated failure is active for this key.
func (e *Encoder) SetDeviceSetting(key, value string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.simulateFailureKeys[key] {
		return fmt.Sprintf("simulated device failure: cannot apply setting %q=%q", key, value)
	}
	e.deviceSettings[key] = value
	return ""
}

// GetDeviceSetting returns a device-level setting value.
func (e *Encoder) GetDeviceSetting(key string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.deviceSettings[key]
	return v, ok
}

// SetChannelSetting stores a channel-level simple setting.
// Returns an error string if a simulated failure is active for this key.
func (e *Encoder) SetChannelSetting(channelID, key, value string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.simulateFailureKeys[key] {
		return fmt.Sprintf("simulated device failure: cannot apply channel %s setting %q=%q", channelID, key, value)
	}
	if e.channelSettings[channelID] == nil {
		e.channelSettings[channelID] = make(map[string]string)
	}
	e.channelSettings[channelID][key] = value
	return ""
}

// GetChannelSetting returns a channel-level simple setting value.
func (e *Encoder) GetChannelSetting(channelID, key string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ch, ok := e.channelSettings[channelID]; ok {
		v, ok := ch[key]
		return v, ok
	}
	return "", false
}

// SetChannelProfile stores the active profile ID for a channel.
// Simulates the device "accepting" a profile selection.
// A real device integration would call the native API to apply the profile here.
func (e *Encoder) SetChannelProfile(channelID, profileID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.channelProfiles[channelID] = profileID
}

// GetChannelProfile returns the active profile ID for a channel, or ("", false) if none set.
func (e *Encoder) GetChannelProfile(channelID string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id, ok := e.channelProfiles[channelID]
	return id, ok && id != ""
}

// HandleTransportConfigChange stores SRT config for the given channel.
func (e *Encoder) HandleTransportConfigChange(channelID string, protocol *cddsdkgo.TransportProtocol) {
	if protocol == nil {
		fmt.Printf("[%s] Unsupported transport protocol format\n", channelID)
		return
	}
	if protocol.SrtCaller == nil {
		fmt.Printf("[%s] No srtCaller in transport protocol — stopping channel\n", channelID)
		e.StopChannel(channelID)
		return
	}
	srt := protocol.SrtCaller.SrtCaller
	ip := srt.Address
	port := int(srt.Port)
	streamID := srt.GetStreamId()
	latencyMs := int(srt.GetMinimumLatencyMilliseconds())
	encryption := srt.Encryption
	fmt.Printf("[%s] Got SRT config: ip=%s port=%d streamId=%s latencyMs=%d encryption=%s\n",
		channelID, ip, port, streamID, latencyMs, describeEncryption(encryption))
	e.mu.Lock()
	ch := e.getOrCreateChannel(channelID)
	ch.srtIP = ip
	ch.srtPort = port
	ch.srtStreamID = streamID
	ch.srtMinLatencyMs = latencyMs
	// A desired config without encryption must not leave a stale passphrase behind.
	if encryption != nil {
		encCopy := *encryption
		ch.srtEncryption = &encCopy
	} else {
		ch.srtEncryption = nil
	}
	e.mu.Unlock()
}

// describeEncryption returns a log-safe description of an SRT encryption config
func describeEncryption(enc *cddsdkgo.SrtEncryption) string {
	if enc == nil {
		return "none"
	}
	keyLength := "device-default"
	if enc.KeyLength != nil {
		keyLength = string(*enc.KeyLength)
	}
	return fmt.Sprintf("enabled (passphrase=REDACTED, keyLength=%s)", keyLength)
}

// HandleUpdateState processes a channel state change (ACTIVE/IDLE).
// Returns an error string if a simulated start/stop failure is active.
func (e *Encoder) HandleUpdateState(channelID string, state cddsdkgo.ChannelState) string {
	if func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.simulateFailureKeys["__start_stop__"]
	}() {
		return fmt.Sprintf("simulated device failure: cannot change channel %s state to %s", channelID, state)
	}
	switch state {
	case cddsdkgo.CHANNELSTATE_IDLE:
		fmt.Printf("[%s] Calling stop\n", channelID)
		e.StopChannel(channelID)
	case cddsdkgo.CHANNELSTATE_ACTIVE:
		e.mu.Lock()
		ch := e.getOrCreateChannel(channelID)
		ip, port, streamID := ch.srtIP, ch.srtPort, ch.srtStreamID
		e.mu.Unlock()
		if ip != "" && port > 0 {
			if e.RunningChannel(channelID) {
				fmt.Printf("[%s] Restarting with updated settings\n", channelID)
				e.StopChannel(channelID)
			}
			fmt.Printf("[%s] Calling Start\n", channelID)
			e.StartChannel(channelID, ip, port, streamID)
		} else {
			fmt.Printf("[%s] Cannot start: no SRT config available\n", channelID)
		}
	}
	return ""
}

// pbKeyLen maps a TR-12 SRT key length to the SRT pbkeylen value in bytes. Returns 0 when unset.
func pbKeyLen(keyLength *cddsdkgo.SrtEncryptionKeyLength) int {
	if keyLength == nil {
		return 0
	}
	switch *keyLength {
	case cddsdkgo.SRTENCRYPTIONKEYLENGTH_AES_128:
		return 16
	case cddsdkgo.SRTENCRYPTIONKEYLENGTH_AES_192:
		return 24
	case cddsdkgo.SRTENCRYPTIONKEYLENGTH_AES_256:
		return 32
	}
	return 0
}

// GetChannelConnection returns the current SRT connection config for the given channel.
func (e *Encoder) GetChannelConnection(channelID string) *cddsdkgo.TransportProtocol {
	e.mu.Lock()
	ch := e.getOrCreateChannel(channelID)
	ip, port, streamID, latencyMs := ch.srtIP, ch.srtPort, ch.srtStreamID, ch.srtMinLatencyMs
	var encryption *cddsdkgo.SrtEncryption
	if ch.srtEncryption != nil {
		encCopy := *ch.srtEncryption
		encryption = &encCopy
	}
	e.mu.Unlock()

	if ip == "" {
		ip = "127.0.0.1"
	}
	if port == 0 {
		port = 5000
	}
	if streamID == "" {
		streamID = "test_stream"
	}
	if latencyMs == 0 {
		latencyMs = 200
	}

	srtProto := cddsdkgo.SrtCallerTransportProtocol{
		Address: ip,
		Port:    float32(port),
	}
	srtProto.StreamId = &streamID
	latencyF := float32(latencyMs)
	srtProto.MinimumLatencyMilliseconds = &latencyF
	srtProto.Encryption = encryption
	tp := cddsdkgo.SrtCallerAsTransportProtocol(cddsdkgo.NewSrtCaller(srtProto))
	return &tp
}

// SetChannelHealth stores health state for a channel.
// level: "DEGRADED" or "CRITICAL"
func (e *Encoder) SetChannelHealth(channelID string, level string, messages []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch := e.getOrCreateChannel(channelID)
	now := time.Now().UTC()
	msg := strings.Join(messages, "; ")
	const maxHealthMessageLen = 128
	if len(msg) > maxHealthMessageLen {
		msg = msg[:maxHealthMessageLen]
	}
	errVal := cddsdkgo.HealthError{Message: msg, Timestamp: now}
	var h cddsdkgo.Health
	if level == "CRITICAL" {
		h = cddsdkgo.CriticalAsHealth(cddsdkgo.NewCritical(errVal))
	} else {
		h = cddsdkgo.DegradedAsHealth(cddsdkgo.NewDegraded(errVal))
	}
	ch.health = &h
}

// ClearChannelHealth resets a channel back to HEALTHY.
func (e *Encoder) ClearChannelHealth(channelID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch := e.getOrCreateChannel(channelID)
	ch.health = nil
}

// GetChannelHealth returns the current health for a channel (HEALTHY if none set).
func (e *Encoder) GetChannelHealth(channelID string) *cddsdkgo.Health {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch, ok := e.channels[channelID]
	if !ok || ch.health == nil {
		h := cddsdkgo.HealthyAsHealth(cddsdkgo.NewHealthy(map[string]interface{}{}))
		return &h
	}
	return ch.health
}

// GetChannelState returns ACTIVE or IDLE for the given channel.
func (e *Encoder) GetChannelState(channelID string) cddsdkgo.ChannelState {
	if e.RunningChannel(channelID) {
		return cddsdkgo.CHANNELSTATE_ACTIVE
	}
	return cddsdkgo.CHANNELSTATE_IDLE
}

// GetSimulatedBitrate returns a fake bitrate value.
func GetSimulatedBitrate() string {
	ms := time.Now().UnixMilli()
	return fmt.Sprintf("%d", int(math.Mod(float64(ms), 10000))+20000)
}

// inputThumbnailPaths maps selected_input values to thumbnail image paths.
var inputThumbnailPaths = map[string]string{
	"SDI1":  "/tmp/image_sdi.jpg",
	"HDMI1": "/tmp/image_hdmi.jpg",
}

// GetChannelThumbnailLocalPath returns the thumbnail path for a channel based
// on its current selected_input setting. Defaults to SDI1 if no config yet.
func (e *Encoder) GetChannelThumbnailLocalPath(channelID string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	selectedInput := "SDI1"
	if settings, ok := e.channelSettings[channelID]; ok {
		if v, ok := settings["IN01"]; ok {
			selectedInput = v
		}
	}
	path, ok := inputThumbnailPaths[selectedInput]
	return path, ok
}
