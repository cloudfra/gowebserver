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
	"errors"
	"log/slog"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
)

// newFFmpeg returns the ffmpeg that conf chooses, or nil when none is
// available, in which case features that need it are turned off. ffmpeg
// is optional, so being without it isn't an error, but the reason is
// logged.
func newFFmpeg(conf FFmpeg) *ffmpeg.FFmpeg {
	ff, err := ffmpeg.New(ffmpeg.Options{
		Path:            conf.Path,
		InstallOnDemand: conf.InstallOnDemand,
		AcceptLicense:   conf.AcceptLicense,
		SourceURL:       conf.SourceURL,
	})
	switch {
	case err == nil:
		slog.Info("ffmpeg is available", "source", ff.Source())
	case errors.Is(err, ffmpeg.ErrLicenseNotAccepted):
		slog.Warn("ffmpeg is not installed, and ffmpeg.installOnDemand needs ffmpeg.acceptLicense to download it", "error", err)
	case conf.Path != "" || conf.InstallOnDemand:
		// Asked for, so worth a warning.
		slog.Warn("ffmpeg is unavailable", "error", err)
	default:
		slog.Info("ffmpeg is not installed; features that need it are off", "hint", "install it, or set ffmpeg.installOnDemand and ffmpeg.acceptLicense")
	}
	return ff
}
