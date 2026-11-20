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
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Instrumentation is what a [WebServer] reports to and adds around its
// own routes; the standalone server (package server) makes it from its
// configuration: Prometheus and OTLP exporters, pprof, CORS. This
// package imports none of those, so a program that embeds a [Handler]
// doesn't link them.
type Instrumentation struct {
	// MeterProvider and TracerProvider receive the server's metrics and
	// spans. Nil uses the otel globals.
	MeterProvider  metric.MeterProvider
	TracerProvider trace.TracerProvider
	// Handlers are more routes on the server's mux, such as /metrics and
	// the debug endpoints.
	Handlers map[string]http.Handler
	// Wrap, when set, wraps the whole mux (after compression), as CORS
	// does.
	Wrap func(http.Handler) http.Handler
	// Shutdown is called when the server stops.
	Shutdown func()
}

// monitoringContext is where the server reports what it does: metrics,
// traces, logs and diagnostics.
type monitoringContext struct {
	// handlers are more routes (Instrumentation.Handlers).
	handlers map[string]http.Handler
	// meterProvider and tracerProvider, when set, are used; else the otel
	// globals are, which only a standalone server made by New without
	// Instrumentation does. An embedded handler always sets both, to no-op
	// providers if need be.
	meterProvider  metric.MeterProvider
	tracerProvider trace.TracerProvider
	// logger is where everything is logged; nil is slog's default
	// logger, which the standalone server sets up.
	logger *slog.Logger
	// diag is the diagnostics log, nil when they're off.
	diag *diagLog
	// stop is Instrumentation.Shutdown.
	stop func()
}

func (m *monitoringContext) getTraceProvider() trace.TracerProvider {
	if m == nil || m.tracerProvider == nil {
		return otel.GetTracerProvider()
	}
	return m.tracerProvider
}

func (m *monitoringContext) getMeterProvider() metric.MeterProvider {
	if m == nil || m.meterProvider == nil {
		return otel.GetMeterProvider()
	}
	return m.meterProvider
}

// log is the logger to log to.
func (m *monitoringContext) log() *slog.Logger {
	if m == nil || m.logger == nil {
		return slog.Default()
	}
	return m.logger
}

// diagnostics is the diagnostics log, nil when they're off.
func (m *monitoringContext) diagnostics() *diagLog {
	if m == nil {
		return nil
	}
	return m.diag
}

// shutdown stops what Instrumentation started, once.
func (m *monitoringContext) shutdown() {
	if m == nil || m.stop == nil {
		return
	}
	stop := m.stop
	m.stop = nil
	stop()
}
