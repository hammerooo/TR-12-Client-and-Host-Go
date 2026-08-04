// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
package version

import "testing"

func TestIsCompatible(t *testing.T) {
	cases := []struct {
		name           string
		receiver       string
		payload        string
		wantCompatible bool
	}{
		{"same version", "11.0.0", "11.0.0", true},
		{"receiver minor greater", "11.2.0", "11.0.0", true},
		{"payload minor greater", "11.0.0", "11.2.0", false},
		{"major mismatch newer", "11.0.0", "10.9.0", false},
		{"major mismatch older", "10.9.0", "11.0.0", false},
		{"patch ignored", "11.0.5", "11.0.9", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IsCompatible(tc.receiver, tc.payload)
			if err != nil {
				t.Fatalf("IsCompatible: %v", err)
			}
			if got != tc.wantCompatible {
				t.Fatalf("receiver=%s payload=%s: want compatible=%v got %v",
					tc.receiver, tc.payload, tc.wantCompatible, got)
			}
		})
	}
}
