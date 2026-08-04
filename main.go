package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"mime"
	"net/mail"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/yankeguo/airmx/internal/maildir"
	"github.com/yankeguo/airmx/internal/push"
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
	if len(os.Args) > 1 && os.Args[1] == "genpushkey" {
		cmdGenpushkey()
		return
	}

	configPath := flag.String("config", "config.yaml", "path to YAML config file")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	store := maildir.New(cfg.DataDir)

	var pushSvc *push.Service
	if cfg.Web.Push != nil {
		pushSvc, err = push.New(
			filepath.Join(cfg.DataDir, "push_subscriptions.json"),
			cfg.Web.Push.VapidPublicKey,
			cfg.Web.Push.VapidPrivateKey,
			"mailto:postmaster@"+cfg.Domain,
		)
		if err != nil {
			log.Fatalf("push: %v", err)
		}
		log.Printf("push: web push enabled (%d subscriptions)", pushSvc.Count())
	}

	var onDeliver func(raw []byte)
	if pushSvc != nil {
		onDeliver = func(raw []byte) { go notifyPush(pushSvc, raw) }
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- smtpserver.ListenAndServe(cfg.SMTPListen, smtpserver.Options{
			Domain:           cfg.Domain,
			Policy:           cfg.Policy,
			AcceptsRecipient: cfg.AcceptsRecipient,
			Store:            store,
			TLSCertDir:       cfg.TLSCertDir,
			OnDeliver:        onDeliver,
		})
	}()
	go func() {
		errCh <- web.New(store, cfg.Web.Username, cfg.Web.PasswordBcrypt, pushSvc).ListenAndServe(cfg.WebListen)
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

// cmdGenpushkey prints a fresh VAPID key pair for use as web.push.
func cmdGenpushkey() {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		log.Fatalf("genpushkey: %v", err)
	}
	fmt.Printf("vapid_public_key: %q\nvapid_private_key: %q\n", pub, priv)
}

// notifyPush sends a Web Push notification for a freshly delivered message,
// with the sender and subject as the body (best-effort header decoding).
func notifyPush(svc *push.Service, raw []byte) {
	var from, subject string
	if msg, err := mail.ReadMessage(bytes.NewReader(raw)); err == nil {
		wd := new(mime.WordDecoder)
		subject, _ = wd.DecodeHeader(msg.Header.Get("Subject"))
		if a, err := mail.ParseAddress(msg.Header.Get("From")); err == nil {
			if name, _ := wd.DecodeHeader(a.Name); name != "" {
				from = name + " <" + a.Address + ">"
			} else {
				from = a.Address
			}
		} else {
			from, _ = wd.DecodeHeader(msg.Header.Get("From"))
		}
	}
	body := from
	if subject != "" {
		if body != "" {
			body += "\n"
		}
		body += subject
	}
	svc.Notify("新邮件", "New mail", body)
}
