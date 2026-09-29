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
	"os"
	"runtime"
	"testing"
)

// TestMain points the user cache directory (os.UserCacheDir) at a
// temporary one, so no test reads or writes the real thumbnail cache or
// ffmpeg downloads.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gowebserver-test-cache-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	env := map[string]string{"XDG_CACHE_HOME": dir, "LocalAppData": dir}
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		// There the cache is $HOME/Library/Caches.
		env["HOME"] = dir
	}
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(code)
}
