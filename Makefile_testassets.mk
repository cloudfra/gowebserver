# Copyright 2019 Jeremy Edwards
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
	cd internal/gowebserver/testing/testassets; zip -qr9 ../../nodir-testassets.zip index.html assets/1.txt assets/2.txt bytype/archive.rar bytype/text.txt site.js "weird #1.txt" weird#.txt weird$$.txt assets/more/3.txt assets/four/4.txt assets/fivesix/5.txt assets/fivesix/6.txt
	mv internal/gowebserver/nodir-testassets.zip internal/gowebserver/testing/nodir-testassets.zip

internal/gowebserver/testing/single-testassets.zip: $(TEST_BASE_ARCHIVES)
	cd internal/gowebserver/testing/; zip -qr9 ../single-testassets.zip testassets/
	mv internal/gowebserver/single-testassets.zip internal/gowebserver/testing/single-testassets.zip

internal/gowebserver/testing/nested-testassets.zip: $(TEST_BASE_ARCHIVES) internal/gowebserver/testing/single-testassets.zip
	cd internal/gowebserver/testing/; zip -qr9 ../nested-testassets.zip *
	mv internal/gowebserver/nested-testassets.zip internal/gowebserver/testing/nested-testassets.zip

internal/gowebserver/testing/testassets.zip:
	cd internal/gowebserver/testing/testassets/; zip -qr9 ../testassets.zip *

internal/gowebserver/testing/testassets.rar:
	cd internal/gowebserver/testing/testassets/; rar a ../testassets.rar *

internal/gowebserver/testing/testassets.tar.gz:
	cd internal/gowebserver/testing/testassets/; tar -I 'gzip -9' -cf ../testassets.tar.gz *

internal/gowebserver/testing/testassets.tar.bz2:
	cd internal/gowebserver/testing/testassets/; BZIP=-9 tar cjf ../testassets.tar.bz2 *

internal/gowebserver/testing/testassets.tar.xz:
	cd internal/gowebserver/testing/testassets/; tar cJf ../testassets.tar.xz *

internal/gowebserver/testing/testassets.tar.lz4:
	cd internal/gowebserver/testing/testassets/; tar cf ../testassets.tar.lz4 -I 'lz4' *

internal/gowebserver/testing/testassets.tar:
	cd internal/gowebserver/testing/testassets/; tar cf ../testassets.tar *

internal/gowebserver/testing/testassets.7z:
	cd internal/gowebserver/testing/testassets/; 7z a ../testassets.7z *
