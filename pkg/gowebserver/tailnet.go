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

package gowebserver

import (
	"errors"
	"fmt"
	"os"
	"runtime"
)

// tailnetAuthKeyEnv is the list of environment variables tsnet reads an auth
// key from itself, in the order it reads them.
var tailnetAuthKeyEnv = []string{"TS_AUTHKEY", "TS_AUTH_KEY"}

// authKey is the auth key the tailnet node is enrolled with, taken from the
// configuration first and then from the environment tsnet also reads.
func (t *Tailnet) authKey() string {
	if t.AuthKey != "" {
		return t.AuthKey
	}
	for _, name := range tailnetAuthKeyEnv {
		if key := os.Getenv(name); key != "" {
			return key
		}
	}
	return ""
}

// validate refuses the tailnet settings that cannot work. A disabled tailnet
// is never validated.
func (t *Tailnet) validate() error {
	if !t.Enabled {
		return nil
	}
	if !tailnetSupported {
		return fmt.Errorf("tailnet: tailnet.enabled cannot be true in a binary built for %s", runtime.GOOS)
	}
	if t.HTTPPort < 1 || t.HTTPPort > 65535 {
		return fmt.Errorf("tailnet: httpPort %d is not a port between 1 and 65535", t.HTTPPort)
	}
	if t.HTTPSPort < 1 || t.HTTPSPort > 65535 {
		return fmt.Errorf("tailnet: httpsPort %d is not a port between 1 and 65535", t.HTTPSPort)
	}
	if t.HTTPPort == t.HTTPSPort {
		return fmt.Errorf("tailnet: httpPort and httpsPort cannot both be %d", t.HTTPPort)
	}
	if t.Ephemeral && t.authKey() == "" {
		return errors.New("tailnet: ephemeral nodes are only created with an ephemeral auth key, set tailnet.authKey, TS_AUTHKEY or TS_AUTH_KEY")
	}
	return nil
}
