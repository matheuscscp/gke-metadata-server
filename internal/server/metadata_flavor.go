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

package server

import (
	"fmt"
	"net/http"
)

const (
	metadataFlavorHeader   = "Metadata-Flavor"
	metadataFlavorGoogle   = "Google"
	metadataFlavorEmulator = "Emulator"
)

func googleFlavorMiddleware(next func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return metadataFlavorMiddleware(metadataFlavorGoogle, "GKE Metadata Server", http.HandlerFunc(next))
}

func emulatorFlavorMiddleware(next func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return metadataFlavorMiddleware(metadataFlavorEmulator, "GKE Metadata Emulator", http.HandlerFunc(next))
}

func metadataFlavorMiddleware(flavor, server string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataFlavor := r.Header.Get(metadataFlavorHeader)
		if metadataFlavor == flavor {
			w.Header().Set(metadataFlavorHeader, flavor)
			w.Header().Set("Server", server)
			next.ServeHTTP(w, r)
			return
		}

		const statusCode = http.StatusForbidden
		w.WriteHeader(statusCode)
		w.Write([]byte(fmt.Sprintf("Missing required header %q: %q\n", metadataFlavorHeader, flavor)))
	})
}
