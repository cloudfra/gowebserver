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

import "testing"

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
