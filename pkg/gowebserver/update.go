// Copyright 2022 Cloudfra
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
	"context"
	"log/slog"
	"net/http"

	"github.com/cloudfra/gowebserver/internal"
	"github.com/cloudfra/gowebserver/pkg/update"
)

// serveUpdates starts automatic updates when a track is set, and serves
// <debugEndpoint>/upgrade when the debug endpoint is on. It returns a
// function that stops the updates.
func (ws *webServerImpl) serveUpdates(mux *http.ServeMux) (func() error, error) {
	var u *update.Updater
	stop := func() error { return nil }
	if ws.update.Track != "" {
		var err error
		u, err = update.New(update.Options{Track: ws.update.Track, Current: internal.Version(), ManifestURL: ws.update.ManifestURL})
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		go u.Run(ctx)
		stop = func() error { cancel(); return nil }
		slog.Info("automatic updates are on", "track", ws.update.Track, "version", internal.Version(), "interval", update.Interval(ws.update.Track))
	}
	if ws.debugEndpoint != "" {
		endpoint := ws.debugEndpoint + "/upgrade"
		slog.Info("Endpoint", "http", endpoint)
		if u != nil {
			mux.Handle(endpoint, u)
		} else {
			mux.HandleFunc(endpoint, updatesOff)
		}
	}
	return stop, nil
}

// updatesOff serves <debugEndpoint>/upgrade when no track is set.
func updatesOff(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "automatic updates are off: set update.track to stable or unstable", http.StatusConflict)
}
