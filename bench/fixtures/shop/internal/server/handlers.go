package server

import (
	"fmt"
	"net/http"

	"shop/internal/cart"
	"shop/internal/pricing"
	"shop/internal/util"
)

// TODO: paginate the product list
func (s *Server) handleProducts(w http.ResponseWriter, r *http.Request) {
	for _, p := range s.st.List() {
		fmt.Fprintf(w, "%s\t%s\t%s\n", p.ID, p.Name, util.FormatMoneyShort(p.PriceCents))
	}
}

// handleCheckout runs behind the auth middleware.
func (s *Server) handleCheckout(w http.ResponseWriter, r *http.Request) {
	c := cart.New()
	for _, id := range r.URL.Query()["id"] {
		p, err := s.st.Get(id)
		if err != nil {
			http.Error(w, "unknown product "+id, http.StatusBadRequest)
			return
		}
		c.Add(p, 1)
	}
	total := pricing.Total(c.Lines(), s.cfg.TaxRate, s.cfg.FreeShippingOver)
	fmt.Fprintf(w, "%s\ntotal: %s\n", c.Summary(s.cfg.Currency), util.FormatMoney(total, s.cfg.Currency))
}
