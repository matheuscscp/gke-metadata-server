// MIT License
//
// Copyright (c) 2023 Matheus Pimenta
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package pkghttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"
)

type (
	MetadataHandler interface {
		GetMetadata(http.ResponseWriter, *http.Request) (any, error)
	}

	MetadataHandlerFunc func(http.ResponseWriter, *http.Request) (any, error)

	TokenHandler struct{ MetadataHandler }

	DirectoryHandler struct{ directoryNode }

	DirectoryLister func(http.ResponseWriter, *http.Request) ([]string, *http.Request, error)

	directoryNode struct {
		children  []*directoryEdge
		pathParam *directoryEdge
		dirLister DirectoryLister
	}

	directoryEdge struct {
		name  string
		value any
	}
)

const (
	metadataFlavorHeader = "Metadata-Flavor"
	metadataFlavorGoogle = "Google"
)

func (f MetadataHandlerFunc) GetMetadata(w http.ResponseWriter, r *http.Request) (any, error) {
	return f(w, r)
}

func (h *DirectoryHandler) HandleDirectory(pattern string, dirLister DirectoryLister) {
	if dirLister == nil {
		panic("nil directory lister")
	}
	path := splitPathPieces(pattern)
	if len(path) == 0 {
		panic("cannot handle directory on empty path")
	}
	const prefix = ""
	var handler MetadataHandler = nil
	h.handle(prefix, path, handler, dirLister)
}

func (h *DirectoryHandler) HandleMetadata(pattern string, handler MetadataHandler) {
	if handler == nil {
		panic("nil handler")
	}
	path := splitPathPieces(pattern)
	if len(path) == 0 {
		panic("cannot handle empty path")
	}
	const prefix = ""
	var dirLister DirectoryLister = nil
	h.handle(prefix, path, handler, dirLister)
}

func (n *directoryNode) handle(prefix string, path []string, handler MetadataHandler, dirLister DirectoryLister) {
	piece := path[0]

	panicPrefix := prefix
	if prefix == "" {
		panicPrefix = "/"
	}
	panicPath := strings.Join(append([]string{prefix}, path...), "/")

	// let's find the correct edge (a static directory edge or a path param)
	var edge *directoryEdge

	// is this a path param?
	if strings.HasPrefix(piece, "$") {
		// yes. create or find the path param edge

		// is there no path param edge yet?
		if n.pathParam == nil {
			// is there a conflicting directory?
			if len(n.children) > 0 {
				children := make([]string, len(n.children))
				for i, e := range n.children {
					children[i] = e.name
				}
				panic(fmt.Sprintf("conflicting static directory and parameterized directory: %s with children {%s} and %s",
					panicPrefix,
					strings.Join(children, ", "),
					panicPath))
			}
			edge = &directoryEdge{name: piece}
			n.pathParam = edge
		} else if n.pathParam.name == piece { // there's already a path param edge. is it the same?
			edge = n.pathParam
		} else { // no, it's a conflicting one
			panic(fmt.Sprintf("conflicting parameterized directories: %s and %s",
				prefix+"/"+n.pathParam.name,
				panicPath))
		}
	} else if n.pathParam != nil { // no, it's a static directory edge. is there a conflicting path param edge?
		panic(fmt.Sprintf("conflicting parameterized directory and static directory: %s and %s",
			prefix+"/"+n.pathParam.name,
			panicPath))
	} else { // no conflicting path param edge. find or create the static directory edge
		for _, e := range n.children {
			if e.name == piece {
				edge = e
				break
			}
		}
		if edge == nil {
			edge = &directoryEdge{name: piece}
			n.children = append(n.children, edge)
		}
	}

	// base case? time to register the handler or the directory lister
	if len(path) == 1 {
		if dirLister != nil {
			if n.dirLister != nil {
				panic(fmt.Sprintf("conflicting directory listers: %s", panicPrefix))
			}
			if n.pathParam == nil {
				panic(fmt.Sprintf("directory lister with no path parameter: %s", panicPrefix))
			}
			n.dirLister = dirLister
		} else {
			if edge.value != nil {
				panic(fmt.Sprintf("conflicting handlers: %s", prefix+"/"+piece))
			}
			edge.value = handler
		}
		return
	}

	// recursive case. if the edge value is nil, create a new directory node
	if edge.value == nil {
		child := &directoryNode{}
		edge.value = child
		child.handle(prefix+"/"+piece, path[1:], handler, dirLister)
		return
	}

	// if the edge value is not nil, it must be a directory node
	child, ok := edge.value.(*directoryNode)
	if !ok {
		panic(fmt.Sprintf("conflicting handlers: %s and %s",
			prefix+"/"+piece,
			panicPath))
	}
	child.handle(prefix+"/"+piece, path[1:], handler, dirLister)
}

func (h *DirectoryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	l := logging.FromRequest(r)

	// check metadata flavor
	if strings.HasPrefix(r.URL.Path, "/computeMetadata/v1") && r.Header.Get(metadataFlavorHeader) != metadataFlavorGoogle {
		msg := fmt.Sprintf("Missing required header %q: %q\n", metadataFlavorHeader, metadataFlavorGoogle)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(msg))
		return
	}

	pieces := splitPathPieces(r.URL.Path)
	if len(pieces) == 0 {
		h.ServeHTTP(w, r)
		l.Debug("empty path")
		return
	}

	u := &h.directoryNode
	var dirPath string
	for i, piece := range pieces {
		dirPath += "/" + piece

		// let's find the correct edge (a static directory edge or a path param)
		var edge *directoryEdge

		// is this edge a path parameter?
		if u.pathParam != nil {
			// yes. use it and add it to the request context
			edge = u.pathParam
			var entries []string
			var err error
			entries, r, err = u.dirLister(w, r)
			if err != nil {
				return
			}
			var found bool
			for _, e := range entries {
				if e == piece {
					found = true
					break
				}
			}
			if !found {
				RespondNotFound(w)
				l.WithField("dir_path", dirPath).Debug("dynamic directory entry not found")
				return
			}
		} else { // no, it's a static directory edge
			// find the edge
			for _, e := range u.children {
				if e.name == piece {
					edge = e
					break
				}
			}
			if edge == nil {
				RespondNotFound(w)
				l.WithField("dir_path", dirPath).Debug("static directory entry not found")
				return
			}
		}

		// more path pieces after this one?
		if i+1 < len(pieces) {
			// then this edge must point to a directory
			if v, ok := edge.value.(*directoryNode); ok {
				u = v
				continue
			}
			RespondNotFound(w)
			l.WithField("dir_path", dirPath).Debug("path not found")
			return
		}

		// no more path pieces after this one. full match! now, is this a directory?
		if d, ok := edge.value.(*directoryNode); ok {
			d.ServeHTTP(w, r)
			return
		}

		// no, it's a handler
		serveMetadata(w, r, edge.value.(MetadataHandler))
		return
	}
}

func (n *directoryNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p := r.URL.Path; !strings.HasSuffix(p, "/") {
		http.Redirect(w, r, p+"/", http.StatusMovedPermanently)
		logging.FromRequest(r).Debug("directory redirect")
		return
	}

	// recursive?
	recursive := r.URL.Query().Get("recursive")
	if strings.HasPrefix(r.URL.Path, "/computeMetadata/v1") && strings.ToLower(recursive) == "true" {
		m, err := n.buildRecursive(w, r)
		if err != nil {
			// buildRecursive already responded and observed the error
			return
		}
		RespondJSON(w, r, http.StatusOK, m)
		return
	}

	var dirEntries []string
	for _, e := range n.children {
		entry := e.name
		if _, ok := e.value.(*directoryNode); ok {
			entry += "/"
		}
		dirEntries = append(dirEntries, entry)
	}

	if n.dirLister != nil {
		var moreDirEntries []string
		var err error
		moreDirEntries, r, err = n.dirLister(w, r)
		if err != nil {
			return
		}
		for _, e := range moreDirEntries {
			entry := e
			if _, ok := n.pathParam.value.(*directoryNode); ok {
				entry += "/"
			}
			dirEntries = append(dirEntries, entry)
		}
	}

	RespondText(w, r, http.StatusOK, strings.Join(dirEntries, "\n")+"\n")
}

func (n *directoryNode) buildRecursive(w http.ResponseWriter, r *http.Request) (map[string]any, error) {
	m := make(map[string]any)

	for _, e := range n.children {
		md, err := n.buildValueRecursive(w, r, e.value)
		if err != nil {
			// buildValueRecursive already responded and observed the error
			return nil, err
		}
		if md != nil {
			m[camelCaseFromKebab(e.name)] = *md
		}
	}

	if n.dirLister != nil {
		var dirEntries []string
		var err error
		dirEntries, r, err = n.dirLister(w, r)
		if err != nil {
			// dirLister already responded and observed the error
			return nil, err
		}
		for _, entry := range dirEntries {
			md, err := n.buildValueRecursive(w, r, n.pathParam.value)
			if err != nil {
				// buildValueRecursive already responded and observed the error
				return nil, err
			}
			if md != nil {
				m[entry] = md
			}
		}
	}

	return m, nil
}

func (n *directoryNode) buildValueRecursive(w http.ResponseWriter, r *http.Request, value any) (*any, error) {
	var res any
	var err error
	switch v := value.(type) {
	case TokenHandler:
		return nil, nil
	case MetadataHandler:
		res, err = v.GetMetadata(w, r)
	case *directoryNode:
		res, err = v.buildRecursive(w, r)
	}
	return &res, err
}

func (n *directoryNode) MarshalJSON() ([]byte, error) {
	m := make(map[string]any)
	for _, e := range n.children {
		v := e.value
		if _, ok := v.(*directoryNode); !ok {
			v = fmt.Sprintf("%T", v)
		}
		m[camelCaseFromKebab(e.name)] = v
	}
	if n.pathParam != nil {
		v := n.pathParam.value
		if _, ok := v.(*directoryNode); !ok {
			v = fmt.Sprintf("%T", v)
		}
		m[n.pathParam.name] = v
	}
	return json.Marshal(m)
}

func serveMetadata(w http.ResponseWriter, r *http.Request, handler MetadataHandler) {
	v, err := handler.GetMetadata(w, r)
	if err != nil {
		// GetMetadata already responded and observed the error
		return
	}
	switch metadata := v.(type) {
	case string:
		RespondText(w, r, http.StatusOK, metadata)
	case []string:
		RespondText(w, r, http.StatusOK, strings.Join(metadata, "\n")+"\n")
	case map[string]any:
		RespondJSON(w, r, http.StatusOK, metadata)
	default:
		RespondErrorf(w, r, http.StatusInternalServerError, "unsupported metadata type: %T", v)
	}
}

func splitPathPieces(path string) (pieces []string) {
	for _, piece := range strings.Split(path, "/") {
		if s := strings.TrimSpace(piece); s != "" {
			pieces = append(pieces, s)
		}
	}
	return
}

func camelCaseFromKebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r == '-' {
			continue
		}
		if i > 0 && s[i-1] == '-' {
			b.WriteString(strings.ToUpper(string(r)))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
