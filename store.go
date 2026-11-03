package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Order statuses, in the order an order normally moves through them.
const (
	StatusNew       = "new"
	StatusPreparing = "preparing"
	StatusReady     = "ready"
	StatusCollected = "collected"
	StatusCancelled = "cancelled"
)

// activeStatuses are the orders the kitchen screen cares about.
var activeStatuses = []string{StatusNew, StatusPreparing, StatusReady}

var validStatuses = map[string]bool{
	StatusNew: true, StatusPreparing: true, StatusReady: true,
	StatusCollected: true, StatusCancelled: true,
}

// ErrNotFound is returned by a Store when an item or order does not exist.
var ErrNotFound = errors.New("not found")

// MenuItem is something for sale. Prices are integer pence/cents, never floats,
// so totals add up exactly.
//
// Field tags are camelCase; they are the stored names in Firestore.
type MenuItem struct {
	ID          string `firestore:"-" json:"id"`
	Name        string `firestore:"name" json:"name"`
	Description string `firestore:"description" json:"description"`
	Category    string `firestore:"category" json:"category"`
	PriceCents  int    `firestore:"priceCents" json:"priceCents"`
	Available   bool   `firestore:"available" json:"available"`
}

// OrderItem is a line on an order. Name and price are copied from the menu at
// order time so later menu edits never rewrite history.
type OrderItem struct {
	ItemID     string `firestore:"itemId" json:"itemId"`
	Name       string `firestore:"name" json:"name"`
	PriceCents int    `firestore:"priceCents" json:"priceCents"`
	Qty        int    `firestore:"qty" json:"qty"`
}

// Order is one customer order.
type Order struct {
	ID         string      `firestore:"-" json:"id"`
	Number     int         `firestore:"number" json:"number"`
	Name       string      `firestore:"name" json:"name"`
	Phone      string      `firestore:"phone" json:"phone"`
	Notes      string      `firestore:"notes" json:"notes"`
	Items      []OrderItem `firestore:"items" json:"items"`
	TotalCents int         `firestore:"totalCents" json:"totalCents"`
	Status     string      `firestore:"status" json:"status"`
	CreatedAt  time.Time   `firestore:"createdAt" json:"createdAt"`
	UpdatedAt  time.Time   `firestore:"updatedAt" json:"updatedAt"`
}

// Store is everything the handlers need from a database. The app ships two
// implementations: a JSON file (the default, no setup) and Firestore (for
// Google Cloud). Handlers only ever see this interface.
type Store interface {
	ListMenu(ctx context.Context) ([]MenuItem, error)
	GetMenuItem(ctx context.Context, id string) (MenuItem, error)
	// SaveMenuItem creates the item when ID is empty, otherwise replaces it.
	SaveMenuItem(ctx context.Context, item *MenuItem) error
	DeleteMenuItem(ctx context.Context, id string) error

	// CreateOrder assigns ID, Number, Status, CreatedAt and UpdatedAt.
	CreateOrder(ctx context.Context, o *Order) error
	GetOrder(ctx context.Context, id string) (Order, error)
	// ListOrders returns orders with any of the given statuses, oldest first.
	ListOrders(ctx context.Context, statuses ...string) ([]Order, error)
	UpdateOrderStatus(ctx context.Context, id, status string) error
}

// parsePrice turns "4.50", "4.5", "4" or "£4.50" into 450. It rejects
// negatives and more than two decimal places.
func parsePrice(s string) (int, error) {
	s = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "£$€"))
	if s == "" {
		return 0, errors.New("price is required")
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		return 0, errors.New("price has too many decimal places")
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w, err := strconv.Atoi(whole)
	if err != nil || w < 0 {
		return 0, errors.New("price must be a positive number")
	}
	f, err := strconv.Atoi(frac)
	if err != nil || f < 0 {
		return 0, errors.New("price must be a positive number")
	}
	return w*100 + f, nil
}

// money formats pence as "£4.50".
func money(cents int) string {
	return fmt.Sprintf("£%d.%02d", cents/100, cents%100)
}

// orderTotal sums the lines of an order.
func orderTotal(items []OrderItem) int {
	total := 0
	for _, it := range items {
		total += it.PriceCents * it.Qty
	}
	return total
}

// sortOrders puts the oldest order first, which is the order the kitchen
// should cook in.
func sortOrders(orders []Order) {
	sort.Slice(orders, func(i, j int) bool { return orders[i].CreatedAt.Before(orders[j].CreatedAt) })
}
