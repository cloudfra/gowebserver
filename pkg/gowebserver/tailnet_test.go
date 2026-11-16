// Copyright 2026 Cloudfra
//
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

//go:build !(js || wasip1 || dragonfly || netbsd)

package gowebserver

import (
	"strings"
	"testing"
)

// TestTailnetValidation covers the tailnet settings that cannot work, and
// that a disabled tailnet is never validated.
func TestTailnetValidation(t *testing.T) {
	// An auth key in the environment of the test would make the ephemeral
	// cases pass for the wrong reason.
	t.Setenv("TS_AUTHKEY", "")
	t.Setenv("TS_AUTH_KEY", "")

	for name, tc := range map[string]struct {
		tailnet    Tailnet
		envName    string
		envValue   string
		wantErrSub string
	}{
		"off by default asks for nothing": {
			tailnet: Tailnet{},
		},
		"off is not validated": {
			tailnet: Tailnet{HTTPPort: 0, HTTPSPort: 0, Ephemeral: true},
		},
		"on with the default ports": {
			tailnet: Tailnet{Enabled: true, HTTPPort: 80, HTTPSPort: 443},
		},
		"on without an HTTPS port": {
			tailnet:    Tailnet{Enabled: true, HTTPPort: 80},
			wantErrSub: "tailnet: httpsPort 0 is not a port between 1 and 65535",
		},
		"on without an HTTP port": {
			tailnet:    Tailnet{Enabled: true, HTTPSPort: 443},
			wantErrSub: "tailnet: httpPort 0 is not a port between 1 and 65535",
		},
		"port above the range": {
			tailnet:    Tailnet{Enabled: true, HTTPPort: 80, HTTPSPort: 65536},
			wantErrSub: "tailnet: httpsPort 65536 is not a port between 1 and 65535",
		},
		"HTTP and HTTPS on the same port": {
			tailnet:    Tailnet{Enabled: true, HTTPPort: 443, HTTPSPort: 443},
			wantErrSub: "tailnet: httpPort and httpsPort cannot both be 443",
		},
		"ephemeral without an auth key": {
			tailnet:    Tailnet{Enabled: true, HTTPPort: 80, HTTPSPort: 443, Ephemeral: true},
			wantErrSub: "tailnet: ephemeral nodes are only created with an ephemeral auth key",
		},
		"ephemeral with an auth key in the config": {
			tailnet: Tailnet{Enabled: true, HTTPPort: 80, HTTPSPort: 443, Ephemeral: true, AuthKey: "tskey-config"},
		},
		"ephemeral with TS_AUTHKEY": {
			tailnet:  Tailnet{Enabled: true, HTTPPort: 80, HTTPSPort: 443, Ephemeral: true},
			envName:  "TS_AUTHKEY",
			envValue: "tskey-env",
		},
		"ephemeral with TS_AUTH_KEY": {
			tailnet:  Tailnet{Enabled: true, HTTPPort: 80, HTTPSPort: 443, Ephemeral: true},
			envName:  "TS_AUTH_KEY",
			envValue: "tskey-env",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.envName != "" {
				t.Setenv(tc.envName, tc.envValue)
			}
			err := tc.tailnet.validate()
			if tc.wantErrSub == "" {
				if err != nil {
					t.Fatalf("validate() got %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() got nil, want an error containing %q", tc.wantErrSub)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("validate() got %q, want it to contain %q", err, tc.wantErrSub)
			}
		})
	}
}

// TestTailnetAuthKeySource covers where the auth key comes from: the
// configuration before the environment tsnet also reads.
func TestTailnetAuthKeySource(t *testing.T) {
	t.Setenv("TS_AUTHKEY", "tskey-authkey-env")
	t.Setenv("TS_AUTH_KEY", "tskey-auth-key-env")

	for name, tc := range map[string]struct {
		authKey string
		want    string
	}{
		"config wins over the environment": {
			authKey: "tskey-config",
			want:    "tskey-config",
		},
		"no config key uses TS_AUTHKEY": {
			want: "tskey-authkey-env",
		},
	} {
		t.Run(name, func(t *testing.T) {
			tailnet := Tailnet{AuthKey: tc.authKey}
			if got := tailnet.authKey(); got != tc.want {
				t.Errorf("authKey() got %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("only TS_AUTH_KEY set", func(t *testing.T) {
		t.Setenv("TS_AUTHKEY", "")
		tailnet := Tailnet{}
		if got := tailnet.authKey(); got != "tskey-auth-key-env" {
			t.Errorf("authKey() got %q, want %q", got, "tskey-auth-key-env")
		}
	})

	t.Run("no key anywhere", func(t *testing.T) {
		t.Setenv("TS_AUTHKEY", "")
		t.Setenv("TS_AUTH_KEY", "")
		tailnet := Tailnet{}
		if got := tailnet.authKey(); got != "" {
			t.Errorf("authKey() got %q, want an empty string", got)
		}
	})
}
