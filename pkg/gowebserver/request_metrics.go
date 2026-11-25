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
	"context"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// What answered a request, the "handler" of the HTTP server metrics.
// Everything under a served filesystem has the one route, so without it
// a directory listing, a thumbnail and a two hour video being watched
// are a single latency figure.
const (
	handlerFile      = "file"      // a file's bytes, or whatever else the file server answers
	handlerListing   = "listing"   // the file browser's page for a directory
	handlerThumbnail = "thumbnail" // a resized image or video frame
	handlerRichView  = "richview"  // the text viewer
	handlerVideoMeta = "videometa" // a video's metadata, preview sprite or subtitles as WebVTT (videometa.go)
	handlerMediaInfo = "mediainfo" // a photo's, video's or song's details for the info panel, and its world map (mediainfo.go)
	handlerUpload    = "upload"
	handlerProxy     = "proxy" // passed on to another server
	handlerIndex     = "index" // the list of served paths
	handlerDebug     = "debug"
)

// durationSeconds are the histogram buckets for how long the server's
// own work takes: from a page that only needed a template to a folder of
// tens of thousands of files on a slow disk.
var durationSeconds = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// tracePropagator carries a trace across servers in the W3C traceparent
// header: a request that arrives with one continues its caller's trace,
// and the reverse proxy sends one on.
var tracePropagator = propagation.TraceContext{}

func handlerAttr(name string) attribute.KeyValue {
	return attribute.String("handler", name)
}

// requestInfo is what a request's handlers say about it, for the metrics
// recorded around them.
type requestInfo struct {
	handler string
}

type requestInfoKey struct{}

// setRequestHandler says which handler is answering r (see the handler
// constants). The last one to say so before the response starts wins. It
// does nothing for a request that isn't being measured.
func setRequestHandler(r *http.Request, name string) {
	if info, ok := r.Context().Value(requestInfoKey{}).(*requestInfo); ok {
		info.handler = name
	}
}

// requestMetrics is what otelhttp's request metrics leave out: the
// requests being served now, how long each took to start answering, and
// which handler answered. It also puts that handler on otelhttp's own
// metrics, as the label "handler".
type requestMetrics struct {
	// Requests being served now.
	active metric.Int64UpDownCounter
	// From the request arriving to the first byte of the response. Unlike
	// the request's duration it leaves out sending the body, which for a
	// large file is as long as the client takes to download it.
	firstByte metric.Float64Histogram
}

func newRequestMetrics(mc *monitoringContext) (*requestMetrics, error) {
	m := mc.getMeterProvider().Meter("request")
	rm := &requestMetrics{}
	var err error
	if rm.active, err = m.Int64UpDownCounter("http_server_active_requests",
		metric.WithDescription("Requests being served now, by handler as first routed (a served path is file until answered).")); err != nil {
		return nil, err
	}
	if rm.firstByte, err = m.Float64Histogram("http_server_time_to_first_byte_seconds",
		metric.WithDescription("From a request arriving to the first byte of its response, by handler (file, listing, thumbnail, videometa, richview, upload, proxy, index, debug)."),
		metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(durationSeconds...)); err != nil {
		return nil, err
	}
	return rm, nil
}

// wrap measures the requests next serves. They are handler's unless next
// says otherwise with setRequestHandler. It goes inside otelhttp's
// handler, whose metrics and span it labels.
func (m *requestMetrics) wrap(next http.Handler, handler string) http.Handler {
	routed := metric.WithAttributes(handlerAttr(handler))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		info := &requestInfo{handler: handler}
		ctx := context.WithValue(r.Context(), requestInfoKey{}, info)
		// A request that was canceled still counts.
		mctx := context.WithoutCancel(ctx)
		m.active.Add(mctx, 1, routed)
		fw := &firstByteWriter{ResponseWriter: w}
		fw.onFirstByte = func() {
			m.firstByte.Record(mctx, time.Since(start).Seconds(), metric.WithAttributes(handlerAttr(info.handler)))
		}
		defer func() {
			// A handler that wrote nothing has answered by returning.
			fw.first()
			m.active.Add(mctx, -1, routed)
			labeler, _ := otelhttp.LabelerFromContext(ctx)
			labeler.Add(handlerAttr(info.handler))
			trace.SpanFromContext(ctx).SetAttributes(handlerAttr(info.handler))
		}()
		next.ServeHTTP(fw, r.WithContext(ctx))
	})
}

// firstByteWriter calls onFirstByte when the response starts: at its
// header, or its first write when the header is left to that. It passes
// on the ways of writing that http.ServeContent and the reverse proxy
// look for, so a file is still sent with sendfile.
type firstByteWriter struct {
	http.ResponseWriter
	onFirstByte func()
	started     bool
}

func (w *firstByteWriter) first() {
	if !w.started {
		w.started = true
		w.onFirstByte()
	}
}

func (w *firstByteWriter) WriteHeader(code int) {
	// 1xx responses come before the answer.
	if code >= http.StatusOK {
		w.first()
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *firstByteWriter) Write(b []byte) (int, error) {
	w.first()
	return w.ResponseWriter.Write(b)
}

func (w *firstByteWriter) ReadFrom(r io.Reader) (int64, error) {
	w.first()
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(r)
	}
	// Only Write, or io.Copy would come straight back here.
	return io.Copy(struct{ io.Writer }{w.ResponseWriter}, r)
}

func (w *firstByteWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap is for http.ResponseController.
func (w *firstByteWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// stageMetrics times the stages of a handler's work: each is a span
// under the request's, and an observation in a histogram labeled
// "stage".
type stageMetrics struct {
	tracer   trace.Tracer
	name     string // of the handler; span names are name.stage
	duration metric.Float64Histogram
}

// newStageMetrics times the stages of the handler name in the histogram
// name_stage_duration_seconds.
func newStageMetrics(mc *monitoringContext, name, description string) (*stageMetrics, metric.Meter, error) {
	m := mc.getMeterProvider().Meter(name)
	duration, err := m.Float64Histogram(name+"_stage_duration_seconds",
		metric.WithDescription(description),
		metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(durationSeconds...))
	if err != nil {
		return nil, nil, err
	}
	return &stageMetrics{tracer: mc.getTraceProvider().Tracer(name), name: name, duration: duration}, m, nil
}

// stage starts a stage. The function it returns ends it and records how
// long it took.
func (m *stageMetrics) stage(ctx context.Context, stage string) (context.Context, trace.Span, func()) {
	start := time.Now()
	ctx, span := m.tracer.Start(ctx, m.name+"."+stage)
	return ctx, span, func() {
		span.End()
		m.duration.Record(context.WithoutCancel(ctx), time.Since(start).Seconds(), metric.WithAttributes(attribute.String("stage", stage)))
	}
}
