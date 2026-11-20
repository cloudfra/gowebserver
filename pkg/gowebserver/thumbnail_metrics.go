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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// What a thumbnail request came to, the "result" of
// thumbnail_requests_total and thumbnail_request_duration_seconds.
const (
	thumbnailCached      = "cached"      // it was in the store
	thumbnailMade        = "made"        // it was made for this request, or one it shared the work with
	thumbnailUnsupported = "unsupported" // the file isn't one a thumbnail can be made of
	thumbnailFailed      = "failed"
	thumbnailCanceled    = "canceled" // the client left before it was ready
)

// The stages of making a thumbnail, the "stage" of
// thumbnail_stage_duration_seconds and the names of the spans under a
// request's "thumbnail" span (as thumbnail.wait and so on).
const (
	thumbnailStageWait   = "wait"   // for one of the render slots
	thumbnailStageDecode = "decode" // the photo, or ffmpeg for a video's frame; all of it when ffmpeg makes the whole thumbnail
	thumbnailStageScale  = "scale"
	thumbnailStageEncode = "encode"
	thumbnailStageStore  = "store"
)

// thumbnailSeconds are the histogram buckets for how long thumbnails
// take: from one read back from the store to a large photo decoded on a
// slow machine.
var thumbnailSeconds = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// thumbnailMegapixels are the histogram buckets for the size of the
// pictures thumbnails are made from, which is what decoding time
// follows.
var thumbnailMegapixels = []float64{0.5, 1, 2, 4, 8, 12, 16, 24, 32, 48, 64, 100}

// thumbnailMetrics says where the time making thumbnails goes. Everything
// is labeled with the kind of file, photo or video.
type thumbnailMetrics struct {
	tracer trace.Tracer
	// Requests by result, and how long each took start to finish.
	requests        metric.Int64Counter
	requestDuration metric.Float64Histogram
	// How long each stage of making one took.
	stageDuration metric.Float64Histogram
	// Megapixels of the source pictures decoded.
	sourceSize metric.Float64Histogram
	// Thumbnails being made right now, by state: waiting for a render
	// slot, or running in one.
	renders metric.Int64UpDownCounter
	// Thumbnails whose every request left while they were being made:
	// work started and thrown away.
	abandoned metric.Int64Counter
	// How long asking the store for a thumbnail took, by whether it was
	// there. Every thumbnail request starts with one.
	lookupDuration metric.Float64Histogram
	// Bytes of thumbnails put in the store, which nothing takes out
	// again: the rate the store grows at.
	storedBytes metric.Int64Counter
	// How long each run of ffmpeg took, by what it was asked for, how it
	// was given the video and how it ended. A video can take several.
	ffmpegDuration metric.Float64Histogram
	// Bytes of videos copied out of archives to temporary files, for
	// ffmpeg to seek in.
	videoCopyBytes metric.Int64Counter

	meter metric.Meter
}

// What ffmpeg was run for, the "operation" of
// thumbnail_ffmpeg_duration_seconds and the names of the spans under
// thumbnail.decode and thumbnail.encode (as ffmpeg.frame and so on).
const (
	ffmpegFrame    = "frame"    // a video's frame, or its whole thumbnail
	ffmpegDuration = "duration" // a video's length
	ffmpegPhoto    = "photo"    // a photo, or its whole thumbnail
	ffmpegEncode   = "encode"   // a thumbnail made in Go as WebP
)

// How a run of ffmpeg ended, the "outcome" of
// thumbnail_ffmpeg_duration_seconds.
const (
	ffmpegOK       = "ok"
	ffmpegNothing  = "nothing"  // it ran, but had no frame or duration to give
	ffmpegTimeout  = "timeout"  // it was stopped after thumbnailVideoTimeout
	ffmpegCanceled = "canceled" // the thumbnail's requests all left
	ffmpegError    = "error"
)

// ffmpegOutcome is how a run of ffmpeg under run, a context with the
// run's timeout made from parent, ended with err.
func ffmpegOutcome(parent, run context.Context, err error) string {
	switch {
	case err == nil:
		return ffmpegOK
	case parent.Err() != nil:
		return ffmpegCanceled
	case errors.Is(run.Err(), context.DeadlineExceeded):
		return ffmpegTimeout
	case errors.Is(err, ffmpeg.ErrNoFrame), errors.Is(err, ffmpeg.ErrNoDuration):
		return ffmpegNothing
	}
	return ffmpegError
}

func newThumbnailMetrics(mc *monitoringContext) (*thumbnailMetrics, error) {
	m := mc.getMeterProvider().Meter("thumbnail")
	tm := &thumbnailMetrics{tracer: mc.getTraceProvider().Tracer("thumbnail")}
	var err error
	if tm.requests, err = m.Int64Counter("thumbnail_requests_total",
		metric.WithDescription("Thumbnail requests, by kind of file (photo, video) and result (cached, made, unsupported, failed, canceled).")); err != nil {
		return nil, err
	}
	if tm.requestDuration, err = m.Float64Histogram("thumbnail_request_duration_seconds",
		metric.WithDescription("How long a thumbnail request took, by kind of file and result."),
		metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(thumbnailSeconds...)); err != nil {
		return nil, err
	}
	if tm.stageDuration, err = m.Float64Histogram("thumbnail_stage_duration_seconds",
		metric.WithDescription("How long each stage of making a thumbnail took: wait (for a render slot), decode, scale, encode, store."),
		metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(thumbnailSeconds...)); err != nil {
		return nil, err
	}
	if tm.sourceSize, err = m.Float64Histogram("thumbnail_source_megapixels",
		metric.WithDescription("Size of the pictures thumbnails were made from, in megapixels."),
		metric.WithExplicitBucketBoundaries(thumbnailMegapixels...)); err != nil {
		return nil, err
	}
	if tm.renders, err = m.Int64UpDownCounter("thumbnail_renders",
		metric.WithDescription("Thumbnails being made now, by state: waiting for a render slot, or running.")); err != nil {
		return nil, err
	}
	if tm.abandoned, err = m.Int64Counter("thumbnail_abandoned_total",
		metric.WithDescription("Thumbnails every request left while they were being made.")); err != nil {
		return nil, err
	}
	if tm.lookupDuration, err = m.Float64Histogram("thumbnail_store_lookup_duration_seconds",
		metric.WithDescription("How long asking the store for a thumbnail took, by result (hit, miss)."),
		metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(thumbnailSeconds...)); err != nil {
		return nil, err
	}
	if tm.storedBytes, err = m.Int64Counter("thumbnail_stored_bytes_total",
		metric.WithDescription("Bytes of thumbnails put in the store, by kind of file. Nothing is evicted, so this is how fast the store grows.")); err != nil {
		return nil, err
	}
	if tm.ffmpegDuration, err = m.Float64Histogram("thumbnail_ffmpeg_duration_seconds",
		metric.WithDescription("How long each run of ffmpeg took, by operation (frame, duration, photo, encode, probe, sprite), input (file, pipe) and outcome (ok, nothing, timeout, canceled, error)."),
		metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(thumbnailSeconds...)); err != nil {
		return nil, err
	}
	if tm.videoCopyBytes, err = m.Int64Counter("thumbnail_video_copy_bytes_total",
		metric.WithDescription("Bytes of videos copied out of archives to temporary files so ffmpeg could seek in them.")); err != nil {
		return nil, err
	}
	tm.meter = m
	return tm, nil
}

// observe reports what doesn't change with each thumbnail: how many can
// be made at once, and how large the store's database is.
func (m *thumbnailMetrics) observe(store *thumbnailStore, slots int) error {
	if _, err := m.meter.Int64ObservableGauge("thumbnail_render_slots",
		metric.WithDescription("Thumbnails that can be made at once. thumbnail_renders{state=\"running\"} at this number means the rest are waiting."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(slots))
			return nil
		})); err != nil {
		return err
	}
	_, err := m.meter.Int64ObservableGauge("thumbnail_store_size_bytes",
		metric.WithDescription("Size of the thumbnail store's database file. Absent when thumbnails are kept in memory."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			if size, ok := store.size(); ok {
				o.Observe(size)
			}
			return nil
		}))
	return err
}

// lookup starts asking the store for a thumbnail. The function it
// returns ends that, given whether the thumbnail was there.
func (m *thumbnailMetrics) lookup(ctx context.Context) func(hit bool) {
	start := time.Now()
	_, span := m.tracer.Start(ctx, "thumbnail.lookup")
	return func(hit bool) {
		result := "miss"
		if hit {
			result = "hit"
		}
		span.SetAttributes(attribute.String("result", result))
		span.End()
		m.lookupDuration.Record(context.WithoutCancel(ctx), time.Since(start).Seconds(), metric.WithAttributes(attribute.String("result", result)))
	}
}

// ffmpegRun starts a run of ffmpeg for operation, with the video piped
// to it or not, as a span that *ctx becomes the context of. The function
// it returns ends the run, given its outcome.
func (m *thumbnailMetrics) ffmpegRun(ctx *context.Context, operation string, piped bool) func(outcome string) {
	start := time.Now()
	input := "file"
	if piped {
		input = "pipe"
	}
	sctx, span := m.tracer.Start(*ctx, "ffmpeg."+operation, trace.WithAttributes(attribute.String("input", input)))
	*ctx = sctx
	return func(outcome string) {
		span.SetAttributes(attribute.String("outcome", outcome))
		span.End()
		m.ffmpegDuration.Record(context.WithoutCancel(sctx), time.Since(start).Seconds(), metric.WithAttributes(
			attribute.String("operation", operation), attribute.String("input", input), attribute.String("outcome", outcome)))
	}
}

func thumbnailKind(video bool) string {
	if video {
		return "video"
	}
	return "photo"
}

func kindAttr(kind string) attribute.KeyValue {
	return attribute.String("kind", kind)
}

// request records a thumbnail request that began at start.
func (m *thumbnailMetrics) request(ctx context.Context, kind, result string, start time.Time) {
	attrs := metric.WithAttributes(kindAttr(kind), attribute.String("result", result))
	// A request that was canceled still counts.
	ctx = context.WithoutCancel(ctx)
	m.requests.Add(ctx, 1, attrs)
	m.requestDuration.Record(ctx, time.Since(start).Seconds(), attrs)
}

// thumbnailTiming is the stages one thumbnail went through and how long
// each took, for the Server-Timing header of the responses that waited
// for it: a browser's network panel then shows where a slow thumbnail's
// time went.
type thumbnailTiming []string

func (t thumbnailTiming) header() string {
	return strings.Join(t, ", ")
}

// stage starts a stage of making a thumbnail of the given kind: a span
// under ctx's and a state of thumbnail_renders. The function it returns
// ends the stage, records how long it took and adds it to timing.
func (m *thumbnailMetrics) stage(ctx context.Context, kind, stage string, timing *thumbnailTiming) (context.Context, trace.Span, func()) {
	start := time.Now()
	ctx, span := m.tracer.Start(ctx, "thumbnail."+stage)
	state := "running"
	if stage == thumbnailStageWait {
		state = "waiting"
	}
	// The render carries on after its first request is canceled.
	mctx := context.WithoutCancel(ctx)
	inState := metric.WithAttributes(kindAttr(kind), attribute.String("state", state))
	m.renders.Add(mctx, 1, inState)
	return ctx, span, func() {
		d := time.Since(start)
		span.End()
		m.renders.Add(mctx, -1, inState)
		m.stageDuration.Record(mctx, d.Seconds(), metric.WithAttributes(kindAttr(kind), attribute.String("stage", stage)))
		*timing = append(*timing, fmt.Sprintf("%s;dur=%.1f", stage, float64(d.Microseconds())/1000))
	}
}
