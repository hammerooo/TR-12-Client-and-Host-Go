module github.com/vsf-tv/TR-12-Client-and-Host-Go/test/debug_deserialize

go 1.23.0

require github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo v0.0.0

require gopkg.in/validator.v2 v2.0.1 // indirect

replace github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo => ../../models/cdd_sdk/generated/cdd_sdkgo
