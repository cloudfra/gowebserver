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

package internal

import (
	"runtime/debug"
	"testing"
)

func TestVersion(t *testing.T) {
	if Version() == "" {
		t.Errorf("Version() is empty, %q", Version())
	}
}

func TestBuildstamp(t *testing.T) {
	if Buildstamp() == "" {
		t.Errorf("Buildstamp() is empty, %q", Buildstamp())
	}
}

// Embedded in another program, the version is this module's there.
func TestModuleVersion(t *testing.T) {
	for name, tc := range map[string]struct {
		bi   *debug.BuildInfo
		want string
	}{
		"no build information":               {nil, ""},
		"this module, built from a checkout": {&debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "(devel)"}}, ""},
		"this module, installed":             {&debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v1.2.3"}}, "v1.2.3"},
		"another program's dependency": {&debug.BuildInfo{
			Main: debug.Module{Path: "example.com/host"},
			Deps: []*debug.Module{{Path: "example.com/other", Version: "v9.9.9"}, {Path: modulePath, Version: "v1.4.0"}},
		}, "v1.4.0"},
		"replaced": {&debug.BuildInfo{
			Main: debug.Module{Path: "example.com/host"},
			Deps: []*debug.Module{{Path: modulePath, Version: "v1.4.0", Replace: &debug.Module{Path: "../gowebserver", Version: "v1.4.1"}}},
		}, "v1.4.1"},
		"not linked": {&debug.BuildInfo{Main: debug.Module{Path: "example.com/host"}}, ""},
	} {
		if got := moduleVersion(tc.bi, tc.bi != nil); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
