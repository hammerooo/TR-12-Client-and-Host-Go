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
// ClientApplication — the ARD run loop. Uses ApplicationLoop with ArdCallbacks.
package application_reference_design

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cddsdkgo "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo"
)

// ClientApplication simulates a 1-channel encoder device using the ARD callbacks.
type ClientApplication struct {
	loop          *ApplicationLoop
	basePath      string
	registration  *cddsdkgo.DeviceRegistration
	running       bool
	thumbnailSDI  *ThumbnailSimulator
	thumbnailHDMI *ThumbnailSimulator
	cancelFn      context.CancelFunc
}

// NewClientApplication creates a new ARD instance.
// registrationFile is an optional explicit path to the registration JSON.
// If empty, defaults to payloads/1_channel_encoder/registration.json under basePath.
func NewClientApplication(sdkURL, basePath, registrationFile string) (*ClientApplication, error) {
	if registrationFile == "" {
		registrationFile = filepath.Join(basePath, "payloads", "1_channel_encoder", "registration.json")
	}
	data, err := os.ReadFile(registrationFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read registration file %s: %w", registrationFile, err)
	}
	var registration cddsdkgo.DeviceRegistration
	if err := json.Unmarshal(data, &registration); err != nil {
		return nil, fmt.Errorf("invalid registration JSON: %w", err)
	}

	callbacks := NewArdCallbacks()
	app := &ClientApplication{
		loop:         NewApplicationLoop(sdkURL, callbacks, &registration),
		basePath:     basePath,
		registration: &registration,
		running:      true,
	}

	sdiDir := filepath.Join(basePath, "thumbnails", "thumbnail_images_sdi")
	hdmiDir := filepath.Join(basePath, "thumbnails", "thumbnail_images_hdmi")

	app.thumbnailSDI, err = NewThumbnailSimulator(sdiDir, "/tmp/image_sdi.jpg", 2, "sdi")
	if err != nil {
		fmt.Printf("Warning: SDI thumbnail simulator not available: %v\n", err)
	}
	app.thumbnailHDMI, err = NewThumbnailSimulator(hdmiDir, "/tmp/image_hdmi.jpg", 2, "hdmi")
	if err != nil {
		fmt.Printf("Warning: HDMI thumbnail simulator not available: %v\n", err)
	}

	return app, nil
}

// SetDeprovisionAfter configures the loop to deprovision and exit once the device has been connected for the given duration. Zero disables it.
// Note that on deprovision, all credentials will be removed.
func (a *ClientApplication) SetDeprovisionAfter(d time.Duration) {
	a.loop.SetDeprovisionAfter(d)
}

// Stop signals the application to shut down.
func (a *ClientApplication) Stop() {
	a.running = false
	if a.cancelFn != nil {
		a.cancelFn()
	}
	a.loop.Disconnect()
	if a.thumbnailSDI != nil {
		a.thumbnailSDI.Stop()
	}
	if a.thumbnailHDMI != nil {
		a.thumbnailHDMI.Stop()
	}
	a.loop.shim.CB.(*ArdCallbacks).Encoder.Stop()
}

// RunLoop is the main application loop.
func (a *ClientApplication) RunLoop(hostID string) {
	if a.thumbnailSDI != nil {
		a.thumbnailSDI.Start()
	}
	if a.thumbnailHDMI != nil {
		a.thumbnailHDMI.Start()
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.cancelFn = cancel
	a.loop.Run(ctx, hostID)

	if a.thumbnailSDI != nil {
		a.thumbnailSDI.Stop()
	}
	if a.thumbnailHDMI != nil {
		a.thumbnailHDMI.Stop()
	}
	fmt.Println("Exiting")
}
