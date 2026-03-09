package middleware

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

type responseData struct {
	status int
	size   int
}

type loggingResponseWriter struct {
	http.ResponseWriter
	responseData *responseData
}

func (r *loggingResponseWriter) Write(b []byte) (int, error) {
	size, err := r.ResponseWriter.Write(b)
	r.responseData.size += size
	return size, err
}

func (r *loggingResponseWriter) WriteHeader(statusCode int) {
	r.ResponseWriter.WriteHeader(statusCode)
	r.responseData.status = statusCode
}

func WithLogging(logger *zap.SugaredLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			rd := &responseData{}
			lw := &loggingResponseWriter{ResponseWriter: w, responseData: rd}

			next.ServeHTTP(lw, r)

			status := rd.status
			if status == 0 {
				status = http.StatusOK
			}

			logger.Infow("Request processed",
				"uri", r.RequestURI,
				"method", r.Method,
				"status", status,
				"duration", time.Since(start),
				"size", rd.size,
			)
		})
	}
}
