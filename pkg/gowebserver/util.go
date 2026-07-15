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
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"go.uber.org/zap"
)

func checkError(err error) {
	if err != nil {
		zap.S().Error(err)
		zap.S().Sync()
	}
}

func createTempDirectory() (string, func(), error) {
	tmpDir, err := os.MkdirTemp(os.TempDir(), "gowebserver")
	if err != nil {
		return "", nilFunc, fmt.Errorf("cannot create temp directory, %w", err)
	}
	return tmpDir, func() {
		tryDeleteDirectory(tmpDir)
	}, nil
}

func tryDeleteDirectory(path string) {
	if !exists(path) {
		return
	}

	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		zap.S().With("error", err, "directory", path).Error("cannot delete directory")
	}
}

func downloadFile(path string) (string, func() error, error) {
	if strings.HasPrefix(strings.ToLower(path), "http") {
		cleanup := nilFuncWithError
		f, err := os.CreateTemp(os.TempDir(), "gowebserverdl")
		if err != nil {
			return "", cleanup, err
		}
		defer f.Close()
		fileName := f.Name()
		cleanup = func() error {
			return os.Remove(fileName)
		}
		resp, err := http.Get(path)
		if err != nil {
			return "", cleanup, err
		}
		defer resp.Body.Close()

		if _, err := io.Copy(f, resp.Body); err != nil {
			return "", cleanup, err
		}
		return f.Name(), cleanup, nil
	}
	return path, nilFuncWithError, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirPath(dirPath string) string {
	return strings.TrimRight(dirPath, "/") + "/"
}

func copyFile(reader io.Reader, createdTime time.Time, modifiedTime time.Time, filePath string) error {
	fsf, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("cannot create target file %s, %w", filePath, err)
	}
	defer fsf.Close()

	_, err = io.Copy(fsf, reader)
	if err != nil {
		os.Remove(fsf.Name())
		return fmt.Errorf("cannot copy to target file %s, %w", filePath, err)
	}
	return os.Chtimes(filePath, createdTime, modifiedTime)
}

var validChars = map[rune]interface{}{
	'.':  nil,
	'-':  nil,
	'_':  nil,
	' ':  nil,
	'\\': nil,
	'/':  nil,
	'$':  nil,
	'#':  nil,
}

func ensureDirs(fileName string) error {
	absFullPath, err := filepath.Abs(filepath.Clean(fileName))
	if err != nil {
		return err
	}

	fullBaseDir := filepath.Dir(absFullPath)
	return os.MkdirAll(fullBaseDir, 0o766)
}

func sanitizeFileName(fileName string) string {
	name := strings.ReplaceAll(filepath.Clean(fileName), "..", ".")
	var sanitized strings.Builder
	sanitized.Grow(len(name))
	for _, r := range name {
		if _, ok := validChars[r]; ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') || ok {
			sanitized.WriteRune(r)
		}
	}

	parts := strings.Split(strings.ReplaceAll(sanitized.String(), "\\", "/"), "/")
	sanitizedParts := []string{}
	for _, part := range parts {
		if strings.ReplaceAll(part, ".", "") != "" {
			sanitizedParts = append(sanitizedParts, part)
		}
	}

	result := strings.Join(sanitizedParts, "/")
	if result == "" {
		return "."
	}
	return result
}

func nilFunc() {
}

func nilFuncWithError() error {
	return nil
}

func createTemplate(tmplText []byte) (*template.Template, error) {
	tmpl := template.New("").Funcs(template.FuncMap{
		"humanizeBytes":     humanize.Bytes,
		"isImage":           isImage,
		"isAudio":           isAudio,
		"isVideo":           isVideo,
		"isMedia":           isMedia,
		"isOdd":             isOdd,
		"isEven":            isEven,
		"humanizeDate":      humanizeDate,
		"humanizeTimestamp": humanizeTimestamp,
		"stepBegin":         stepBegin,
		"stepEnd":           stepEnd,
		"urlEncode":         urlEncode,
	})
	return tmpl.Parse(string(tmplText))
}

// Deprecated: Use createTemplate() instead.
func executeTemplate(tmplText []byte, params interface{}, w io.Writer) error {
	t, err := createTemplate(tmplText)
	if err != nil {
		return err
	}

	if err := t.Execute(w, params); err != nil {
		return err
	}
	return nil
}

func humanizeDate(t time.Time) string {
	return t.Format("2006-01-02")
}

func humanizeTimestamp(t time.Time) string {
	return t.Format("2006-01-02 03:04:05PM")
}

func isImage(name string) bool {
	return strings.HasPrefix(mime.TypeByExtension(filepath.Ext(name)), "image")
}

func isAudio(name string) bool {
	return strings.HasPrefix(mime.TypeByExtension(filepath.Ext(name)), "audio")
}

func isVideo(name string) bool {
	return strings.HasPrefix(mime.TypeByExtension(filepath.Ext(name)), "video")
}

func isMedia(name string) bool {
	return isVideo(name) || isImage(name)
}

func isOdd(v int) bool {
	return v%2 == 1
}

func isEven(v int) bool {
	return v%2 == 0
}

func stepBegin(val int, step int, _ int) bool {
	if step == 0 {
		return true
	}
	return val%step == 0
}

func stepEnd(val int, step int, maxVal int) bool {
	if step == 0 {
		return true
	}
	return val%step == step-1 || val+1 == maxVal
}

func urlEncode(u string) string {
	return url.PathEscape(u)
}

// encodeURLPath percent-encodes the given URL path so that characters with
// special meaning in URLs (such as '#', '?', and '%') are preserved as part
// of the path instead of being interpreted as the start of a fragment or
// query string when used in an href/Location value. Path separators ('/')
// are preserved.
func encodeURLPath(p string) string {
	return (&url.URL{Path: p}).EscapedPath()
}
