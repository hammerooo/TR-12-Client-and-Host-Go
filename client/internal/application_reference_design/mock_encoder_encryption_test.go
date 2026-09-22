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

func buildSrtCallerProtocol(encryption *cddsdkgo.SrtEncryption) *cddsdkgo.TransportProtocol {
	srtProto := cddsdkgo.SrtCallerTransportProtocol{
		Address: "192.168.1.50",
		Port:    9000,
	}
	streamID := "stream1"
	srtProto.StreamId = &streamID
	srtProto.Encryption = encryption
	tp := cddsdkgo.SrtCallerAsTransportProtocol(cddsdkgo.NewSrtCaller(srtProto))
	return &tp
}

func srtCallerFromProtocol(t *testing.T, tp *cddsdkgo.TransportProtocol) cddsdkgo.SrtCallerTransportProtocol {
	t.Helper()
	if tp == nil || tp.SrtCaller == nil {
		t.Fatal("expected srtCaller transport protocol")
	}
	return tp.SrtCaller.SrtCaller
}

func TestEncryptionStoredAndReadBack(t *testing.T) {
	enc := NewEncoder()
	keyLen := cddsdkgo.SRTENCRYPTIONKEYLENGTH_AES_256
	desired := &cddsdkgo.SrtEncryption{Passphrase: "0123456789passphrase", KeyLength: &keyLen}

	enc.HandleTransportConfigChange("CH01", buildSrtCallerProtocol(desired))

	got := srtCallerFromProtocol(t, enc.GetChannelConnection("CH01"))
	if got.Encryption == nil {
		t.Fatal("expected encryption in read-back connection")
	}
	if got.Encryption.Passphrase != "0123456789passphrase" {
		t.Errorf("passphrase: got %q, want %q", got.Encryption.Passphrase, "0123456789passphrase")
	}
	if got.Encryption.KeyLength == nil || *got.Encryption.KeyLength != cddsdkgo.SRTENCRYPTIONKEYLENGTH_AES_256 {
		t.Errorf("keyLength: got %v, want AES_256", got.Encryption.KeyLength)
	}
}

func TestEncryptionOptionalKeyLength(t *testing.T) {
	enc := NewEncoder()
	desired := &cddsdkgo.SrtEncryption{Passphrase: "0123456789passphrase"}

	enc.HandleTransportConfigChange("CH01", buildSrtCallerProtocol(desired))

	got := srtCallerFromProtocol(t, enc.GetChannelConnection("CH01"))
	if got.Encryption == nil {
		t.Fatal("expected encryption in read-back connection")
	}
	if got.Encryption.KeyLength != nil {
		t.Errorf("keyLength: got %v, want nil (device default)", *got.Encryption.KeyLength)
	}
}

func TestEncryptionCleared(t *testing.T) {
	enc := NewEncoder()
	keyLen := cddsdkgo.SRTENCRYPTIONKEYLENGTH_AES_128
	enc.HandleTransportConfigChange("CH01", buildSrtCallerProtocol(
		&cddsdkgo.SrtEncryption{Passphrase: "0123456789passphrase", KeyLength: &keyLen}))

	enc.HandleTransportConfigChange("CH01", buildSrtCallerProtocol(nil))

	got := srtCallerFromProtocol(t, enc.GetChannelConnection("CH01"))
	if got.Encryption != nil {
		t.Errorf("expected encryption cleared, got %+v", got.Encryption)
	}
}

func TestPbKeyLenMapping(t *testing.T) {
	aes128 := cddsdkgo.SRTENCRYPTIONKEYLENGTH_AES_128
	aes192 := cddsdkgo.SRTENCRYPTIONKEYLENGTH_AES_192
	aes256 := cddsdkgo.SRTENCRYPTIONKEYLENGTH_AES_256
	cases := []struct {
		name      string
		keyLength *cddsdkgo.SrtEncryptionKeyLength
		want      int
	}{
		{"nil (device default)", nil, 0},
		{"AES_128", &aes128, 16},
		{"AES_192", &aes192, 24},
		{"AES_256", &aes256, 32},
	}
	for _, tc := range cases {
		if got := pbKeyLen(tc.keyLength); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestActualConfigurationReportsEncryptionFromDevice(t *testing.T) {
	shim := NewTr12Shim()
	reg := buildTestRegistration()

	keyLen := cddsdkgo.SRTENCRYPTIONKEYLENGTH_AES_192
	desired := &cddsdkgo.DesiredDeviceConfiguration{
		Version: "device-v1",
		Channels: []cddsdkgo.DesiredChannelConfiguration{
			{
				Id:      "CH01",
				Version: "CH01-v1",
				// IDLE so applying state doesn't try to launch ffmpeg in tests.
				State:    cddsdkgo.CHANNELSTATE_IDLE,
				Protocol: buildSrtCallerProtocol(&cddsdkgo.SrtEncryption{Passphrase: "0123456789passphrase", KeyLength: &keyLen}),
			},
		},
	}

	if !shim.ApplyDesiredConfiguration(desired) {
		t.Fatal("ApplyDesiredConfiguration returned false")
	}

	actual := shim.GetActualConfiguration(reg, desired, map[string]string{"CH01": "CH01-v1"})
	if len(actual.Channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(actual.Channels))
	}
	got := srtCallerFromProtocol(t, actual.Channels[0].Protocol)
	if got.Encryption == nil {
		t.Fatal("expected encryption in actual configuration")
	}
	if got.Encryption.Passphrase != "0123456789passphrase" {
		t.Errorf("passphrase: got %q, want %q", got.Encryption.Passphrase, "0123456789passphrase")
	}
	if got.Encryption.KeyLength == nil || *got.Encryption.KeyLength != cddsdkgo.SRTENCRYPTIONKEYLENGTH_AES_192 {
		t.Errorf("keyLength: got %v, want AES_192", got.Encryption.KeyLength)
	}

	// A new desired config without encryption must clear it from actual.
	desired.Channels[0].Protocol = buildSrtCallerProtocol(nil)
	desired.Channels[0].Version = "CH01-v2"
	if !shim.ApplyDesiredConfiguration(desired) {
		t.Fatal("ApplyDesiredConfiguration returned false")
	}
	actual = shim.GetActualConfiguration(reg, desired, map[string]string{"CH01": "CH01-v2"})
	got = srtCallerFromProtocol(t, actual.Channels[0].Protocol)
	if got.Encryption != nil {
		t.Errorf("expected encryption cleared in actual configuration, got %+v", got.Encryption)
	}
}
