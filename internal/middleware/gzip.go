package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
)

type gzipWriter struct {
	http.ResponseWriter
	Writer io.Writer
}

func (w gzipWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

func shouldCompressContentType(contentType string) bool {
	for _, t := range []string{"application/json", "text/html"} {
		if strings.HasPrefix(contentType, t) {
			return true
		}
	}
	return false
}

type contentTypeWrapper struct {
	http.ResponseWriter
	headerWritten  bool
	shouldCompress bool
	gzipWriter     *gzip.Writer
}

func (w *contentTypeWrapper) WriteHeader(statusCode int) {
	if w.headerWritten {
		return
	}
	w.headerWritten = true

	contentType := w.Header().Get("Content-Type")
	w.shouldCompress = shouldCompressContentType(contentType)

	if w.shouldCompress {
		gz, err := gzip.NewWriterLevel(w.ResponseWriter, gzip.BestSpeed)
		if err != nil {
			w.ResponseWriter.WriteHeader(statusCode)
			return
		}
		w.gzipWriter = gz
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *contentTypeWrapper) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}
	if w.shouldCompress && w.gzipWriter != nil {
		return w.gzipWriter.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *contentTypeWrapper) Close() error {
	if w.gzipWriter != nil {
		return w.gzipWriter.Close()
	}
	return nil
}

func WithGzipCompression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		ctw := &contentTypeWrapper{ResponseWriter: w}
		defer ctw.Close()

		next.ServeHTTP(ctw, r)
	})
}

func WithGzipDecompression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Content-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer gz.Close()

		r.Body = io.NopCloser(gz)

		next.ServeHTTP(w, r)
	})
}
