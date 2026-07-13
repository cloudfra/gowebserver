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

//go:build !(plan9 || js || aix)

package gowebserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// fakeOTLPServer returns a test server that accepts any POST and records receipt.
func fakeOTLPServer(t *testing.T) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	received := make(chan struct{}, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, received
}

func TestNewTraceProvider_NoURI(t *testing.T) {
	tp, err := newTraceProvider(Monitoring{}, resource.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if tp != nil {
		t.Error("expected nil provider when trace URI is empty")
	}
}

func TestNewTraceProvider_ExportsSpans(t *testing.T) {
	srv, received := fakeOTLPServer(t)

	m := Monitoring{Trace: Trace{URI: srv.URL}}
	tp, err := newTraceProvider(m, resource.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if tp == nil {
		t.Fatal("expected non-nil trace provider")
	}
	defer tp.Shutdown(context.Background())

	_, span := tp.Tracer("test").Start(context.Background(), "export-test")
	span.End()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tp.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case <-received:
		// spans reached the OTLP endpoint
	case <-time.After(5 * time.Second):
		t.Error("timed out waiting for spans to reach the OTLP endpoint")
	}
}

// TestNewTraceProvider_SpanProcessorRegistered verifies that a SpanProcessor
// passed to newTraceProvider is actually wired into the tracer provider via
// sdktrace.WithSpanProcessor.
func TestNewTraceProvider_SpanProcessorRegistered(t *testing.T) {
	srv, _ := fakeOTLPServer(t)

	recorder := tracetest.NewSpanRecorder()
	m := Monitoring{Trace: Trace{URI: srv.URL}}
	tp, err := newTraceProvider(m, resource.Default(), recorder)
	if err != nil {
		t.Fatal(err)
	}
	if tp == nil {
		t.Fatal("expected non-nil trace provider")
	}
	defer tp.Shutdown(context.Background())

	_, span := tp.Tracer("test").Start(context.Background(), "recorded-span")
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("span processor captured %d spans, want 1", len(ended))
	}
	if got := ended[0].Name(); got != "recorded-span" {
		t.Errorf("got span name %q, want %q", got, "recorded-span")
	}
}

// TestSetupMonitoring_WithTrace exercises the full setupMonitoring path with an
// OTLP trace endpoint. It verifies:
//  1. The /tracez debug handler is registered when DebugEndpoint is set.
//  2. The zpages SpanProcessor is wired into the tracer provider so that spans
//     created through that provider appear on the /tracez page.
func TestSetupMonitoring_WithTrace(t *testing.T) {
	srv, _ := fakeOTLPServer(t)

	m := Monitoring{
		DebugEndpoint: "/debug",
		Trace: Trace{
			Enabled: true,
			URI:     srv.URL,
		},
	}

	mc, err := setupMonitoring(m)
	if err != nil {
		t.Fatal(err)
	}
	defer mc.shutdown()

	// The /debug/tracez handler must be registered.
	tracezHandler, ok := mc.handlers["/debug/tracez"]
	if !ok {
		t.Fatal("expected /debug/tracez handler to be registered")
	}

	// Create a span through the monitored provider so the zpages processor sees it.
	tracer := mc.getTraceProvider().Tracer("test")
	_, span := tracer.Start(context.Background(), "zpages-test-span")
	span.End()

	// Render the tracez page and verify the span name is present.
	req, _ := http.NewRequest(http.MethodGet, "/debug/tracez", nil)
	rr := httptest.NewRecorder()
	tracezHandler.ServeHTTP(rr, req)

	if !strings.Contains(rr.Body.String(), "zpages-test-span") {
		t.Errorf("/debug/tracez page does not mention the span name\nbody:\n%s", rr.Body.String())
	}
}

// TestSetupMonitoring_NilSpanProcessorWhenNoDebugEndpoint ensures that when
// DebugEndpoint is empty, newTraceProvider is called with a nil SpanProcessor
// and still produces a working provider.
func TestSetupMonitoring_NilSpanProcessorWhenNoDebugEndpoint(t *testing.T) {
	srv, _ := fakeOTLPServer(t)

	m := Monitoring{
		Trace: Trace{
			Enabled: true,
			URI:     srv.URL,
		},
	}

	mc, err := setupMonitoring(m)
	if err != nil {
		t.Fatal(err)
	}
	defer mc.shutdown()

	if _, ok := mc.handlers["/debug/tracez"]; ok {
		t.Error("did not expect /debug/tracez handler when DebugEndpoint is empty")
	}

	if mc.getTraceProvider() == nil {
		t.Error("expected a non-nil trace provider")
	}

	// Verify spans can still be created without panicking.
	_, span := mc.getTraceProvider().Tracer("test").Start(context.Background(), "no-debug-span")
	span.End()
}
