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

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func newTraceProvider(m Monitoring, r *resource.Resource, sp sdktrace.SpanProcessor) (*sdktrace.TracerProvider, error) {
	if len(m.Trace.URI) == 0 {
		return nil, nil
	}

	exp, err := otlptracehttp.New(
		context.Background(),
		otlptracehttp.WithEndpointURL(m.Trace.URI),
	)
	if err != nil {
		return nil, err
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(r),
	}
	if sp != nil {
		opts = append(opts, sdktrace.WithSpanProcessor(sp))
	}

	return sdktrace.NewTracerProvider(opts...), nil
}
