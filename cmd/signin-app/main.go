package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"qiandao/internal/notifier"
	"qiandao/internal/provider"
	"qiandao/internal/provider/hdhive"
	"qiandao/internal/provider/juying"
	"qiandao/internal/service"
	"qiandao/internal/store"
	"qiandao/internal/web"
)

func main() {
	dbPath := getenv("SIGNIN_DB", filepath.Join("data", "signin.db"))
	addr := getenv("SIGNIN_ADDR", ":4567")
	staticDir := getenv("SIGNIN_WEB_DIR", "web")

	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()

	if err := applyEnvSettings(context.Background(), st); err != nil {
		log.Fatalf("load settings: %v", err)
	}

	nt := notifier.New()
	svc := service.NewSignInService(st, []provider.Provider{
		hdhive.New(),
		juying.New(),
	}, nt)
	scheduler := service.NewScheduler(st, svc)
	scheduler.Start()
	defer scheduler.Stop()

	handler := web.New(st, svc, staticDir)
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("signin-app listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func applyEnvSettings(ctx context.Context, st store.Store) error {
	settings, err := st.GetSettings(ctx)
	if err != nil {
		return err
	}
	changed := false
	if v := os.Getenv("TZ"); v != "" && settings.Timezone != v {
		settings.Timezone = v
		changed = true
	}
	if v := os.Getenv("SIGNIN_WEB_USERNAME"); v != "" && settings.Web.Username != v {
		settings.Web.Username = v
		changed = true
	}
	if v := os.Getenv("SIGNIN_WEB_PASSWORD"); v != "" && settings.Web.Password != v {
		settings.Web.Password = v
		changed = true
	}
	if changed {
		return st.SaveSettings(ctx, settings)
	}
	return nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
