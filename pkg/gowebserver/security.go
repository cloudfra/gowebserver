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
	"io/fs"
	"strings"
)

// secretExtensions are the extensions of files that are secret on their
// own: private keys, key stores and encrypted files.
var secretExtensions = []string{
	".asc", ".gpg", ".jks", ".kdb", ".kdbx", ".key", ".p12",
	".pem", ".pgp", ".pfx", ".ppk", ".truststore",
}

// secretNames are the names of folders and files that hold credentials,
// like the .ssh folder of a home directory and the ssh keys in it.
var secretNames = []string{
	".aws", ".docker", ".git-credentials", ".gnupg", ".htpasswd",
	".kube", ".netrc", ".npmrc", ".pgpass", ".pypirc", ".ssh", "_netrc",
	"id_dsa", "id_ecdsa", "id_ed25519", "id_ed448", "id_rsa", "id_xmss",
}

// secretPrefixes are the names of files that come in named variants, like
// the .env.local file an application reads its settings from.
var secretPrefixes = []string{".env"}

// isSecretName says whether a file or folder name holds secrets, and is
// what security.hideSecrets keeps from browsers.
func isSecretName(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range secretPrefixes {
		if lower == prefix || strings.HasPrefix(lower, prefix+".") {
			return true
		}
	}
	for _, secret := range secretNames {
		if lower == secret {
			return true
		}
	}
	for _, extension := range secretExtensions {
		if strings.HasSuffix(lower, extension) {
			return true
		}
	}
	return false
}

// isSecretPath says whether any file or folder along a slash separated path
// holds secrets, which hides what a hidden folder holds along with it.
func isSecretPath(p string) bool {
	for _, name := range strings.Split(p, "/") {
		if name != "" && isSecretName(name) {
			return true
		}
	}
	return false
}

// secretFS is what a served filesystem looks like to browsers with
// security.hideSecrets on: secrets are missing from listings and from
// requests that name them.
type secretFS struct {
	fs.FS
}

// Open says a secret isn't there, and hides the secret entries of the
// folders it opens.
func (f secretFS) Open(name string) (fs.File, error) {
	if isSecretPath(name) {
		return nil, fs.ErrNotExist
	}
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	dir, ok := file.(fs.ReadDirFile)
	if !ok {
		return file, nil
	}
	// A listing reads a folder's entries through its handle, so they're
	// filtered there as well as in ReadDir.
	info, err := dir.Stat()
	if err != nil || !info.IsDir() {
		return file, nil
	}
	return secretDir{dir}, nil
}

// ReadDir is the entries of the folder at name, without the secrets.
func (f secretFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if isSecretPath(name) {
		return nil, fs.ErrNotExist
	}
	entries, err := fs.ReadDir(f.FS, name)
	if err != nil {
		return nil, err
	}
	return withoutSecrets(entries), nil
}

// Stat says a secret isn't there.
func (f secretFS) Stat(name string) (fs.FileInfo, error) {
	if isSecretPath(name) {
		return nil, fs.ErrNotExist
	}
	return fs.Stat(f.FS, name)
}

// secretDir is an open folder with its secret entries hidden.
type secretDir struct {
	fs.ReadDirFile
}

// ReadDir hides the secret entries. A bounded read that comes back holding
// only secrets reads the next batch, so the caller gets entries or the end
// of the folder rather than an empty slice without an error.
func (d secretDir) ReadDir(count int) ([]fs.DirEntry, error) {
	for {
		entries, err := d.ReadDirFile.ReadDir(count)
		kept := withoutSecrets(entries)
		if err != nil || count <= 0 || len(kept) > 0 {
			return kept, err
		}
	}
}

// withoutSecrets is the entries that aren't secrets.
func withoutSecrets(entries []fs.DirEntry) []fs.DirEntry {
	kept := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if isSecretName(entry.Name()) {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}
