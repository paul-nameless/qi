package main

import (
	"log"
	"net/http"

	"shop/internal/config"
	"shop/internal/server"
	"shop/internal/store"
)

func main() {
	cfg, err := config.Load("config.json")
	if err != nil {
		log.Printf("config: %v, using defaults", err)
		cfg = config.Default()
	}
	st := store.NewMemory()
	st.Seed()
	srv := server.New(cfg, st)
	// TODO: graceful shutdown, depends on the auth session flush
	log.Printf("listening on %s", srv.Addr())
	log.Fatal(http.ListenAndServe(srv.Addr(), srv.Handler()))
}
