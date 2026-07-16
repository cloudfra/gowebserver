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
	"mime"
	"path/filepath"
	"strings"
)

var mimeIconMap = map[string]string{
	".":                               "folder",
	".3gp":                            "video",
	".7z":                             "archive",
	".ai":                             "photoshop",
	".avi":                            "video",
	".bak":                            "backup",
	".bash":                           "terminal",
	".bin":                            "binary",
	".bz2":                            "archive",
	".cc":                             "code",
	".cert":                           "certificate",
	".cfg":                            "config",
	".cmd":                            "terminal",
	".config":                         "config",
	".cpp":                            "code",
	".crt":                            "certificate",
	".cs":                             "code",
	".css":                            "stylesheet",
	".dat":                            "data",
	".db":                             "database",
	".deb":                            "package",
	".divx":                           "video",
	".doc":                            "doc",
	".docx":                           "doc",
	".download":                       "download",
	".ds_store":                       "database",
	".dwg":                            "cad",
	".epub":                           "ebook",
	".exe":                            "terminal",
	".flv":                            "video",
	".go":                             "code",
	".gz":                             "archive",
	".htm":                            "markup",
	".html":                           "markup",
	".ini":                            "config",
	".iso":                            "disc",
	".java":                           "code",
	".jpg":                            "image",
	".js":                             "script",
	".key":                            "key",
	".log":                            "log",
	".m4v":                            "video",
	".md":                             "doc",
	".mov":                            "video",
	".mp3":                            "audio",
	".mp4":                            "video",
	".mpeg":                           "video",
	".msi":                            "package",
	".pdf":                            "pdf",
	".pem":                            "key",
	".pk":                             "key",
	".pkg":                            "package",
	".pkv":                            "key",
	".ppt":                            "presentation",
	".pptx":                           "presentation",
	".ps1":                            "terminal",
	".psd":                            "photoshop",
	".psm1":                           "terminal",
	".pub":                            "certificate",
	".qt":                             "video",
	".rar":                            "archive",
	".rpm":                            "package",
	".scss":                           "stylesheet",
	".sh":                             "terminal",
	".snap":                           "package",
	".sqlite":                         "database",
	".svg":                            "vector",
	".tar":                            "archive",
	".ts":                             "script",
	".tsx":                            "script",
	".ttf":                            "font",
	".txt":                            "text",
	".webm":                           "video",
	".wmv":                            "video",
	".xls":                            "spreadsheet",
	".xlsx":                           "spreadsheet",
	".xvid":                           "video",
	".xz":                             "archive",
	".yaml":                           "config",
	".yml":                            "config",
	".zip":                            "archive",
	"application/illustrator":         "photoshop",
	"application/json":                "config",
	"application/pdf":                 "pdf",
	"application/x-cd-image":          "disc",
	"application/x-ms-dos-executable": "terminal",
	"application/x-msdownload":        "terminal",
	"application/x-shellscript":       "terminal",
	"application/x-x509-ca-cert":      "certificate",
	"application/x-yaml":              "config",
	"audio":                           "audio",
	"font":                            "font",
	"image":                           "image",
	"text":                            "text",
	"video":                           "video",
}

func nameToIconClass(isDir bool, name string) string {
	ext := filepath.Ext(strings.ToLower(name))
	if isDir {
		return "folder"
	}

	if val, ok := mimeIconMap[ext]; ok {
		return val
	}

	mimeType := mime.TypeByExtension(ext)

	if mimeType != "" {
		if val, ok := mimeIconMap[mimeType]; ok {
			return val
		}

		if parts := strings.Split(mimeType, "/"); len(parts) > 1 {
			if val, ok := mimeIconMap[parts[0]]; ok {
				return val
			}
		}
	}

	return "unknown"
}

func isRichViewable(iconClass string) bool {
	switch iconClass {
	case "markup", "data":
		return false
	case "code", "terminal", "text", "stylesheet", "script", "config", "log", "doc", "key", "certificate":
		return true
	}
	return false
}
