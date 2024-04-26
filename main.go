package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/drornir/dobs"
)

func main() {
	var slogHandler slog.Handler
	slogHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// remove time just because it's an example
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})

	slogHandler = dobs.NewSlogHandler(slogHandler)

	logger := slog.New(slogHandler)

	server := makeHTTPServer(logger)

	logger.Info("starting server on :8080")

	err := http.ListenAndServe(":8080", server)
	if err != nil {
		dobs.Errorf("error from http.ListenAndServe: %w", err).
			LogTo(context.Background(), logger)
		defer os.Exit(1)
	}
}

type AppState struct {
	lock sync.RWMutex

	counter uint64
}

func (as *AppState) Counter() uint64 {
	as.lock.RLock()
	defer as.lock.RUnlock()
	return as.counter
}

func (as *AppState) AddCounter(i uint64) {
	as.lock.Lock()
	defer as.lock.Unlock()
	as.counter += i
}

type HTTPServer struct {
	logger  *slog.Logger
	wrapped http.Handler
}

func (s *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	newCtx := r.Context()
	newCtx = dobs.ContextAppendAttrs(newCtx,
		slog.String("http.method", r.Method),
		slog.String("http.path", r.URL.Path),
	)

	var dobsErrContainer dobs.Error
	newCtx = context.WithValue(newCtx, "dobs-error", &dobsErrContainer)

	r = r.WithContext(newCtx)
	s.wrapped.ServeHTTP(w, r)

	if dobsErrContainer.Err != nil {
		dobsErrContainer.LogTo(newCtx, s.logger)
	}
}

func setDobsError(ctx context.Context, e dobs.Error) {
	ptr, ok := ctx.Value("dobs-error").(*dobs.Error)
	if !ok || ptr == nil {
		panic("dobs error container not set on context")
	}

	*ptr = e
}

func makeHTTPServer(logger *slog.Logger) http.Handler {
	state := AppState{}

	mux := http.NewServeMux()

	server := &HTTPServer{
		logger:  logger,
		wrapped: mux,
	}

	mux.HandleFunc("GET /counter", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)

		counterValue := state.Counter()
		server.logger.DebugContext(ctx, "returning counter value", slog.Uint64("counterValue", counterValue))

		_, err := fmt.Fprintf(w, "%d", counterValue)
		if err != nil {
			derr := dobs.Errorf("error writing response body: %w", err).
				WithAttrs(slog.Uint64("counterValue", counterValue)).
				WithContextAttrs(ctx)
			setDobsError(ctx, derr) // or derr.LogTo(ctx, server.logger)
			return
		}
	})

	return server
}
