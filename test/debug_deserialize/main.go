package main

import (
	"encoding/json"
	"fmt"

	cddsdkgo "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo"
)

func main() {
	// This is the exact payload from the host's MQTT message (desiredDeviceConfiguration inner content)
	desiredJSON := `{
		"channels": [{
			"channelSettings": {
				"standardSettings": [
					{"id":"RS01","value":"1920x1080"},
					{"id":"FR01","value":"30"},
					{"id":"MB01","value":"8000"},
					{"id":"RC01","value":"CBR"},
					{"id":"CO01","value":"H.264_main"},
					{"id":"GP01","value":"60"},
					{"id":"IN01","value":"SDI"},
					{"id":"AUD01EN","value":"ENABLED"},
					{"id":"AUD02EN","value":"DISABLED"},
					{"id":"AUD03EN","value":"DISABLED"},
					{"id":"AUD04EN","value":"DISABLED"}
				]
			},
			"id": "CH01",
			"protocol": {
				"srtCaller": {
					"address": "52.43.112.138",
					"encryption": {"keyLength":"AES_128","passphrase":"foobarbarbar"},
					"minimumLatencyMilliseconds": 3000,
					"port": 3601
				}
			},
			"state": "ACTIVE",
			"version": "1783559747713161166"
		}],
		"version": "1783559747713161166"
	}`

	fmt.Println("=== Test 1: Deserialize full DesiredDeviceConfiguration ===")
	var desired cddsdkgo.DesiredDeviceConfiguration
	err := json.Unmarshal([]byte(desiredJSON), &desired)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: %d channels\n", len(desired.Channels))
	}

	fmt.Println("\n=== Test 2: Deserialize just ChannelSettings ===")
	csJSON := `{"standardSettings": [{"id":"RS01","value":"1920x1080"}]}`
	var cs cddsdkgo.ChannelSettings
	err = json.Unmarshal([]byte(csJSON), &cs)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: Profile=%v StandardSettings=%v\n", cs.Profile != nil, cs.StandardSettings != nil)
	}

	fmt.Println("\n=== Test 3: Deserialize ChannelSettings with profile ===")
	csProfileJSON := `{"profile": {"id":"h264c"}}`
	var cs2 cddsdkgo.ChannelSettings
	err = json.Unmarshal([]byte(csProfileJSON), &cs2)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: Profile=%v StandardSettings=%v\n", cs2.Profile != nil, cs2.StandardSettings != nil)
	}

	fmt.Println("\n=== Test 4: Deserialize single DesiredChannelConfiguration ===")
	chJSON := `{
		"channelSettings": {"standardSettings": [{"id":"RS01","value":"1920x1080"}]},
		"id": "CH01",
		"state": "ACTIVE",
		"version": "1"
	}`
	var ch cddsdkgo.DesiredChannelConfiguration
	err = json.Unmarshal([]byte(chJSON), &ch)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: id=%s state=%s\n", ch.Id, ch.State)
	}

	fmt.Println("\n=== Test 5: Minimal channel - no channelSettings, no protocol ===")
	minJSON := `{"id": "CH01", "state": "ACTIVE", "version": "1"}`
	var ch2 cddsdkgo.DesiredChannelConfiguration
	err = json.Unmarshal([]byte(minJSON), &ch2)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: id=%s state=%s\n", ch2.Id, ch2.State)
	}

	fmt.Println("\n=== Test 6: Channel with protocol only ===")
	protoJSON := `{
		"id": "CH01", "state": "ACTIVE", "version": "1",
		"protocol": {"srtCaller": {"address": "1.2.3.4", "port": 9000}}
	}`
	var ch3 cddsdkgo.DesiredChannelConfiguration
	err = json.Unmarshal([]byte(protoJSON), &ch3)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: id=%s protocol.SrtCaller=%v\n", ch3.Id, ch3.Protocol.SrtCaller != nil)
	}

	fmt.Println("\n=== Test 7: Channel with channelSettings + protocol ===")
	bothJSON := `{
		"id": "CH01", "state": "ACTIVE", "version": "1",
		"channelSettings": {"standardSettings": [{"id":"RS01","value":"1080"}]},
		"protocol": {"srtCaller": {"address": "1.2.3.4", "port": 9000}}
	}`
	var ch4 cddsdkgo.DesiredChannelConfiguration
	err = json.Unmarshal([]byte(bothJSON), &ch4)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: id=%s settings=%v proto=%v\n", ch4.Id, ch4.ChannelSettings != nil, ch4.Protocol != nil)
	}

	fmt.Println("\n=== Test 8: DesiredDeviceConfiguration with minimal channel ===")
	minDesiredJSON := `{"channels": [{"id": "CH01", "state": "ACTIVE", "version": "1"}], "version": "1"}`
	var desired2 cddsdkgo.DesiredDeviceConfiguration
	err = json.Unmarshal([]byte(minDesiredJSON), &desired2)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: %d channels\n", len(desired2.Channels))
	}

	fmt.Println("\n=== Test 9: DesiredDeviceConfiguration with channel + protocol (no settings) ===")
	protoDesiredJSON := `{"channels": [{"id": "CH01", "state": "ACTIVE", "version": "1", "protocol": {"srtCaller": {"address": "1.2.3.4", "port": 9000}}}], "version": "1"}`
	var desired3 cddsdkgo.DesiredDeviceConfiguration
	err = json.Unmarshal([]byte(protoDesiredJSON), &desired3)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: %d channels, proto=%v\n", len(desired3.Channels), desired3.Channels[0].Protocol != nil)
	}

	fmt.Println("\n=== Test 10: DesiredDeviceConfiguration with channel + settings (no protocol) ===")
	settingsDesiredJSON := `{"channels": [{"id": "CH01", "state": "ACTIVE", "version": "1", "channelSettings": {"standardSettings": [{"id":"RS01","value":"1080"}]}}], "version": "1"}`
	var desired4 cddsdkgo.DesiredDeviceConfiguration
	err = json.Unmarshal([]byte(settingsDesiredJSON), &desired4)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: %d channels, settings=%v\n", len(desired4.Channels), desired4.Channels[0].ChannelSettings != nil)
	}

	fmt.Println("\n=== Test 11: DesiredDeviceConfiguration with channel + settings + protocol ===")
	fullDesiredJSON := `{"channels": [{"id": "CH01", "state": "ACTIVE", "version": "1", "channelSettings": {"standardSettings": [{"id":"RS01","value":"1080"}]}, "protocol": {"srtCaller": {"address": "1.2.3.4", "port": 9000}}}], "version": "1"}`
	var desired5 cddsdkgo.DesiredDeviceConfiguration
	err = json.Unmarshal([]byte(fullDesiredJSON), &desired5)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: %d channels, settings=%v proto=%v\n", len(desired5.Channels), desired5.Channels[0].ChannelSettings != nil, desired5.Channels[0].Protocol != nil)
	}

	fmt.Println("\n=== Test 12: Same as Test 11 but with encryption ===")
	encDesiredJSON := `{"channels": [{"id": "CH01", "state": "ACTIVE", "version": "1", "channelSettings": {"standardSettings": [{"id":"RS01","value":"1080"}]}, "protocol": {"srtCaller": {"address": "1.2.3.4", "port": 9000, "encryption": {"passphrase": "foobarbarbar", "keyLength": "AES_128"}}}}], "version": "1"}`
	var desired6 cddsdkgo.DesiredDeviceConfiguration
	err = json.Unmarshal([]byte(encDesiredJSON), &desired6)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: %d channels, settings=%v proto=%v\n", len(desired6.Channels), desired6.Channels[0].ChannelSettings != nil, desired6.Channels[0].Protocol != nil)
	}

	fmt.Println("\n=== Test 13: Same as Test 1 but with many settings ===")
	manySettingsJSON := `{"channels": [{"id": "CH01", "state": "ACTIVE", "version": "1", "channelSettings": {"standardSettings": [{"id":"RS01","value":"1920x1080"},{"id":"FR01","value":"30"},{"id":"MB01","value":"8000"},{"id":"RC01","value":"CBR"},{"id":"CO01","value":"H.264_main"},{"id":"GP01","value":"60"},{"id":"IN01","value":"SDI"},{"id":"AUD01_EN","value":"ENABLED"},{"id":"AUD02_EN","value":"DISABLED"},{"id":"AUD03_EN","value":"DISABLED"},{"id":"AUD04_EN","value":"DISABLED"}]}, "protocol": {"srtCaller": {"address": "52.43.112.138", "port": 3601, "minimumLatencyMilliseconds": 3000, "encryption": {"keyLength":"AES_128","passphrase":"foobarbarbar"}}}}], "version": "1"}`
	var desired7 cddsdkgo.DesiredDeviceConfiguration
	err = json.Unmarshal([]byte(manySettingsJSON), &desired7)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: %d channels\n", len(desired7.Channels))
	}

	// Binary search on number of settings
	fmt.Println("\n=== Test 14: Binary search - how many settings break it? ===")
	for n := 1; n <= 11; n++ {
		settings := ""
		for i := 0; i < n; i++ {
			if i > 0 { settings += "," }
			settings += fmt.Sprintf(`{"id":"S%02d","value":"v%d"}`, i, i)
		}
		testJSON := fmt.Sprintf(`{"channels": [{"id": "CH01", "state": "ACTIVE", "version": "1", "channelSettings": {"standardSettings": [%s]}}], "version": "1"}`, settings)
		var d cddsdkgo.DesiredDeviceConfiguration
		err = json.Unmarshal([]byte(testJSON), &d)
		if err != nil {
			fmt.Printf("  n=%d FAILS: %v\n", n, err)
			break
		} else {
			fmt.Printf("  n=%d OK\n", n)
		}
	}

	fmt.Println("\n=== Test 15: Exact real settings but NO protocol ===")
	realSettingsJSON := `{"channels": [{"id": "CH01", "state": "ACTIVE", "version": "1", "channelSettings": {"standardSettings": [{"id":"RS01","value":"1920x1080"},{"id":"FR01","value":"30"},{"id":"MB01","value":"8000"},{"id":"RC01","value":"CBR"},{"id":"CO01","value":"H.264_main"},{"id":"GP01","value":"60"},{"id":"IN01","value":"SDI"},{"id":"AUD01_EN","value":"ENABLED"},{"id":"AUD02_EN","value":"DISABLED"},{"id":"AUD03_EN","value":"DISABLED"},{"id":"AUD04_EN","value":"DISABLED"}]}}], "version": "1"}`
	var desired8 cddsdkgo.DesiredDeviceConfiguration
	err = json.Unmarshal([]byte(realSettingsJSON), &desired8)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: %d channels\n", len(desired8.Channels))
	}

	fmt.Println("\n=== Test 16: Exact real settings WITH protocol (no encryption) ===")
	realSettingsProtoJSON := `{"channels": [{"id": "CH01", "state": "ACTIVE", "version": "1", "channelSettings": {"standardSettings": [{"id":"RS01","value":"1920x1080"},{"id":"FR01","value":"30"},{"id":"MB01","value":"8000"},{"id":"RC01","value":"CBR"},{"id":"CO01","value":"H.264_main"},{"id":"GP01","value":"60"},{"id":"IN01","value":"SDI"},{"id":"AUD01_EN","value":"ENABLED"},{"id":"AUD02_EN","value":"DISABLED"},{"id":"AUD03_EN","value":"DISABLED"},{"id":"AUD04_EN","value":"DISABLED"}]}, "protocol": {"srtCaller": {"address": "52.43.112.138", "port": 3601, "minimumLatencyMilliseconds": 3000}}}], "version": "1"}`
	var desired9 cddsdkgo.DesiredDeviceConfiguration
	err = json.Unmarshal([]byte(realSettingsProtoJSON), &desired9)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: %d channels\n", len(desired9.Channels))
	}

	fmt.Println("\n=== Test 17: Exact real settings WITH encryption ===")
	realFullJSON := `{"channels": [{"id": "CH01", "state": "ACTIVE", "version": "1", "channelSettings": {"standardSettings": [{"id":"RS01","value":"1920x1080"},{"id":"FR01","value":"30"},{"id":"MB01","value":"8000"},{"id":"RC01","value":"CBR"},{"id":"CO01","value":"H.264_main"},{"id":"GP01","value":"60"},{"id":"IN01","value":"SDI"},{"id":"AUD01_EN","value":"ENABLED"},{"id":"AUD02_EN","value":"DISABLED"},{"id":"AUD03_EN","value":"DISABLED"},{"id":"AUD04_EN","value":"DISABLED"}]}, "protocol": {"srtCaller": {"address": "52.43.112.138", "port": 3601, "minimumLatencyMilliseconds": 3000, "encryption": {"keyLength":"AES_128","passphrase":"foobarbarbar"}}}}], "version": "1"}`
	var desired10 cddsdkgo.DesiredDeviceConfiguration
	err = json.Unmarshal([]byte(realFullJSON), &desired10)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: %d channels\n", len(desired10.Channels))
	}

	fmt.Println("\n=== Test 18: Exact payload from MQTT (with long version ID) ===")
	exactMqttJSON := `{"channels":[{"channelSettings":{"standardSettings":[{"id":"RS01","value":"1920x1080"},{"id":"FR01","value":"30"},{"id":"MB01","value":"8000"},{"id":"RC01","value":"CBR"},{"id":"CO01","value":"H.264_main"},{"id":"GP01","value":"60"},{"id":"IN01","value":"SDI"},{"id":"AUD01_EN","value":"ENABLED"},{"id":"AUD02_EN","value":"DISABLED"},{"id":"AUD03_EN","value":"DISABLED"},{"id":"AUD04_EN","value":"DISABLED"}]},"id":"CH01","protocol":{"srtCaller":{"address":"52.43.112.138","encryption":{"keyLength":"AES_128","passphrase":"foobarbarbar"},"minimumLatencyMilliseconds":3000,"port":3601}},"state":"ACTIVE","version":"1783559747713161166"}],"version":"1783559747713161166"}`
	var desired11 cddsdkgo.DesiredDeviceConfiguration
	err = json.Unmarshal([]byte(exactMqttJSON), &desired11)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		fmt.Printf("OK: %d channels\n", len(desired11.Channels))
	}
}