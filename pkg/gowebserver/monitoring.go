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
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/cloudfra/gowebserver/internal"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/contrib/zpages"
	"go.opentelemetry.io/otel"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

func setupMonitoring(m Monitoring) (*monitoringContext, error) {
	mc := &monitoringContext{
		handlers: map[string]http.Handler{},
	}

	if len(m.DebugEndpoint) > 0 {
		mc.handlers[m.DebugEndpoint+"/pprof/"] = http.HandlerFunc(pprof.Index)
		mc.handlers[m.DebugEndpoint+"/pprof/cmdline"] = http.HandlerFunc(pprof.Cmdline)
		mc.handlers[m.DebugEndpoint+"/pprof/profile"] = http.HandlerFunc(pprof.Profile)
		mc.handlers[m.DebugEndpoint+"/pprof/symbol"] = http.HandlerFunc(pprof.Symbol)
		mc.handlers[m.DebugEndpoint+"/pprof/trace"] = http.HandlerFunc(pprof.Trace)
	}

	r, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("gowebserver"),
			semconv.ServiceVersionKey.String(internal.Version()),
		),
	)
	if err != nil {
		mc.shutdown()
		return nil, err
	}

	var zsp *zpages.SpanProcessor
	if len(m.DebugEndpoint) > 0 {
		zsp = zpages.NewSpanProcessor()
	}

	// Convert to interface only when non-nil; a typed nil would satisfy
	// sp != nil in newTraceProvider and cause a nil-pointer dereference.
	var sp sdktrace.SpanProcessor
	if zsp != nil {
		sp = zsp
	}

	tp, err := newTraceProvider(m, r, sp)
	if err != nil {
		mc.shutdown()
		return nil, err
	}

	if tp != nil {
		mc.tp = tp
		if zsp != nil {
			mc.handlers[m.DebugEndpoint+"/tracez"] = zpages.NewTracezHandler(zsp)
		}
	}

	promExporter, promProvider, prom, err := newPrometheusExporter(m, r)
	if err != nil {
		mc.shutdown()
		return nil, err
	}
	if prom != nil {
		mc.promProvider = promProvider
		mc.promExporter = promExporter
		if m.Metrics.Path != "" {
			mc.handlers[m.Metrics.Path] = prom
		}

		if err := startHostMetrics(); err != nil {
			mc.shutdown()
			return nil, err
		}

		if err := runtime.Start(
			runtime.WithMeterProvider(promProvider),
			runtime.WithMinimumReadMemStatsInterval(10*time.Second),
		); err != nil {
			mc.shutdown()
			return nil, err
		}
	}

	return mc, nil
}

func newPrometheusExporter(m Monitoring, r *resource.Resource) (*otelprom.Exporter, *sdkmetric.MeterProvider, http.Handler, error) {
	registry := prometheus.NewRegistry()
	if err := registry.Register(collectors.NewBuildInfoCollector()); err != nil {
		return nil, nil, nil, err
	}
	if err := registry.Register(collectors.NewGoCollector()); err != nil {
		return nil, nil, nil, err
	}
	if err := registry.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{
		ReportErrors: true,
	})); err != nil {
		return nil, nil, nil, err
	}

	prometheusExporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		return nil, nil, nil, err
	}

	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(prometheusExporter))
	otel.SetMeterProvider(provider)

	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return prometheusExporter, provider, h, nil
}

type monitoringContext struct {
	handlers     map[string]http.Handler
	promExporter *otelprom.Exporter
	promProvider metric.MeterProvider
	tp           *sdktrace.TracerProvider
}

func (m *monitoringContext) getTraceProvider() trace.TracerProvider {
	if m == nil || m.tp == nil {
		return otel.GetTracerProvider()
	}
	return m.tp
}

func (m *monitoringContext) getMeterProvider() metric.MeterProvider {
	if m == nil || m.promProvider == nil {
		return otel.GetMeterProvider()
	}
	return m.promProvider
}

func (m *monitoringContext) shutdown() {
	if m == nil {
		return
	}
	ctx := context.Background()
	if m.promProvider != nil {
		m.promProvider = nil
	}
	if m.promExporter != nil {
		if err := m.promExporter.Shutdown(ctx); err != nil {
			zap.S().With("error", err).Error("cannot shutdown prometheus exporter")
		}
		m.promExporter = nil
	}
	if m.tp != nil {
		if err := m.tp.Shutdown(ctx); err != nil {
			zap.S().With("error", err).Error("cannot shutdown trace provider")
		}
	}
}
