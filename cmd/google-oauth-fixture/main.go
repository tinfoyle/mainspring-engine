package main

import (
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/googlefixture"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		response, err := (&http.Client{Timeout: 3 * time.Second}).Get("http://127.0.0.1:8080/health/ready")
		if err != nil || response.StatusCode != http.StatusNoContent {
			log.Fatal("Google fixture is not ready")
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return
	}
	clientID, clientSecret, redirectOrigin := os.Getenv("GOOGLE_FIXTURE_CLIENT_ID"), os.Getenv("GOOGLE_FIXTURE_CLIENT_SECRET"), os.Getenv("GOOGLE_FIXTURE_REDIRECT_ORIGIN")
	server, err := googlefixture.New(googlefixture.Config{ClientID: clientID, ClientSecret: clientSecret, RedirectOrigin: redirectOrigin})
	if err != nil {
		log.Fatal(err)
	}
	httpServer := &http.Server{Addr: ":8080", Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
