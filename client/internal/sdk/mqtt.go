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
// MQTT connection, subscription, and callback handling for the CDD SDK.
package sdk

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/vsf-tv/TR-12-Client-and-Host-Go/client/internal/models"
	"github.com/vsf-tv/TR-12-Client-and-Host-Go/client/internal/utils"
	cddsdkgo "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo"
	tr12models "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/TR-12-Models/generated/tr12go"
)

// parseBrokerAddress converts an mqttUri value into a paho-compatible broker address.
// Handles both full URIs like "tls://host:port" and plain hostnames (defaults to ssl://host:443).
func parseBrokerAddress(raw string) string {
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err == nil && u.Host != "" {
			host := u.Hostname()
			port := u.Port()
			if port == "" {
				port = "443"
			}
			return fmt.Sprintf("ssl://%s:%s", host, port)
		}
	}
	return fmt.Sprintf("ssl://%s:443", raw)
}

// startConnect attempts an MQTT connection once certs are available.
func (s *CddSdk) startConnect() (cddsdkgo.ConnectResponseContent, error) {
	if s.is(models.StateReconnecting, models.StateConnected) {
		return cddsdkgo.ConnectResponseContent{
			Success:    true,
			State:      s.state,
			Message:    "Already connected or automatically re-connecting",
			DeviceId:   tr12models.PtrString(s.certs.GetDeviceID()),
			RegionName: tr12models.PtrString(s.certs.GetRegion()),
		}, nil
	}
	if s.mqttClient != nil && s.state == models.StateConnecting {
		return cddsdkgo.ConnectResponseContent{
			Success: true, State: s.state, Message: "Connecting",
		}, nil
	}

	s.transition(models.StateConnecting)

	tlsConfig, err := utils.SSLContext(
		s.certs.CACertFile,
		s.certs.DeviceCertFile,
		s.certs.PrivKeyFile,
		s.certs.HostSettings.MqttAlpnProtocol,
	)
	if err != nil {
		s.logger.Errorf("[MQTT] SSL setup failed: %v (type=%T)", err, err)
		s.reset()
		return cddsdkgo.ConnectResponseContent{
			Success: false, State: s.state,
			Message: "Unable to connect at this time. Check network connection.",
			Error:   utils.ExceptionToErrorDetails(fmt.Errorf("SSL setup error: %w", err)),
		}, nil
	}

	if err := s.connectMQTT(tlsConfig); err != nil {
		s.logger.Errorf("[MQTT] connect failed: %v (type=%T)", err, err)
		s.reset()
		return cddsdkgo.ConnectResponseContent{
			Success: false, State: s.state,
			Message: "Unable to connect at this time. Check network connection.",
			Error:   utils.ExceptionToErrorDetails(fmt.Errorf("connection error: %w", err)),
		}, nil
	}

	return cddsdkgo.ConnectResponseContent{
		Success:    true,
		State:      s.state,
		Message:    "Connection started",
		RegionName: tr12models.PtrString(s.certs.GetRegion()),
		DeviceId:   tr12models.PtrString(s.certs.GetDeviceID()),
	}, nil
}

func (s *CddSdk) connectMQTT(tlsConfig *tls.Config) error {
	uri := s.certs.GetURI()
	keepalive := time.Duration(s.certs.HostSettings.MqttKeepaliveSeconds) * time.Second

	brokerAddr := parseBrokerAddress(uri)
	s.logger.Infof("[MQTT] raw URI from creds: %q  →  broker address: %s", uri, brokerAddr)

	opts := mqtt.NewClientOptions().
		AddBroker(brokerAddr).
		SetClientID(s.certs.GetDeviceID()).
		SetTLSConfig(tlsConfig).
		SetKeepAlive(keepalive).
		SetCleanSession(true).
		SetMaxReconnectInterval(10 * time.Second).
		SetAutoReconnect(true).
		SetOrderMatters(false).
		SetOnConnectHandler(s.onConnect).
		SetConnectionLostHandler(s.onDisconnect).
		SetDefaultPublishHandler(s.onMessage)

	s.mqttClient = mqtt.NewClient(opts)
	token := s.mqttClient.Connect()
	if !token.WaitTimeout(30 * time.Second) {
		return fmt.Errorf("MQTT connect timed out")
	}
	if token.Error() != nil {
		return fmt.Errorf("MQTT connect failed: %w", token.Error())
	}
	return nil
}

// onConnect is the MQTT CONNACK callback — subscribes to topics and sends registration.
func (s *CddSdk) onConnect(client mqtt.Client) {
	s.logger.Info("ON CONNECT CALLBACK")
	s.transition(models.StateConnected)

	hs, err := s.certs.GetHostSettings()
	if err != nil {
		s.logger.Errorf("Error in onConnect: %v", err)
		return
	}

	s.logger.Infof("[MQTT] HostSettings dump: desiredConfigTopic=%q certRotationTopic=%q thumbnailTopic=%q deprovisionTopic=%q logSubscriptionTopic=%q",
		hs.DeviceSubscribesToDesiredConfigurationTopic, hs.DeviceSubscribesToCertificateRotationTopic, hs.DeviceSubscribesToThumbnailSubscriptionTopic, hs.DeviceSubscribesToDeprovisionTopic, hs.DeviceSubscribesToLogSubscriptionTopic)
	s.logger.Infof("[MQTT] DeviceID=%q from certs store", s.certs.GetDeviceID())

	subscriptions := map[string]mqtt.MessageHandler{
		hs.DeviceSubscribesToDesiredConfigurationTopic:  s.updateConfigurationCallback,
		hs.DeviceSubscribesToCertificateRotationTopic:   s.updateCertsCallback,
		hs.DeviceSubscribesToThumbnailSubscriptionTopic: s.updateThumbnailSubscriptionCallback,
		hs.DeviceSubscribesToDeprovisionTopic:           s.deprovisionDeviceCallback,
		hs.DeviceSubscribesToLogSubscriptionTopic:       s.updateLogSubscriptionCallback,
	}
	for topic, handler := range subscriptions {
		s.logger.Infof("[MQTT] Subscribing to topic: %s", topic)
		token := client.Subscribe(topic, 0, handler)
		if token.WaitTimeout(5*time.Second) && token.Error() != nil {
			s.logger.Errorf("Failed to subscribe to %s: %v", topic, token.Error())
		} else {
			s.logger.Infof("[MQTT] Subscribed OK: %s", topic)
		}
	}

	s.reportRegistration()
	s.regDelivered = true
}

// onDisconnect handles connection-lost events.
func (s *CddSdk) onDisconnect(client mqtt.Client, err error) {
	if s.is(models.StateConnected) {
		s.logger.Infof("Connection lost: %v", err)
		s.transition(models.StateReconnecting)
	}
}

// onMessage handles unrouted messages.
func (s *CddSdk) onMessage(client mqtt.Client, msg mqtt.Message) {
	s.logger.Errorf("[MQTT] Got UNHANDLED message on topic: %s payloadLen=%d payload=%s", msg.Topic(), len(msg.Payload()), string(msg.Payload()))
}

// doPublishMessage publishes a JSON payload to a topic, retrying registration if needed.
func (s *CddSdk) doPublishMessage(payload interface{}, topic string) error {
	if !s.regDelivered {
		s.logger.Info("Attempting to re-publish registration")
		s.reportRegistration()
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	if s.mqttClient == nil {
		return fmt.Errorf("MQTT client not connected")
	}
	token := s.mqttClient.Publish(topic, 0, false, data)
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("MQTT publish timed out")
	}
	if token.Error() != nil {
		return fmt.Errorf("MQTT publish error: %w", token.Error())
	}
	s.logger.Infof("Message accepted for delivery on %s", topic)
	return nil
}

// reportRegistration publishes the device registration to the host service.
func (s *CddSdk) reportRegistration() {
	if !s.is(models.StateConnected) {
		s.logger.Error("Can't report registration when not connected")
		return
	}
	hs, err := s.certs.GetHostSettings()
	if err != nil {
		s.logger.Errorf("Can't get host settings for registration: %v", err)
		return
	}
	if s.registration == nil {
		s.logger.Error("No registration stored — cannot publish")
		return
	}
	// s.registration is already a validated typed DeviceRegistration — publish directly.
	envelope := map[string]interface{}{
		"deviceRegistration": s.registration,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		s.logger.Errorf("Failed to marshal registration envelope: %v", err)
		return
	}
	s.logger.Info("Reporting Registration")
	token := s.mqttClient.Publish(hs.DevicePublishesRegistrationTopic, 1, false, data)
	if token.WaitTimeout(5*time.Second) && token.Error() != nil {
		s.logger.Errorf("Failed to publish registration: %v", token.Error())
		return
	}
	s.logger.Info("Registration delivered")
}

// --- MQTT subscription callbacks ---

func (s *CddSdk) updateConfigurationCallback(_ mqtt.Client, msg mqtt.Message) {
	s.logger.Infof("****** CONFIG UPDATE received on topic=%s payloadLen=%d", msg.Topic(), len(msg.Payload()))
	s.logger.Info("****** CONFIG UPDATE payload:\n" + string(msg.Payload()))

	// Deserialize the envelope, extract desiredDeviceConfiguration, then deserialize the config
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(msg.Payload(), &envelope); err != nil {
		s.logger.Errorf("Could not parse configuration update envelope: %v", err)
		return
	}

	var updateID string
	if rawUID, ok := envelope["updateId"]; ok {
		var numID float64
		var strID string
		if err := json.Unmarshal(rawUID, &numID); err == nil {
			updateID = fmt.Sprintf("%d", int(numID))
		} else if err := json.Unmarshal(rawUID, &strID); err == nil {
			updateID = strID
		}
		s.logger.Infof("****** CONFIG UPDATE updateId=%s", updateID)
	}
	if updateID == "" {
		updateID = s.updateID.Get()
	}

	// Extract the desiredDeviceConfiguration field from the envelope
	cfgRaw, ok := envelope["desiredDeviceConfiguration"]
	if !ok {
		s.logger.Errorf("Could not find desiredDeviceConfiguration in config update envelope")
		return
	}
	var deviceConfig cddsdkgo.DesiredDeviceConfiguration
	if err := json.Unmarshal(cfgRaw, &deviceConfig); err != nil {
		s.logger.Errorf("Could not deserialize DesiredDeviceConfiguration: %v", err)
		return
	}

	s.configPayload = &deviceConfig
	s.logger.Infof("****** CONFIG UPDATE stored, version=%s", deviceConfig.Version)
}

func (s *CddSdk) updateCertsCallback(_ mqtt.Client, msg mqtt.Message) {
	var rotate tr12models.DeviceSubscribesToCertificateRotationResponseContent
	if err := json.Unmarshal(msg.Payload(), &rotate); err != nil {
		s.logger.Errorf("[CERTS] Could not parse credential update: %v", err)
		return
	}
	certSnip := rotate.DeviceCertificate
	if len(certSnip) > 80 {
		certSnip = certSnip[:80]
	}
	s.logger.Infof("[CERTS] Got rotate message. mqttUri=%q regionName=%q certStart=%q", rotate.MqttUri, rotate.GetRegionName(), certSnip)
	updated, err := s.certs.RotateCerts(&rotate)
	if err != nil {
		s.logger.Errorf("[CERTS] Could not process credential update: %v", err)
		return
	}
	if updated {
		// Save registration before Disconnect clears it via reset().
		// Snapshot hostID too since reset() clears it.
		savedRegistration := s.registration
		savedHostID := s.hostID
		s.logger.Info("[CERTS] Cert changed — reconnecting with new credentials")
		s.Disconnect()
		time.Sleep(1 * time.Second)
		// Re-connect using the public API method — it acquires apiLock internally.
		// The MQTT callback does not hold apiLock so there is no deadlock risk here.
		s.Connect(savedRegistration, savedHostID)
	} else {
		s.logger.Info("[CERTS] Device cert not changed. No action taken.")
	}
}

func (s *CddSdk) updateThumbnailSubscriptionCallback(_ mqtt.Client, msg mqtt.Message) {
	s.logger.Infof("[THUMB] raw payload: %s", string(msg.Payload()))
	var sub tr12models.DeviceSubscribesToThumbnailSubscriptionResponseContent
	if err := json.Unmarshal(msg.Payload(), &sub); err != nil {
		s.logger.Errorf("Could not process thumbnail subscription update: %v", err)
		return
	}
	local := models.RequestThumbnailRequestContent{
		Requests: sub.Requests,
	}
	for channelID, req := range local.Requests {
		s.logger.Infof("[THUMB] channel=%s remotePath=%q periodSeconds=%.0f expiresAt=%v maxSizeKB=%.0f",
			channelID, req.GetRemotePath(), req.GetPeriodSeconds(), req.GetExpiresAt(), req.GetMaxSizeKB())
	}
	if err := s.thumbnailManager.UpdateThumbnail(&local); err != nil {
		s.logger.Errorf("Thumbnail subscription error: %v", err)
	}
}

func (s *CddSdk) deprovisionDeviceCallback(_ mqtt.Client, msg mqtt.Message) {
	var deprov tr12models.DeviceSubscribesToDeprovisionResponseContent
	if err := json.Unmarshal(msg.Payload(), &deprov); err != nil {
		s.logger.Errorf("Could not parse deprovision update. Deprovisioning anyway: %v", err)
	} else {
		s.logger.Infof("Service deprovisioned client at: %s. Reason: %s", deprov.GetTimestamp(), deprov.GetReason())
	}
	if err := s.handleDeprovision(s.hostID); err != nil {
		s.logger.Errorf("Could not process deprovision update: %v", err)
	}
}

func (s *CddSdk) updateLogSubscriptionCallback(_ mqtt.Client, msg mqtt.Message) {
	var logSub tr12models.DeviceSubscribesToLogSubscriptionResponseContent
	if err := json.Unmarshal(msg.Payload(), &logSub); err != nil {
		s.logger.Errorf("Could not process logger update: %v", err)
		return
	}
	s.logSubscription = logSub
	s.logger.Info("Got new log request")
	s.logger.Dump()
}

// reportLogs is the callback invoked after log rotation to upload logs to the host service.
func (s *CddSdk) reportLogs(logFilePath string) {
	if s.processingLogPut {
		s.logSpewDetected = time.Now().Unix()
		return
	}
	s.processingLogPut = true
	defer func() { s.processingLogPut = false }()

	if s.logSpewDetected > 0 {
		s.logger.Errorf("Log spew detected. Last at %d", s.logSpewDetected)
		s.logSpewDetected = 0
	}
	remotePath := s.logSubscription.GetRemotePath()
	expires := s.logSubscription.GetExpiresAt()
	if remotePath != "" && s.logSubscription.HasExpiresAt() && time.Now().Before(expires) {
		s.logger.Info("Pushing Logs")
		if err := utils.UploadFile(logFilePath, remotePath, 5, "log", nil); err != nil {
			s.logger.Errorf("Can't upload logs: %v", err)
		}
	}
}
