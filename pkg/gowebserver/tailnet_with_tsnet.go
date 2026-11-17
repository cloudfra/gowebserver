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
	"fmt"
	"log/slog"
	"net/http"

	"go.uber.org/zap"
	"tailscale.com/tsnet"
)

// tailnetSupported says this build can embed a Tailscale node.
const tailnetSupported = true

// serveTailnet joins the tailnet described by t and serves handler on it,
// returning a cleanup that leaves the tailnet again.
func serveTailnet(t Tailnet, handler http.Handler) (func() error, error) {
	srv := &tsnet.Server{
		Hostname:  t.Hostname,
		AuthKey:   t.authKey(),
		Dir:       t.StateDir,
		Ephemeral: t.Ephemeral,
		Logf:      zap.S().Debugf,
		UserLogf:  tailnetUserLogf,
	}

	// Listen announces the port on the tailnet and starts the node. A node
	// still waiting for a login does not block it, and says what it waits for
	// on UserLogf.
	httpListener, err := srv.Listen("tcp", fmt.Sprintf(":%d", t.HTTPPort))
	if err != nil {
		return nil, fmt.Errorf("tailnet: %w", err)
	}
	go func() {
		checkError(http.Serve(httpListener, handler))
	}()

	// ListenTLS first waits for the node to be running, which a node waiting
	// for a login never becomes, so it runs apart from everything else.
	go func() {
		httpsListener, err := srv.ListenTLS("tcp", fmt.Sprintf(":%d", t.HTTPSPort))
		if err != nil {
			checkError(fmt.Errorf("tailnet HTTPS: %w", err))
			return
		}
		checkError(http.Serve(httpsListener, handler))
	}()

	slog.Info("Serving on tailnet",
		"httpPort", t.HTTPPort,
		"httpsPort", t.HTTPSPort,
		"stateDir", srv.GetRootPath(),
	)

	return srv.Close, nil
}

// tailnetUserLogf routes tsnet's own status lines, the interactive login URL
// among them, into the server's log.
func tailnetUserLogf(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...))
}
