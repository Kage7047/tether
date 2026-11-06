// Command tether serves the iPad companion on your tailnet.
//
//	tether -vault ~/second-brain -allow you@github           # joins the tailnet as "tether"
//	tether -vault ~/second-brain -dev                        # localhost:8787, no tailnet (development)
//
// On first run tsnet prints a login URL (or reads TS_AUTHKEY). With HTTPS enabled in the tailnet's
// admin console, the app is at https://tether.<tailnet>.ts.net and installs as a home-screen app.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"tailscale.com/tsnet"

	"github.com/Kage7047/tether/internal/brain"
	"github.com/Kage7047/tether/internal/server"
	"github.com/Kage7047/tether/web"
)

func main() {
	home, _ := os.UserHomeDir()
	vault := flag.String("vault", filepath.Join(home, "second-brain"), "second-brain directory")
	allow := flag.String("allow", "", "tailnet logins allowed in, comma separated (required unless -dev)")
	hostname := flag.String("hostname", "tether", "name on the tailnet")
	state := flag.String("state", filepath.Join(home, ".local", "share", "tether"), "tsnet state directory")
	plain := flag.Bool("http", false, "serve plain HTTP on :80 instead of HTTPS on :443 (no installable app)")
	dev := flag.Bool("dev", false, "serve on 127.0.0.1:8787 without the tailnet, as user dev@local")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s := &server.Server{Vault: &brain.Vault{Root: *vault}, App: web.App(), Log: log}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var ln net.Listener
	if *dev {
		var err error
		ln, err = net.Listen("tcp", "127.0.0.1:8787")
		if err != nil {
			fatal(log, err)
		}
		s.Allowed = []string{"dev@local"}
		s.Hosts = []string{"127.0.0.1", "localhost"}
		s.Identify = func(r *http.Request) (string, error) { return "dev@local", nil }
		log.Info("dev mode: no tailnet, localhost only", "url", "http://127.0.0.1:8787")
	} else {
		if *allow == "" {
			fatal(log, errors.New("-allow is required: name the tailnet logins that may use tether"))
		}
		s.Allowed = strings.Split(*allow, ",")
		ts := &tsnet.Server{Hostname: *hostname, Dir: *state}
		defer ts.Close()
		lc, err := ts.LocalClient()
		if err != nil {
			fatal(log, err)
		}
		if *plain {
			ln, err = ts.Listen("tcp", ":80")
		} else {
			ln, err = ts.ListenTLS("tcp", ":443")
		}
		if err != nil {
			fatal(log, err)
		}
		st, err := lc.Status(ctx)
		if err != nil {
			fatal(log, err)
		}
		s.Hosts = []string{strings.ToLower(*hostname), strings.TrimSuffix(strings.ToLower(st.Self.DNSName), ".")}
		for _, ip := range st.TailscaleIPs {
			s.Hosts = append(s.Hosts, ip.String())
		}
		s.Identify = func(r *http.Request) (string, error) {
			who, err := lc.WhoIs(r.Context(), r.RemoteAddr)
			if err != nil {
				return "", err
			}
			return who.UserProfile.LoginName, nil
		}
		log.Info("on the tailnet", "hostname", *hostname, "allowed", s.Allowed)
	}

	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal(log, err)
	}
}

func fatal(log *slog.Logger, err error) {
	log.Error("tether", "err", err)
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
