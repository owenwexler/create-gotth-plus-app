package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gorm.io/gorm"

	"github.com/owenwexler/gotth-plus/internal/app"
	"github.com/owenwexler/gotth-plus/internal/config"
	"github.com/owenwexler/gotth-plus/internal/database"
	"github.com/owenwexler/gotth-plus/internal/requestid"
)

func main() {
	env := config.GetEnv()

	// JSON logs in production for log tooling, readable text in development; both carry request_id
	var logHandler slog.Handler = slog.NewTextHandler(os.Stderr, nil)
	if env.IsProduction() {
		logHandler = slog.NewJSONHandler(os.Stderr, nil)
	}
	slog.SetDefault(slog.New(requestid.NewLogHandler(logHandler)))

	port := fmt.Sprintf(":%s", env.Port)

	db, err := database.ConnectDb(env.DatabasePath)
	if err != nil {
		slog.Error("database connection failed", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              port,
		Handler:           app.NewHandler(env, db),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// SIGINT/SIGTERM (Ctrl+C, `docker stop`, air/templ restarts) cancel ctx and start a graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// bind first so "listening" is only logged once the port is really ours
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		slog.Error("listen failed", "addr", srv.Addr, "err", err)
		closeDB(db)
		os.Exit(1)
	}

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("server listening", "port", env.Port, "env", env.GoEnv)
		serveErr <- srv.Serve(listener)
	}()

	select {
	case err := <-serveErr:
		// Serve only returns on its own when it crashed; a graceful shutdown happens in the other case
		slog.Error("server stopped", "err", err)
		closeDB(db)
		os.Exit(1)
	case <-ctx.Done():
		stop() // a second signal now kills the process the default way instead of being swallowed
		slog.Info("shutting down, waiting for in-flight requests", "timeout", shutdownTimeout)
	}

	// Stop accepting connections and let in-flight requests finish, but not forever
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed, closing connections", "err", err)
		_ = srv.Close()
	}

	// only close the database once no handler can still be using it
	closeDB(db)
	slog.Info("server stopped")
}

// closeDB releases the connection pool.
func closeDB(db *gorm.DB) {
	sqlDB, err := db.DB()
	if err != nil {
		slog.Error("getting database handle", "err", err)
		return
	}
	if err := sqlDB.Close(); err != nil {
		slog.Error("closing database", "err", err)
	}
}

// shutdownTimeout bounds how long in-flight requests get to finish; it matches the server's WriteTimeout.
const shutdownTimeout = 30 * time.Second
