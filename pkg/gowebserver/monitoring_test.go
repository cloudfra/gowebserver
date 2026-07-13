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
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestMonitoringContext(t *testing.T) {
	registry := prometheus.NewRegistry()
	prometheusExporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		t.Fatal(err)
	}
	testCases := []struct {
		name string
		mc   *monitoringContext
	}{
		{
			name: "nil",
			mc:   nil,
		},
		{
			name: "full",
			mc: &monitoringContext{
				promExporter: prometheusExporter,
				promProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(prometheusExporter)),
				tp:           sdktrace.NewTracerProvider(),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.mc.getTraceProvider() == nil {
				t.Error("getTraceProvider is nil")
			}
			if tc.mc.getMeterProvider() == nil {
				t.Error("getMeterProvider is nil")
			}
			tc.mc.shutdown()
			tc.mc.shutdown()
		})
	}
}

func TestSetupMonitoring(t *testing.T) {
	testCases := []struct {
		name string
		m    Monitoring
	}{
		{
			name: "empty",
			m:    Monitoring{},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := setupMonitoring(tc.m); err != nil {
				t.Error(err)
			}
		})
	}
}
