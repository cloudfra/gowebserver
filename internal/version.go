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

// Package internal exposes build-time version metadata injected via ldflags.
package internal

import "runtime/debug"

var (
	version    = "UNKNOWN"
	buildstamp = "UNKNOWN"
)

// modulePath is this module's, to find its version in a program's build
// information when the program that links it isn't this module's.
const modulePath = "github.com/cloudfra/gowebserver"

// Version of the package: the one stamped into the build, or else the
// module's version in the program's build information (as when another
// program embeds the package), or "UNKNOWN".
func Version() string {
	if version != "UNKNOWN" {
		return version
	}
	if v := moduleVersion(debug.ReadBuildInfo()); v != "" {
		return v
	}
	return version
}

// Buildstamp of the package: the one stamped into the build, or else the
// time of the commit it was built from, when known, or "UNKNOWN".
func Buildstamp() string {
	if buildstamp != "UNKNOWN" {
		return buildstamp
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Path == modulePath {
		for _, s := range bi.Settings {
			if s.Key == "vcs.time" && s.Value != "" {
				return s.Value
			}
		}
	}
	return buildstamp
}

// moduleVersion is this module's version in bi, or "".
func moduleVersion(bi *debug.BuildInfo, ok bool) string {
	if !ok || bi == nil {
		return ""
	}
	if bi.Main.Path == modulePath {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
		return ""
	}
	for _, d := range bi.Deps {
		if d.Path != modulePath {
			continue
		}
		if d.Replace != nil && d.Replace.Version != "" {
			return d.Replace.Version
		}
		return d.Version
	}
	return ""
}
