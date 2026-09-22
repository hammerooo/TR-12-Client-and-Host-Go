package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidatePathExistsAndWriteable(t *testing.T) {
	t.Run("valid directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := ValidatePathExistsAndWriteable(dir); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})
	t.Run("nonexistent path", func(t *testing.T) {
		err := ValidatePathExistsAndWriteable("/nonexistent/path/xyz")
		if err == nil {
			t.Fatal("expected error for nonexistent path")
		}
	})
	t.Run("file not directory", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file.txt")
		os.WriteFile(f, []byte("x"), 0644)
		err := ValidatePathExistsAndWriteable(f)
		if err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("expected 'not a directory' error, got %v", err)
		}
	})
}

func TestGenerateClientKeys(t *testing.T) {
	pub, priv, err := GenerateClientKeys()
	if err != nil {
		t.Fatalf("GenerateClientKeys: %v", err)
	}
	if !strings.Contains(pub, "PUBLIC KEY") {
		t.Fatalf("expected PEM public key, got %q", pub[:40])
	}
	if !strings.Contains(priv, "PRIVATE KEY") {
		t.Fatalf("expected PEM private key, got %q", priv[:40])
	}
}

func TestGenerateCSR(t *testing.T) {
	_, priv, _ := GenerateClientKeys()
	csr, err := GenerateCSR(priv)
	if err != nil {
		t.Fatalf("GenerateCSR: %v", err)
	}
	if !strings.Contains(csr, "CERTIFICATE REQUEST") {
		t.Fatalf("expected PEM CSR, got %q", csr[:40])
	}
}

func TestGenerateCSR_InvalidKey(t *testing.T) {
	_, err := GenerateCSR("not-a-pem")
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestGetHostConfiguration(t *testing.T) {
	dir := t.TempDir()
	hostCfgDir := filepath.Join(dir, "host_configuration")
	os.MkdirAll(hostCfgDir, 0755)
	cfg := `{"serviceId":"test-host","serviceName":"Test","deviceTypes":["SOURCE"],"createPairingCodeUrl":"http://localhost:8080/pair","authenticatePairingCodeUrl":"http://localhost:8080/authenticate","thumbnailMaximumSizeKB":100,"logFileMaximumSizeKB":500}`
	os.WriteFile(filepath.Join(hostCfgDir, "test-host.json"), []byte(cfg), 0644)

	config, err := GetHostConfiguration("test-host", dir)
	if err != nil {
		t.Fatalf("GetHostConfiguration: %v", err)
	}
	if config.ServiceId != "test-host" {
		t.Fatalf("expected serviceId=test-host, got %q", config.ServiceId)
	}
}

func TestGetHostConfiguration_NotFound(t *testing.T) {
	_, err := GetHostConfiguration("nonexistent", t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

func TestThrottle_AllowsInitialPublishes(t *testing.T) {
	th := NewThrottle(1)
	for i := 0; i < 5; i++ {
		if !th.CanPublish() {
			t.Fatalf("publish %d should be allowed", i)
		}
	}
}

func TestThrottle_BlocksExcessPublishes(t *testing.T) {
	th := NewThrottle(1)
	// maxPublishInWindow = max(10/1, 5) = 10
	for i := 0; i < 10; i++ {
		th.CanPublish()
	}
	if th.CanPublish() {
		t.Fatal("expected throttle to block after max publishes")
	}
}

func TestThrottle_ResetsAfterWindow(t *testing.T) {
	th := NewThrottle(10)
	// windowSeconds=10, maxPub=max(10/10,5)=5
	for i := 0; i < 5; i++ {
		th.CanPublish()
	}
	if th.CanPublish() {
		t.Fatal("expected throttle to block")
	}
	// Manually expire timestamps
	th.mu.Lock()
	past := time.Now().Unix() - 20
	for i := range th.publishTimes {
		th.publishTimes[i] = past
	}
	th.mu.Unlock()
	if !th.CanPublish() {
		t.Fatal("expected throttle to allow after window expires")
	}
}

func TestUpdateID_Sequential(t *testing.T) {
	uid := NewUpdateID()
	id1 := uid.Get()
	id2 := uid.Get()
	if id1 == id2 {
		t.Fatalf("expected different IDs, got %q and %q", id1, id2)
	}
	if !strings.HasSuffix(id1, "_1") {
		t.Fatalf("expected first ID to end with _1, got %q", id1)
	}
	if !strings.HasSuffix(id2, "_2") {
		t.Fatalf("expected second ID to end with _2, got %q", id2)
	}
	// Same base prefix
	base1 := id1[:strings.LastIndex(id1, "_")]
	base2 := id2[:strings.LastIndex(id2, "_")]
	if base1 != base2 {
		t.Fatalf("expected same base, got %q and %q", base1, base2)
	}
}

func TestExceptionToErrorDetails_Nil(t *testing.T) {
	if ExceptionToErrorDetails(nil) != nil {
		t.Fatal("expected nil for nil error")
	}
}

func TestExceptionToErrorDetails_Error(t *testing.T) {
	err := ExceptionToErrorDetails(os.ErrNotExist)
	if err == nil {
		t.Fatal("expected non-nil ErrorDetails")
	}
	if err.Message != os.ErrNotExist.Error() {
		t.Fatalf("expected message %q, got %q", os.ErrNotExist.Error(), err.Message)
	}
}
