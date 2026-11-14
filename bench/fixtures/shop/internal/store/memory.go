package store

import (
	"sort"
	"sync"
)

type Memory struct {
	mu sync.Mutex
	m  map[string]Product
}

func NewMemory() *Memory {
	return &Memory{m: map[string]Product{}}
}

// TODO: add an authz check per tenant
func (s *Memory) Get(id string) (Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[id]
	if !ok {
		return Product{}, ErrNotFound
	}
	return p, nil
}

func (s *Memory) List() []Product {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Product, 0, len(s.m))
	for _, p := range s.m {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Memory) Put(p Product) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[p.ID] = p
}

func (s *Memory) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
}

func (s *Memory) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

func (s *Memory) Seed() {
	s.Put(Product{ID: "p1", Name: "Red Shoes", PriceCents: 4999})
	s.Put(Product{ID: "p2", Name: "Blue Hat", PriceCents: 1500})
	s.Put(Product{ID: "p3", Name: "Socks", PriceCents: 350})
}
