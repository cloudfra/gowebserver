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
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
)

// TestSecretName checks which file and folder names hold secrets.
func TestSecretName(t *testing.T) {
	for name, tc := range map[string]struct {
		file string
		want bool
	}{
		"private key by extension":       {file: "server.key", want: true},
		"extension in either case":       {file: "Server.PEM", want: true},
		"credential stores":              {file: "vault.kdbx", want: true},
		"ssh key by name":                {file: "id_ed25519", want: true},
		"ssh folder":                     {file: ".ssh", want: true},
		"env file and its variants":      {file: ".env.production", want: true},
		"a public key isn't a secret":    {file: "id_rsa.pub", want: false},
		"a folder named keys is not one": {file: "keys", want: false},
		"a folder named env is not one":  {file: "env", want: false},
		"an ordinary file":               {file: "notes.txt", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := isSecretName(tc.file); got != tc.want {
				t.Errorf("isSecretName(%q) = %t, want %t", tc.file, got, tc.want)
			}
		})
	}
}

// TestSecretPath checks that what a hidden folder holds is hidden along
// with it, wherever the path leads.
func TestSecretPath(t *testing.T) {
	for name, tc := range map[string]struct {
		path string
		want bool
	}{
		"a secret at the top":                 {path: "server.key", want: true},
		"a secret in an ordinary folder":      {path: "backup/server.key", want: true},
		"an ordinary file in a secret folder": {path: ".ssh/notes.txt", want: true},
		"an ordinary file":                    {path: "trip/photo.jpg", want: false},
		"a public key in a folder":            {path: "keys/id_rsa.pub", want: false},
		"the root folder":                     {path: ".", want: false},
		"inside an archive mount":             {path: "site.zip.d/server.key", want: true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := isSecretPath(tc.path); got != tc.want {
				t.Errorf("isSecretPath(%q) = %t, want %t", tc.path, got, tc.want)
			}
		})
	}
}

// browseGet is a request's response through a file system's handler chain.
func browseGet(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))
	return rec
}

// TestSecretFS checks what the wrapper hides from the file system under it,
// and what it leaves alone.
func TestSecretFS(t *testing.T) {
	mapFS := fstest.MapFS{
		"only/a.gpg":     {Data: []byte("secret")},
		"only/b.key":     {Data: []byte("secret")},
		"only/z.txt":     {Data: []byte("public")},
		"server.key":     {Data: []byte("secret")},
		"id_ed25519":     {Data: []byte("secret")},
		"trip/photo.jpg": {Data: []byte("public")},
	}
	fsys := secretFS{mapFS}

	// A folder's entries come through fs.ReadDir, through the folder's
	// handle, and one entry at a time.
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	if got := entryNames(entries); !cmp.Equal(got, []string{"only", "trip"}) {
		t.Errorf("entries of . = %q, want [only trip]", got)
	}
	f, err := fsys.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	rdf, ok := f.(fs.ReadDirFile)
	if !ok {
		t.Fatal("the root folder isn't a ReadDirFile")
	}
	entries, err = rdf.ReadDir(-1)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryNames(entries); !cmp.Equal(got, []string{"only", "trip"}) {
		t.Errorf("entries of . through its handle = %q, want [only trip]", got)
	}
	// A bounded read that comes back holding only secrets reads on, so the
	// caller gets an entry rather than an empty slice without an error.
	only, err := fsys.Open("only")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := only.Close(); err != nil {
			t.Error(err)
		}
	}()
	entries, err = only.(fs.ReadDirFile).ReadDir(1)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryNames(entries); !cmp.Equal(got, []string{"z.txt"}) {
		t.Errorf("a bounded read of only/ = %q, want [z.txt]", got)
	}

	for _, target := range []string{"server.key", "id_ed25519", "only/a.gpg", "only/b.key"} {
		if _, err := fsys.Open(target); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Open(%q) = %v, want fs.ErrNotExist", target, err)
		}
		if _, err := fs.Stat(fsys, target); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) = %v, want fs.ErrNotExist", target, err)
		}
		if _, err := fs.ReadFile(fsys, target); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadFile(%q) = %v, want fs.ErrNotExist", target, err)
		}
	}

	// What isn't a secret stays as it was, next to one and on its own.
	if got, err := fs.ReadFile(fsys, "only/z.txt"); err != nil || string(got) != "public" {
		t.Errorf("ReadFile(only/z.txt) = %q, %v", got, err)
	}
	if got, err := fs.ReadFile(fsys, "trip/photo.jpg"); err != nil || string(got) != "public" {
		t.Errorf("ReadFile(trip/photo.jpg) = %q, %v", got, err)
	}

	// A hidden file is refused exactly as one that isn't there, so being
	// hidden says nothing about whether a file exists.
	_, hiddenErr := fsys.Open("server.key")
	if hiddenErr == nil {
		t.Fatal("a secret opened")
	}
	_, missingErr := fsys.Open("not-there.key")
	if missingErr == nil {
		t.Fatal("a missing file claimed to be there")
	}
	if hiddenErr.Error() != missingErr.Error() {
		t.Errorf("a secret is refused with %q, a missing file with %q", hiddenErr, missingErr)
	}
}

// entryNames is the names of entries, in the order they came.
func entryNames(entries []fs.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// TestHideSecrets checks what browsing a folder reveals with
// security.hideSecrets on and off.
func TestHideSecrets(t *testing.T) {
	root := t.TempDir()
	write := func(name string, data []byte) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	photo := encodeJPEG(t, stripes(64, 48))
	write("notes.txt", []byte("public"))
	write("trip/photo.jpg", photo)
	write("server.key", []byte("secret"))
	write(".env.production", []byte("secret"))
	write("id_ed25519", []byte("secret"))
	write("id_rsa/photo.jpg", photo)

	for name, tc := range map[string]struct {
		enhanced    bool
		hideSecrets bool
	}{
		"enhanced listing":        {enhanced: true},
		"enhanced listing hidden": {enhanced: true, hideSecrets: true},
		"plain listing":           {},
		"plain listing hidden":    {hideSecrets: true},
	} {
		t.Run(name, func(t *testing.T) {
			mc := &monitoringContext{}
			thumbs, err := newThumbnailer(Thumbnails{Enabled: true, CachePath: thumbnailCacheMemory}, nil, mc)
			if err != nil {
				t.Fatal(err)
			}
			h, closeFS, err := newHandlerFromFS(root, mc, tc.enhanced, tc.hideSecrets, thumbs)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := errors.Join(thumbs.close(), closeFS()); err != nil {
					t.Error(err)
				}
			})

			// A hidden entry is out of the listing, and an ordinary one isn't.
			page := browseGet(t, h, "/").Body.String()
			for _, entry := range []string{"server.key", ".env.production", "id_ed25519", "id_rsa"} {
				if got := strings.Contains(page, entry); got != !tc.hideSecrets {
					t.Errorf("the listing of / mentions %q: %t, want %t", entry, got, !tc.hideSecrets)
				}
			}
			for _, entry := range []string{"notes.txt", "trip"} {
				if !strings.Contains(page, entry) {
					t.Errorf("the listing of / doesn't mention %q", entry)
				}
			}

			// A request naming a secret, a file below it, or one of its other
			// views is refused, and an ordinary request is answered.
			secretStatus := http.StatusOK
			if tc.hideSecrets {
				secretStatus = http.StatusNotFound
			}
			for _, target := range []string{
				"/server.key", "/.env.production", "/id_ed25519", "/id_rsa/photo.jpg", "/id_rsa/",
				"/server.key?view=rich", "/id_rsa/photo.jpg?width=32",
			} {
				rec := browseGet(t, h, target)
				if rec.Code != secretStatus {
					t.Errorf("%s: status %d, body %q, want %d", target, rec.Code, rec.Body.String(), secretStatus)
				}
			}
			for _, target := range []string{"/notes.txt", "/trip/", "/trip/photo.jpg", "/trip/photo.jpg?width=32"} {
				if rec := browseGet(t, h, target); rec.Code != http.StatusOK {
					t.Errorf("%s: status %d, body %q, want 200", target, rec.Code, rec.Body.String())
				}
			}

			// A photo under a hidden folder doesn't become a cover photo.
			// Only the enhanced listing has a preview to answer with.
			if tc.enhanced {
				want := items("id_rsa/photo.jpg", "trip/photo.jpg")
				if tc.hideSecrets {
					want = items("trip/photo.jpg")
				}
				if got := previewGet(t, h, "/?preview"); !cmp.Equal(got.Items, want) {
					t.Errorf("the preview of / is %+v, want %+v", got.Items, want)
				}
				if rec := browseGet(t, h, "/id_rsa/?preview"); rec.Code != secretStatus {
					t.Errorf("/id_rsa/?preview: status %d, body %q, want %d", rec.Code, rec.Body.String(), secretStatus)
				}
			}

			// With hiding on, a secret is refused exactly as a file that isn't
			// there is, and a hidden folder exactly as an absent folder is.
			if tc.hideSecrets {
				for _, pair := range [][2]string{{"/server.key", "/not-there.key"}, {"/id_rsa/", "/nope/"}} {
					hidden := browseGet(t, h, pair[0])
					missing := browseGet(t, h, pair[1])
					if hidden.Code != missing.Code || hidden.Body.String() != missing.Body.String() {
						t.Errorf("%s answered %d %q and %s answered %d %q",
							pair[0], hidden.Code, hidden.Body.String(), pair[1], missing.Code, missing.Body.String())
					}
				}
			}
		})
	}
}
