package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
)

const (
	maxQtyPerItem = 20
	maxNameLen    = 60
	maxPhoneLen   = 30
	maxNotesLen   = 300
)

// handleMenu is the front screen: the menu with quantity boxes and a form for
// the customer's name.
func (s *server) handleMenu(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListMenu(r.Context())
	if err != nil {
		log.Printf("menu error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "menu.html", http.StatusOK, page{Title: "Order", Menu: groupMenu(items, true)})
}

// groupMenu splits a sorted menu into categories. Unavailable items are
// dropped when onlyAvailable is set (the customer view).
func groupMenu(items []MenuItem, onlyAvailable bool) []menuGroup {
	var groups []menuGroup
	for _, it := range items {
		if onlyAvailable && !it.Available {
			continue
		}
		cat := it.Category
		if cat == "" {
			cat = "Other"
		}
		if n := len(groups); n > 0 && groups[n-1].Category == cat {
			groups[n-1].Items = append(groups[n-1].Items, it)
			continue
		}
		groups = append(groups, menuGroup{Category: cat, Items: []MenuItem{it}})
	}
	return groups
}

// handlePlaceOrder builds an order from the form. Quantities arrive as
// qty_<itemID> fields; prices are always looked up on the server, never read
// from the form, so a customer cannot change what things cost.
func (s *server) handlePlaceOrder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	menu, err := s.store.ListMenu(r.Context())
	if err != nil {
		log.Printf("place order: menu error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	form := map[string]string{
		"name":  strings.TrimSpace(r.FormValue("name")),
		"phone": strings.TrimSpace(r.FormValue("phone")),
		"notes": strings.TrimSpace(r.FormValue("notes")),
	}
	items := buildOrderItems(menu, r.PostForm)

	if msg := validateOrder(form, items); msg != "" {
		// Show the menu again with the problem and the customer's input.
		s.render(w, r, "menu.html", http.StatusUnprocessableEntity, page{
			Title: "Order", Menu: groupMenu(menu, true), Error: msg, Form: form,
		})
		return
	}

	order := &Order{
		Name: form["name"], Phone: form["phone"], Notes: form["notes"],
		Items: items, TotalCents: orderTotal(items),
	}
	if err := s.store.CreateOrder(r.Context(), order); err != nil {
		log.Printf("place order error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	log.Printf("order #%d placed: %d lines, %s", order.Number, len(items), money(order.TotalCents))
	http.Redirect(w, r, "/order/"+order.ID, http.StatusSeeOther)
}

// buildOrderItems turns the submitted quantities into order lines using the
// current menu. Unknown, unavailable and zero-quantity entries are ignored.
func buildOrderItems(menu []MenuItem, form map[string][]string) []OrderItem {
	var items []OrderItem
	for _, m := range menu {
		vals := form["qty_"+m.ID]
		if !m.Available || len(vals) == 0 {
			continue
		}
		qty, err := strconv.Atoi(strings.TrimSpace(vals[0]))
		if err != nil || qty <= 0 {
			continue
		}
		if qty > maxQtyPerItem {
			qty = maxQtyPerItem
		}
		items = append(items, OrderItem{ItemID: m.ID, Name: m.Name, PriceCents: m.PriceCents, Qty: qty})
	}
	return items
}

// validateOrder returns a message for the customer, or "" when the order is OK.
func validateOrder(form map[string]string, items []OrderItem) string {
	switch {
	case len(items) == 0:
		return "Please choose at least one item."
	case form["name"] == "":
		return "Please tell us your name so we can call it out."
	case len(form["name"]) > maxNameLen:
		return "That name is too long."
	case len(form["phone"]) > maxPhoneLen:
		return "That phone number is too long."
	case len(form["notes"]) > maxNotesLen:
		return "Notes are limited to 300 characters."
	}
	return ""
}

// handleOrderStatus shows the customer their order. The page polls
// status.json so it updates without a reload.
func (s *server) handleOrderStatus(w http.ResponseWriter, r *http.Request) {
	o, err := s.store.GetOrder(r.Context(), r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("order status error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	noStore(w)
	s.render(w, r, "order.html", http.StatusOK, page{Title: "Order #" + strconv.Itoa(o.Number), Order: o})
}

func (s *server) handleOrderStatusJSON(w http.ResponseWriter, r *http.Request) {
	o, err := s.store.GetOrder(r.Context(), r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("order status json error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": o.Status, "label": statusLabel(o.Status)})
}

func writeJSON(w http.ResponseWriter, v any) {
	noStore(w)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json error: %v", err)
	}
}
