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
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// templateFS holds the page templates, one directory each:
// template/NAME/NAME.html, with the CSS and JavaScript it includes beside
// it. Their tests live a directory further down, so they aren't embedded.
//
//go:embed template/*/*.html template/*/*.css template/*/*.js
var templateFS embed.FS

// templateInclude matches a line that includes a file: {{include "NAME"}}.
// The line may end in CRLF, as the files do in a checkout on Windows.
var templateInclude = regexp.MustCompile(`(?m)^([ \t]*)\{\{include "([^"]+)"\}\}(\r?)$`)

// mustLoadTemplate is loadTemplate for the embedded templates, which are
// known to load (TestLoadTemplate).
func mustLoadTemplate(name string) []byte {
	text, err := loadTemplate(templateFS, name)
	if err != nil {
		panic(err)
	}
	return text
}

// loadTemplate returns the text of the template template/NAME/NAME.html
// in fsys as one text: the CSS and JavaScript kept in files of their own
// go back into it here, whether into the page itself or into the assets
// it defines and links to (pageassets.go). Each line that is only
// {{include "FILE"}} becomes the content of FILE, from the template's
// directory, indented like that line.
//
// An included file is CSS or JavaScript and nothing else, so it can be
// linted and tested as such; it is an error for it to hold a template
// action. Whatever needs one stays in the HTML.
func loadTemplate(fsys fs.FS, name string) ([]byte, error) {
	dir := path.Join("template", name)
	page, err := fs.ReadFile(fsys, path.Join(dir, name+".html"))
	if err != nil {
		return nil, err
	}
	var firstErr error
	out := templateInclude.ReplaceAllFunc(page, func(line []byte) []byte {
		m := templateInclude.FindSubmatch(line)
		indent, file, eol := string(m[1]), string(m[2]), string(m[3])
		text, err := fs.ReadFile(fsys, path.Join(dir, file))
		if err == nil && strings.Contains(string(text), "{{") {
			err = fmt.Errorf("%s holds a template action; keep that part in %s.html", path.Join(dir, file), name)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return line
		}
		lines := strings.Split(strings.TrimRight(string(text), "\r\n"), "\n")
		for i, l := range lines {
			if l != "" && l != "\r" {
				lines[i] = indent + l
			}
		}
		return []byte(strings.Join(lines, "\n") + eol)
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}
