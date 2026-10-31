// Command wpgo runs WordPress Go: a WordPress-compatible CMS in a single binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // site time zones work on minimal images

	"wpgo/internal/config"
	"wpgo/internal/db"
	"wpgo/internal/server"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("wpgo: ")
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "serve":
		if err := serve(ctx, cfg); err != nil {
			log.Fatal(err)
		}
	case "migrate":
		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Fatal(err)
		}
		defer pool.Close()
		applied, err := db.Migrate(ctx, pool)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("applied %d migration(s) %v\n", len(applied), applied)
	case "version":
		fmt.Println("Pursae", server.Version)
	case "adminsec": // AdminSecGo recovery: disable-hide-login, reset-user <login>, unlock-all, deactivate
		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Fatal(err)
		}
		defer pool.Close()
		out, err := server.AdminSecCLI(ctx, pool, os.Args[2:])
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(out)
	default:
		flag.Usage = func() { fmt.Fprintln(os.Stderr, "usage: wpgo [serve|migrate|version|adminsec]") }
		flag.Usage()
		os.Exit(2)
	}
}

func serve(ctx context.Context, cfg config.Config) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := db.Migrate(ctx, pool)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		log.Printf("applied migrations %v", applied)
	}
	if err := os.MkdirAll(cfg.UploadsDir, 0o755); err != nil {
		return err
	}
	srv, err := server.New(ctx, cfg, pool)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	go srv.RunScheduler(ctx)
	log.Printf("listening on %s (site %s)", cfg.Addr, cfg.SiteURL)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
