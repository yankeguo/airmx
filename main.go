package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/yankeguo/airmx/internal/maildir"
	"github.com/yankeguo/airmx/internal/smtpserver"
	"github.com/yankeguo/airmx/internal/web"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if len(os.Args) > 1 && os.Args[1] == "hashpw" {
		cmdHashpw(os.Args[2:])
		return
	}

	configPath := flag.String("config", "config.yaml", "path to YAML config file")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	store := maildir.New(cfg.DataDir)

	errCh := make(chan error, 2)
	go func() {
		errCh <- smtpserver.ListenAndServe(cfg.SMTPListen, smtpserver.Options{
			Domain:           cfg.Domain,
			Policy:           cfg.Policy,
			AcceptsRecipient: cfg.AcceptsRecipient,
			Store:            store,
		})
	}()
	go func() {
		errCh <- web.New(store, cfg.Web.Username, cfg.Web.PasswordBcrypt).ListenAndServe(cfg.WebListen)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Maildir delivery is atomic, so exiting on signal without draining
	// connections is safe.
	select {
	case err := <-errCh:
		log.Fatalf("server exited: %v", err)
	case sig := <-sigCh:
		log.Printf("received %v, shutting down", sig)
	}
}

// cmdHashpw prints a bcrypt hash for use as web.password_bcrypt.
func cmdHashpw(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: airmx hashpw <password>")
		os.Exit(2)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(args[0]), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("hashpw: %v", err)
	}
	fmt.Println(string(hash))
}
