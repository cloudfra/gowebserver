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

// https://astaxie.gitbooks.io/build-web-application-with-golang/content/en/04.5.html
// http://sanatgersappa.blogspot.com/2013/03/handling-multiple-file-uploads-in-go.html
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cloudfra/gowebserver/internal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var uploadHTML = mustLoadTemplate("upload")

const (
	uploadFileFormName = "gowebserveruploadfile[]"
)

type uploadHTTPHandler struct {
	tp                 trace.TracerProvider
	uploadHTTPPath     string
	uploadDirectory    string
	uploadedBytesTotal metric.Int64Counter
	uploadedFilesTotal metric.Int64Counter
	// How long an upload request took, by result.
	requestDuration metric.Float64Histogram
	tmpl            *template.Template
	log             *slog.Logger
	chrome          pageChrome
}

type uploadResponse struct {
	Success bool  `json:"success"`
	Error   error `json:"error,omitempty"`
}

const (
	uploadTraceName = "uploadHTTPHandler"
)

// What an upload came to, the "result" of upload_request_duration_seconds.
const (
	uploadOK       = "ok"
	uploadRejected = "rejected" // the request's fault: another origin, or not a form
	uploadFailed   = "failed"   // the server couldn't store a file
)

// uploadSeconds are the histogram buckets for how long uploads take,
// which is mostly how long the client takes to send the files.
var uploadSeconds = []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 300, 900}

func (uh *uploadHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uploadTracer := uh.tp.Tracer(uploadTraceName)
	ctx, span := uploadTracer.Start(r.Context(), r.Method)
	defer span.End()

	logger := uh.log

	if r.Method == http.MethodGet {
		crutime := time.Now().Unix()
		h := sha256.New()
		if _, err := io.WriteString(h, strconv.FormatInt(crutime, 10)); err != nil {
			logger.Error("failed to write upload token seed to hash", "crutime", crutime, "error", err)
		}
		token := fmt.Sprintf("%x", h.Sum(nil))

		params := struct {
			UploadHTTPPath     string
			UploadToken        string
			UploadFileFormName string
			ApplicationVersion string
			Chrome             pageChrome
		}{uh.uploadHTTPPath, token, uploadFileFormName, internal.Version(), uh.chrome}

		if err := uh.tmpl.Execute(w, params); err != nil {
			logger.Error("cannot execute upload.html template.", "error", err)
		}
	} else {
		var resp uploadResponse
		start := time.Now()
		result := uploadFailed
		defer func() {
			span.SetAttributes(attribute.String("result", result))
			uh.requestDuration.Record(context.WithoutCancel(ctx), time.Since(start).Seconds(), metric.WithAttributes(attribute.String("result", result)))
		}()

		if !isSameOrigin(r) {
			result = uploadRejected
			resp.Error = fmt.Errorf("forbidden: cross-origin upload requests are not allowed")
			writeUploadResponse(w, resp, http.StatusForbidden, logger, span)
			return
		}

		ctx, childSpan := uploadTracer.Start(ctx, "ParseMultipartForm")
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			result = uploadRejected
			resp.Error = fmt.Errorf("InternalError: cannot parse multi-part form")
			writeUploadResponse(w, resp, http.StatusBadRequest, logger, childSpan)
			childSpan.End()
			return
		}

		childSpan.End()
		m := r.MultipartForm
		files := m.File[uploadFileFormName]
		for i := range files {
			fileName := sanitizeFileName(files[i].Filename)

			ctx, childSpan := uploadTracer.Start(ctx, "upload.file", trace.WithAttributes(attribute.String("filename", fileName)))
			defer childSpan.End()
			span.AddEvent("create file", trace.WithAttributes(attribute.String("filename", fileName)))

			file, err := files[i].Open()
			if err != nil {
				resp.Error = fmt.Errorf("InternalError: Cannot download file (%s), %w", fileName, err)
				writeUploadResponse(w, resp, http.StatusInternalServerError, logger, childSpan)
				return
			}
			defer func() {
				if err := file.Close(); err != nil {
					logger.Error("failed to close uploaded multipart file", "error", errCategory(err))
				}
			}()

			localPath := filepath.Join(uh.uploadDirectory, fileName)

			if err := ensureDirs(localPath); err != nil {
				resp.Error = fmt.Errorf("InternalError: Cannot create directory to store file (%s), %w", uh.uploadDirectory, err)
				writeUploadResponse(w, resp, http.StatusInternalServerError, logger, childSpan)
				return
			}

			f, err := os.Create(filepath.Clean(localPath))
			if err != nil {
				resp.Error = fmt.Errorf("InternalError: Cannot create file (%s), %w", localPath, err)
				writeUploadResponse(w, resp, http.StatusInternalServerError, logger, childSpan)
				return
			}
			defer func() {
				if err := f.Close(); err != nil {
					logger.Error("failed to close uploaded destination file", "error", errCategory(err))
				}
			}()
			bytesWritten, err := io.Copy(f, file)
			if err != nil {
				resp.Error = fmt.Errorf("InternalError: Cannot write file (%s), %w", localPath, err)
				writeUploadResponse(w, resp, http.StatusInternalServerError, logger, childSpan)
				return
			}
			childSpan.SetAttributes(attribute.Int64("bytesWritten", bytesWritten))
			uh.uploadedBytesTotal.Add(ctx, bytesWritten)
			uh.uploadedFilesTotal.Add(ctx, 1)
			logger.Debug("upload complete", "bytes", bytesWritten)
		}

		result = uploadOK
		resp.Success = true
		writeUploadResponse(w, resp, http.StatusOK, logger, span)
	}
}

func newUploadHandler(mc *monitoringContext, uploadHTTPPath string, uploadDirectory string, chrome pageChrome) (http.Handler, error) {
	m := mc.getMeterProvider().Meter("upload")

	uploadedBytesTotal, err := m.Int64Counter("uploaded_bytes_total", metric.WithDescription("Number of bytes uploaded."), metric.WithUnit("bytes"))
	if err != nil {
		return nil, err
	}
	uploadedFilesTotal, err := m.Int64Counter("uploaded_files_total", metric.WithDescription("Number of files uploaded."))
	if err != nil {
		return nil, err
	}
	requestDuration, err := m.Float64Histogram("upload_request_duration_seconds",
		metric.WithDescription("How long an upload request took, receiving the files included, by result (ok, rejected, failed)."),
		metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(uploadSeconds...))
	if err != nil {
		return nil, err
	}
	tmpl, err := createTemplate(uploadHTML)
	if err != nil {
		return nil, err
	}
	return &uploadHTTPHandler{
		requestDuration:    requestDuration,
		tp:                 mc.getTraceProvider(),
		uploadHTTPPath:     uploadHTTPPath,
		uploadDirectory:    uploadDirectory,
		uploadedBytesTotal: uploadedBytesTotal,
		uploadedFilesTotal: uploadedFilesTotal,
		tmpl:               tmpl,
		log:                mc.log(),
		chrome:             chrome,
	}, nil
}

// isSameOrigin reports whether the request's Origin header (if present)
// matches the request's own Host, to guard the upload endpoint against
// cross-site form submissions. Requests without an Origin header (e.g.
// same-origin form posts from older browsers, or non-browser clients) are
// allowed through since they cannot be issued cross-site by a browser in a
// way that this check would catch.
func isSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(originURL.Host, r.Host)
}

func writeUploadResponse(w http.ResponseWriter, resp uploadResponse, statusCode int, logger *slog.Logger, span trace.Span) {
	if resp.Error != nil {
		logger.Warn("upload failed", "error", errCategory(resp.Error))
		span.RecordError(resp.Error)
	} else {
		logger.Debug("Upload Successful")
	}
	data, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Malformed server response.", http.StatusInternalServerError)
		logger.Warn("Cannot marshal upload JSON response", "error", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusCode)
	if _, err := w.Write(data); err != nil {
		logger.Warn("cannot write upload response", "error", err)
	}
}
