/*
Copyright 2023 The Godel Scheduler Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metrics

import (
	"net/http"
)

type responseWriterWithLength struct {
	http.ResponseWriter
	length int
}

func (w *responseWriterWithLength) Write(b []byte) (n int, err error) {
	n, err = w.ResponseWriter.Write(b)
	w.length += n
	return
}

func (w *responseWriterWithLength) Length() int {
	return w.length
}

type HandlerFunc func(responseSize int)

type limitedLengthHandler struct {
	hl            http.Handler
	lengthHandler HandlerFunc
}

func (h *limitedLengthHandler) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	lengthWriter := &responseWriterWithLength{ResponseWriter: res}
	h.hl.ServeHTTP(lengthWriter, req)
	h.lengthHandler(lengthWriter.Length())
}

func WithLimitedLengthHandler(h http.Handler, f HandlerFunc) http.Handler {
	return &limitedLengthHandler{hl: h, lengthHandler: f}
}
