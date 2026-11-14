package store

import "errors"

var ErrNotFound = errors.New("not found")

type Product struct {
	ID         string
	Name       string
	PriceCents int64
}

type Store interface {
	Get(id string) (Product, error)
	List() []Product
	Put(p Product)
}
