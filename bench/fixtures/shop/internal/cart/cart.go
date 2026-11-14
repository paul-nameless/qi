package cart

import (
	"fmt"

	"shop/internal/pricing"
	"shop/internal/store"
	"shop/internal/util"
)

type item struct {
	p   store.Product
	qty int
}

type Cart struct {
	items map[string]*item
	order []string
}

func New() *Cart {
	return &Cart{items: map[string]*item{}}
}

// TODO: handle negative qty
func (c *Cart) Add(p store.Product, qty int) {
	if it, ok := c.items[p.ID]; ok {
		it.qty += qty
		return
	}
	c.items[p.ID] = &item{p: p, qty: qty}
	c.order = append(c.order, p.ID)
}

func (c *Cart) Remove(id string) {
	delete(c.items, id)
	for i, x := range c.order {
		if x == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

func (c *Cart) Lines() []pricing.Line {
	out := make([]pricing.Line, 0, len(c.order))
	for _, id := range c.order {
		it := c.items[id]
		out = append(out, pricing.Line{PriceCents: it.p.PriceCents, Qty: it.qty})
	}
	return out
}

func (c *Cart) Summary(currency string) string {
	return fmt.Sprintf("%d items, %s", len(c.order), util.FormatMoney(pricing.Subtotal(c.Lines()), currency))
}
