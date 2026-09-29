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

//go:build !unix

package update

import (
	"log/slog"
	"os"
)

// ExitCodeRestart is the exit status used to ask a service manager to
// start the updated executable.
const ExitCodeRestart = 75

// restart exits so the service manager starts the new executable: Windows
// can't replace a running process's image in place. As a Windows service
// with restart-on-failure set (see the README), it comes straight back;
// run by hand, it has to be started again.
func restart(string) error {
	slog.Info("exiting so the service manager starts the update", "exit code", ExitCodeRestart)
	os.Exit(ExitCodeRestart)
	return nil
}
