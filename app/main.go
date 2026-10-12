package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	executable, _ := os.Executable()
	defaultModuleDir := filepath.Dir(filepath.Dir(executable))
	moduleDir := flag.String("module-dir", defaultModuleDir, "installed module directory")
	stateDir := flag.String("state-dir", "/data/adb/screenbridge", "persistent state directory")
	configPath := flag.String("config", "", "configuration file (defaults to state-dir/config.conf)")
	flag.Parse()

	if *configPath == "" {
		*configPath = filepath.Join(*stateDir, "config.conf")
	}
	config, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if config.BootDelay > 0 {
		log.Printf("waiting %s before starting", config.BootDelay)
		time.Sleep(config.BootDelay)
	}

	logger := log.New(os.Stdout, "screenbridge: ", log.LstdFlags|log.Lmicroseconds)
	application := newServer(config, *moduleDir, *stateDir, logger)
	address := net.JoinHostPort(config.Host, fmt.Sprintf("%d", config.Port))
	server := &http.Server{
		Addr:              address,
		Handler:           application,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		application.closeVideoSessions()
	}()

	logger.Printf("listening on %s; module=%s state=%s", address, *moduleDir, *stateDir)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("HTTP server: %v", err)
	}
	<-shutdownDone
}
