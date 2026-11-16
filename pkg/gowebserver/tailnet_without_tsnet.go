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

//go:build js || wasip1 || dragonfly || netbsd

package gowebserver

import (
	"errors"
	"net/http"
)

// tailnetSupported says tsnet does not compile for this build, so its
// dependency cost stays out of the binary.
const tailnetSupported = false

// serveTailnet keeps the tailnet option linkable where tsnet does not build.
// validate refuses tailnet.enabled before this is reached.
func serveTailnet(t Tailnet, handler http.Handler) (func() error, error) {
	return nil, errors.New("tailnet: this binary was built for an operating system without an embedded Tailscale node")
}
