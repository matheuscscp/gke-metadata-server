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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"
)

type (
	DirectoryHandler struct{ directoryNode }

	// DirectoryLister is a function that must read the current request context if necessary
	// and list the entries for a directory. If there are errors the function should send
	// the response to the client immediately, otherwise it should return an updated request
	// context possibly containing useful information extracted from the request for future
	// callers.
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

func (h *DirectoryHandler) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request), dirListers ...DirectoryLister) {
	h.Handle(pattern, http.HandlerFunc(handler), dirListers...)
}

func (h *DirectoryHandler) Handle(pattern string, handler http.Handler, dirListers ...DirectoryLister) {
	path := splitPathPieces(pattern)
	if len(path) == 0 {
		panic("cannot handle empty path")
	}
	const prefix = ""
	h.handle(prefix, path, handler, dirListers)
}

func (n *directoryNode) handle(prefix string, path []string, handler http.Handler, dirListers []DirectoryLister) {
	piece := path[0]
	prefix += "/" + piece
	l := logging.
		FromContext(context.Background()).
		WithField("prefix", prefix).
		WithField("path", path).
		WithField("piece", piece)
	l.
		WithField("len(n.children) before", len(n.children)).
		Trace("handle start")

	// find correct edge
	var edge *directoryEdge
	if strings.HasPrefix(piece, "$") { // is this a path param?
		if n.pathParam == nil {
			edge = &directoryEdge{name: piece}
			n.pathParam = edge
		} else if n.pathParam.name == piece {
			edge = n.pathParam
		} else {
			panic(fmt.Sprintf("conflicting path parameters at %s and %s", prefix,
				strings.Join(append([]string{prefix}, path[1:]...), "/")))
		}

		// copy directory lister
		if len(dirListers) == 0 || dirListers[0] == nil {
			panic(fmt.Sprintf("path parameter '%s' at %s provided without a matching directory lister", piece, prefix))
		}
		if n.dirLister != nil {
			l.Trace("overriding previous dirLister")
		}
		n.dirLister = dirListers[0]
		dirListers = dirListers[1:]
	} else { // no, it's a static path piece/directory
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

	l.
		WithField("len(n.children) after", len(n.children)).
		Trace("handle end")

	// base case
	if len(path) == 1 {
		edge.value = handler
		return
	}

	// len(path) > 1
	var child *directoryNode
	if edge.value != nil {
		if _, ok := edge.value.(http.Handler); ok {
			panic(fmt.Sprintf("conflicting handlers at %s and %s", prefix,
				strings.Join(append([]string{prefix}, path[1:]...), "/")))
		}
		child = edge.value.(*directoryNode)
	} else {
		child = &directoryNode{}
		edge.value = child
	}
	child.handle(prefix, path[1:], handler, dirListers)
}

func (h *DirectoryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	l := logging.FromRequest(r)

	pieces := splitPathPieces(r.URL.Path)
	if len(pieces) == 0 {
		h.serveHTTP(w, r)
		l.Debug("empty path")
		return
	}

	u := &h.directoryNode
	var dirPath string
	for i, piece := range pieces {
		dirPath += "/" + piece

		// find edge
		var edge *directoryEdge
		for _, e := range u.children {
			if e.name == piece {
				edge = e
				break
			}
		}
		if edge == nil && u.pathParam == nil {
			RespondNotFound(w)
			l.WithField("dir_path", dirPath).Debug("path edge not found")
			return
		}
		// edge != nil || u.pathParam != nil
		if edge == nil { // is this edge a path parameter?
			// yes. use it and add it to the context
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
				l.WithField("dir_path", dirPath).Debug("entry not found")
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

		// no more path pieces after this one. full match! now, is this a handler?
		if handler, ok := edge.value.(http.Handler); ok {
			handler.ServeHTTP(w, r)
			return
		}
		// no, it's a directory.
		edge.value.(*directoryNode).serveHTTP(w, r)
		return
	}
}

// serveHTTP does not implement http.Handler on purpose, because we chose
// to identify a leaf nodes by testing the type assetion .(http.Handler),
// and directoryNode is not a leaf node by definition.
func (n *directoryNode) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if p := r.URL.Path; !strings.HasSuffix(p, "/") {
		http.Redirect(w, r, p+"/", http.StatusMovedPermanently)
		logging.FromRequest(r).Debug("directory redirect")
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

func splitPathPieces(path string) (pieces []string) {
	for _, piece := range strings.Split(path, "/") {
		if s := strings.TrimSpace(piece); s != "" {
			pieces = append(pieces, s)
		}
	}
	return
}

func (n *directoryNode) MarshalJSON() ([]byte, error) {
	edges := n.children
	if n.pathParam != nil {
		edges = append(edges, n.pathParam)
	}
	return json.Marshal(edges)
}

func (e *directoryEdge) MarshalJSON() ([]byte, error) {
	v := e.value
	if _, ok := v.(http.Handler); ok {
		v = "http.Handler"
	}
	return json.Marshal(map[string]any{e.name: v})
}
