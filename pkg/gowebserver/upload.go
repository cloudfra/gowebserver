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
	"crypto/md5"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
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
	"go.uber.org/zap"
)

//go:embed upload.html
var uploadHTML []byte

const (
	uploadFileFormName = "gowebserveruploadfile[]"
)

type uploadHTTPHandler struct {
	tp                 trace.TracerProvider
	uploadHTTPPath     string
	uploadDirectory    string
	uploadedBytesTotal metric.Int64Counter
	uploadedFilesTotal metric.Int64Counter
	tmpl               *template.Template
}

type uploadResponse struct {
	Success bool  `json:"success"`
	Error   error `json:"error,omitempty"`
}

const (
	uploadTraceName = "uploadHTTPHandler"
)

func (uh *uploadHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uploadTracer := uh.tp.Tracer(uploadTraceName)
	ctx, span := uploadTracer.Start(r.Context(), r.Method)
	defer span.End()

	logger := zap.S().With("url", r.URL)

	if r.Method == http.MethodGet {
		crutime := time.Now().Unix()
		h := md5.New()
		io.WriteString(h, strconv.FormatInt(crutime, 10))
		token := fmt.Sprintf("%x", h.Sum(nil))

		params := struct {
			UploadHTTPPath     string
			UploadToken        string
			UploadFileFormName string
			ApplicationVersion string
		}{uh.uploadHTTPPath, token, uploadFileFormName, internal.Version()}

		if err := uh.tmpl.Execute(w, params); err != nil {
			logger.With("error", err).Error("cannot execute upload.html template.")
		}
	} else {
		var resp uploadResponse

		if !isSameOrigin(r) {
			resp.Error = fmt.Errorf("forbidden: cross-origin upload requests are not allowed")
			writeUploadResponse(w, resp, http.StatusForbidden, logger, span)
			return
		}

		ctx, childSpan := uploadTracer.Start(ctx, "ParseMultipartForm")
		if err := r.ParseMultipartForm(32 << 20); err != nil {
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

			ctx, childSpan := uploadTracer.Start(ctx, fileName)
			defer childSpan.End()
			span.AddEvent("create file", trace.WithAttributes(attribute.String("filename", fileName)))

			file, err := files[i].Open()
			if err != nil {
				resp.Error = fmt.Errorf("InternalError: Cannot download file (%s), %w", fileName, err)
				writeUploadResponse(w, resp, http.StatusInternalServerError, logger, childSpan)
				return
			}
			defer file.Close()

			localPath := filepath.Join(uh.uploadDirectory, fileName)

			if err := ensureDirs(localPath); err != nil {
				resp.Error = fmt.Errorf("InternalError: Cannot create directory to store file (%s), %w", uh.uploadDirectory, err)
				writeUploadResponse(w, resp, http.StatusInternalServerError, logger, childSpan)
				return
			}

			f, err := os.Create(localPath)
			if err != nil {
				resp.Error = fmt.Errorf("InternalError: Cannot create file (%s), %w", localPath, err)
				writeUploadResponse(w, resp, http.StatusInternalServerError, logger, childSpan)
				return
			}
			defer f.Close()
			bytesWritten, err := io.Copy(f, file)
			if err != nil {
				resp.Error = fmt.Errorf("InternalError: Cannot write file (%s), %w", localPath, err)
				writeUploadResponse(w, resp, http.StatusInternalServerError, logger, childSpan)
				return
			}
			childSpan.SetAttributes(attribute.Int64("bytesWritten", bytesWritten))
			uh.uploadedBytesTotal.Add(ctx, bytesWritten)
			uh.uploadedFilesTotal.Add(ctx, 1)
			logger.With("fileName", fileName).With("localPath", localPath).Info("Upload Complete")
		}

		resp.Success = true
		writeUploadResponse(w, resp, http.StatusOK, logger, span)
	}
}

func newUploadHandler(mc *monitoringContext, uploadHTTPPath string, uploadDirectory string) (http.Handler, error) {
	m := mc.getMeterProvider().Meter(uploadDirectory)

	uploadedBytesTotal, err := m.Int64Counter("uploaded_bytes_total", metric.WithDescription("Number of bytes uploaded."), metric.WithUnit("bytes"))
	if err != nil {
		return nil, err
	}
	uploadedFilesTotal, err := m.Int64Counter("uploaded_files_total", metric.WithDescription("Number of files uploaded."))
	if err != nil {
		return nil, err
	}
	tmpl, err := createTemplate(uploadHTML)
	if err != nil {
		return nil, err
	}
	return &uploadHTTPHandler{
		tp:                 mc.getTraceProvider(),
		uploadHTTPPath:     uploadHTTPPath,
		uploadDirectory:    uploadDirectory,
		uploadedBytesTotal: uploadedBytesTotal,
		uploadedFilesTotal: uploadedFilesTotal,
		tmpl:               tmpl,
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

func writeUploadResponse(w http.ResponseWriter, resp uploadResponse, statusCode int, logger *zap.SugaredLogger, span trace.Span) {
	if resp.Error != nil {
		logger.With("error", resp.Error).Warn("Upload error")
		span.RecordError(resp.Error)
	} else {
		logger.Debug("Upload Successful")
	}
	data, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Malformed server response.", http.StatusInternalServerError)
		logger.With("error", err).Warn("Cannot marshal upload JSON response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusCode)
	if _, err := w.Write(data); err != nil {
		logger.With("error", err).Warn("cannot write upload response")
	}
}
