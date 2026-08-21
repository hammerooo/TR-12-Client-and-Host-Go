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
// Application Reference Design (ARD) — simulates a 1-channel encoder device
// that makes REST calls on the CDD SDK daemon.
//
// Usage:
//
//	ard --host_id <host_id>
//	ard --host_id vsf_test_host --sdk_url http://127.0.0.1:8603
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	ard "github.com/vsf-tv/TR-12-Client-and-Host-Go/client/internal/application_reference_design"
)

func main() {
	hostID := flag.String("host_id", "", "Host ID to connect to (required)")
	sdkURL := flag.String("sdk_url", "http://127.0.0.1:8603", "Base URL of the running CDD SDK")
	registrationFile := flag.String("registration_file", "", "Path to registration JSON file (default: payloads/1_channel_encoder/registration.json)")
	deprovisionAfter := flag.Duration("deprovision_after", 0, "If > 0, deprovision from the host and exit once CONNECTED for this long (e.g. 1m, 90s)")
	flag.Parse()

	if *hostID == "" {
		flag.Usage()
		os.Exit(1)
	}

	// Resolve base path for payloads and thumbnail images.
	// When built in-place (go build -o ard ./cmd/application_reference_design),
	// the binary sits next to payloads/ and thumbnails/ so filepath.Dir(execPath) works.
	// When built elsewhere (e.g. go build -o client/ard), walk up candidate paths.
	execPath, err := os.Executable()
	if err != nil {
		execPath, _ = os.Getwd()
	}
	basePath := filepath.Dir(execPath)
	if _, err := os.Stat(filepath.Join(basePath, "payloads")); os.IsNotExist(err) {
		// Try source directory relative to cwd (covers `go run` and out-of-tree builds)
		cwd, _ := os.Getwd()
		for _, candidate := range []string{
			filepath.Join(cwd, "cmd", "application_reference_design"),
			cwd,
		} {
			if _, err := os.Stat(filepath.Join(candidate, "payloads")); err == nil {
				basePath = candidate
				break
			}
		}
	}

	app, err := ard.NewClientApplication(*sdkURL, basePath, *registrationFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize ARD: %v\n", err)
		os.Exit(1)
	}
	app.SetDeprovisionAfter(*deprovisionAfter)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nReceived shutdown signal, cleaning up...")
		app.Stop()
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()

	fmt.Printf("ARD connecting to host: %s via SDK at %s\n", *hostID, *sdkURL)
	if *deprovisionAfter > 0 {
		fmt.Printf("ARD will deprovision and exit after %s connected\n", *deprovisionAfter)
	}
	app.RunLoop(*hostID)
}
