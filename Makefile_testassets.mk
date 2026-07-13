# Copyright 2019 Cloudfra
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

TEST_BASE_ARCHIVES = internal/gowebserver/testing/testassets.zip
TEST_BASE_ARCHIVES += internal/gowebserver/testing/testassets.rar
TEST_BASE_ARCHIVES += internal/gowebserver/testing/testassets.tar.gz
TEST_BASE_ARCHIVES += internal/gowebserver/testing/testassets.tar.bz2
TEST_BASE_ARCHIVES += internal/gowebserver/testing/testassets.tar
TEST_BASE_ARCHIVES += internal/gowebserver/testing/testassets.7z
TEST_BASE_ARCHIVES += internal/gowebserver/testing/testassets.tar.xz
TEST_BASE_ARCHIVES += internal/gowebserver/testing/testassets.tar.lz4

TEST_ARCHIVES = $(TEST_BASE_ARCHIVES) internal/gowebserver/testing/nested-testassets.zip internal/gowebserver/testing/single-testassets.zip internal/gowebserver/testing/nodir-testassets.zip

internal/gowebserver/testing/nodir-testassets.zip: $(TEST_BASE_ARCHIVES) internal/gowebserver/testing/single-testassets.zip internal/gowebserver/testing/nested-testassets.zip
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)/internal/gowebserver/testing/testassets"; zip -qr9 ../../nodir-testassets.zip index.html assets/1.txt assets/2.txt bytype/archive.rar bytype/text.txt site.js "weird #1.txt" weird#.txt weird$$.txt assets/more/3.txt assets/four/4.txt assets/fivesix/5.txt assets/fivesix/6.txt
	mv "$(REPOSITORY_ROOT)/internal/gowebserver/nodir-testassets.zip" "$(REPOSITORY_ROOT)/$@"

internal/gowebserver/testing/single-testassets.zip: $(TEST_BASE_ARCHIVES)
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)/internal/gowebserver/testing/"; zip -qr9 ../single-testassets.zip testassets/
	mv "$(REPOSITORY_ROOT)/internal/gowebserver/single-testassets.zip" "$(REPOSITORY_ROOT)/$@"

internal/gowebserver/testing/nested-testassets.zip: $(TEST_BASE_ARCHIVES) internal/gowebserver/testing/single-testassets.zip
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)/internal/gowebserver/testing/"; zip -qr9 ../nested-testassets.zip *
	mv "$(REPOSITORY_ROOT)/internal/gowebserver/nested-testassets.zip" "$(REPOSITORY_ROOT)/$@"

internal/gowebserver/testing/testassets.zip:
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)/internal/gowebserver/testing/testassets/"; zip -qr9 "$(REPOSITORY_ROOT)/$@" *

internal/gowebserver/testing/testassets.rar:
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)/internal/gowebserver/testing/testassets/"; rar a "$(REPOSITORY_ROOT)/$@" *

internal/gowebserver/testing/testassets.tar.gz:
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)/internal/gowebserver/testing/testassets/"; tar -cvf - * | gzip -9 - > "$(REPOSITORY_ROOT)/$@"

internal/gowebserver/testing/testassets.tar.bz2:
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)/internal/gowebserver/testing/testassets/"; tar -cvf - * | bzip2 -9 - > "$(REPOSITORY_ROOT)/$@"

internal/gowebserver/testing/testassets.tar.xz:
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)/internal/gowebserver/testing/testassets/"; tar cJf "../testassets.tar.xz" *

internal/gowebserver/testing/testassets.tar.lz4:
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)/internal/gowebserver/testing/testassets/"; tar cf - * | lz4 > "$(REPOSITORY_ROOT)/$@"

internal/gowebserver/testing/testassets.tar:
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)/internal/gowebserver/testing/testassets/"; tar cf "../testassets.tar" *

internal/gowebserver/testing/testassets.7z:
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)/internal/gowebserver/testing/testassets/"; 7z a "$(REPOSITORY_ROOT)/$@" *
