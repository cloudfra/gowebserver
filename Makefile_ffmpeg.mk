# Copyright 2026 Cloudfra
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Embedding ffmpeg (opt-in).
#
#   make FFMPEG=1 all                                  every platform
#   make FFMPEG=1 build/bin/linux/amd64/gowebserver    one platform
#
# With FFMPEG=1, binaries are built with -tags ffmpeg, and those for the
# platforms below embed a static ffmpeg, downloaded first and stored
# xz-compressed in pkg/ffmpeg/bin/ (not checked in). Other platforms build
# as usual and use ffmpeg from PATH. The embedded builds are full-featured
# GPL builds, so binaries that embed them carry ffmpeg's GPL terms.
#
# Sources: Linux from https://johnvansickle.com/ffmpeg/ (current release),
# Windows from https://github.com/BtbN/FFmpeg-Builds (FFMPEG_WINDOWS_VERSION).
# There's no macOS source here: no static build could be verified.

FFMPEG_EMBED_DIR = pkg/ffmpeg/bin
FFMPEG_DOWNLOAD_DIR = build/ffmpeg
FFMPEG_WINDOWS_VERSION = 9.0

FFMPEG_URL_linux_amd64 = https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz
FFMPEG_URL_linux_arm64 = https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-arm64-static.tar.xz
FFMPEG_URL_linux_386 = https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-i686-static.tar.xz
FFMPEG_URL_windows_amd64 = https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n$(FFMPEG_WINDOWS_VERSION)-latest-win64-gpl-$(FFMPEG_WINDOWS_VERSION).zip
FFMPEG_URL_windows_arm64 = https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n$(FFMPEG_WINDOWS_VERSION)-latest-winarm64-gpl-$(FFMPEG_WINDOWS_VERSION).zip

# os_arch pairs with an embeddable build. Keep in sync with the
# embedded_<os>_<arch>.go files in pkg/ffmpeg.
FFMPEG_PLATFORMS = linux_amd64 linux_arm64 linux_386 windows_amd64 windows_arm64
FFMPEG_EMBEDS = $(foreach p,$(FFMPEG_PLATFORMS),$(FFMPEG_EMBED_DIR)/ffmpeg-$(subst _,-,$(p)).xz)

ffmpeg-embeds: $(FFMPEG_EMBEDS)

# Linux: a tarball with the ffmpeg binary in its top-level directory.
$(FFMPEG_EMBED_DIR)/ffmpeg-linux-%.xz:
	mkdir -p "$(FFMPEG_DOWNLOAD_DIR)/linux-$*" "$(FFMPEG_EMBED_DIR)"
	curl -fsSL -o "$(FFMPEG_DOWNLOAD_DIR)/linux-$*.tar.xz" "$(FFMPEG_URL_linux_$*)"
	tar -xJf "$(FFMPEG_DOWNLOAD_DIR)/linux-$*.tar.xz" -C "$(FFMPEG_DOWNLOAD_DIR)/linux-$*" --strip-components=1 --wildcards '*/ffmpeg'
	xz -9 -T0 -c "$(FFMPEG_DOWNLOAD_DIR)/linux-$*/ffmpeg" > "$@.tmp"
	mv "$@.tmp" "$@"

# Windows: a zip with bin/ffmpeg.exe under its top-level directory.
$(FFMPEG_EMBED_DIR)/ffmpeg-windows-%.xz:
	mkdir -p "$(FFMPEG_DOWNLOAD_DIR)/windows-$*" "$(FFMPEG_EMBED_DIR)"
	curl -fsSL -o "$(FFMPEG_DOWNLOAD_DIR)/windows-$*.zip" "$(FFMPEG_URL_windows_$*)"
	unzip -j -o "$(FFMPEG_DOWNLOAD_DIR)/windows-$*.zip" '*/bin/ffmpeg.exe' -d "$(FFMPEG_DOWNLOAD_DIR)/windows-$*"
	xz -9 -T0 -c "$(FFMPEG_DOWNLOAD_DIR)/windows-$*/ffmpeg.exe" > "$@.tmp"
	mv "$@.tmp" "$@"

clean-ffmpeg:
	rm -f $(FFMPEG_EMBED_DIR)/*.xz $(FFMPEG_EMBED_DIR)/*.xz.tmp
	rm -rf "$(FFMPEG_DOWNLOAD_DIR)"

ifeq ($(FFMPEG),1)
GOFLAGS := $(strip $(GOFLAGS) -tags=ffmpeg)
export GOFLAGS

# Each supported platform's binaries need its ffmpeg first. These add a
# prerequisite to the build/bin/% rule in Makefile_build.mk.
$(foreach app,$(ALL_APPS),$(foreach p,$(FFMPEG_PLATFORMS),$(eval build/bin/$(subst _,/,$(p))/$(app)$(if $(findstring windows,$(p)),.exe,): $(FFMPEG_EMBED_DIR)/ffmpeg-$(subst _,-,$(p)).xz)))
endif

.PHONY: ffmpeg-embeds clean-ffmpeg
