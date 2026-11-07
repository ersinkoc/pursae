// Command wpgo runs WordPress Go: a WordPress-compatible CMS in a single binary.
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
	_ "time/tzdata" // site time zones work on minimal images

	"wpgo/internal/config"
	"wpgo/internal/coreupdate"
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
		if exe, err := executable(); err == nil {
			coreupdate.Guard(exe, server.Version) // a failed core update goes back to the previous binary here
		}
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
	case "launch": // container entrypoint: run the installed binary (see internal/coreupdate.Launch)
		exe, err := executable()
		if err != nil {
			log.Fatal(err)
		}
		dir := os.Getenv("WPGO_BIN_DIR")
		args := os.Args[2:]
		if len(args) == 0 {
			args = []string{"serve"}
		}
		log.Fatal(coreupdate.Launch(exe, dir, args))
	case "version":
		fmt.Println("Pursae", server.Version)
	case "mcp": // stdio MCP bridge to a site's /wp-json/pursae/v1/mcp (mcp.go)
		if err := runMCP(ctx, os.Args[2:], os.Stdin, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, "wpgo mcp:", err)
			os.Exit(1)
		}
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
		flag.Usage = func() { fmt.Fprintln(os.Stderr, "usage: wpgo [serve|launch|migrate|version|adminsec|mcp]") }
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
	go confirmUpdate(ctx, cfg.Addr)
	ln, err := coreupdate.Listen(cfg.Addr) // inherited across a core update's restart
	if err != nil {
		return err
	}
	coreupdate.Serving(ln, httpSrv.Shutdown)
	log.Printf("listening on %s (site %s)", cfg.Addr, cfg.SiteURL)
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if coreupdate.Restarting() {
		select {} // the core update execs the new binary; do not exit before it does
	}
	return nil
}

func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// confirmUpdate ends a pending core update once this binary answers requests (internal/coreupdate).
func confirmUpdate(ctx context.Context, addr string) {
	exe, err := executable()
	if err != nil {
		return
	}
	if st := coreupdate.Read(exe); st == nil || st.Status != "pending" || st.To != server.Version {
		return
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if coreupdate.WaitHealthy(ctx, "http://"+net.JoinHostPort(host, port)+"/wp-login") {
		coreupdate.Confirm(exe, server.Version)
		log.Printf("core update to %s confirmed", server.Version)
	}
}
