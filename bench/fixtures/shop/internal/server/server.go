package server

import (
	"fmt"
	"net/http"

	"shop/internal/config"
	"shop/internal/store"
)

type Server struct {
	cfg    *config.Config
	st     store.Store
	secret []byte
}

func New(cfg *config.Config, st store.Store) *Server {
	return &Server{cfg: cfg, st: st, secret: []byte("dev-secret")}
}

func (s *Server) Addr() string {
	return fmt.Sprintf(":%d", s.cfg.Port)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/products", s.handleProducts)
	mux.Handle("/checkout", RequireUser(s.secret, http.HandlerFunc(s.handleCheckout)))
	return Logging(mux)
}
