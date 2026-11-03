package main

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func roleTitle(role string) string {
	if role == RoleKitchen {
		return "Kitchen"
	}
	return "Admin"
}

// ---- login ----------------------------------------------------------------

func (s *server) handleLoginForm(role string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		s.render(w, r, "login.html", http.StatusOK, page{Title: roleTitle(role) + " sign in", Role: "", Form: map[string]string{"role": role}})
	}
}

func (s *server) handleLogin(role string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		ip := clientIP(r)
		again := func(status int, msg string) {
			s.render(w, r, "login.html", status, page{
				Title: roleTitle(role) + " sign in", Error: msg, Form: map[string]string{"role": role},
			})
		}
		if left, locked := s.auth.locked(ip); locked {
			again(http.StatusTooManyRequests, "Too many attempts. Try again in "+left.Round(time.Minute).String()+".")
			return
		}
		if !s.auth.checkPIN(role, r.FormValue("pin")) {
			s.auth.recordFailure(ip)
			log.Printf("%s login FAILED from %s", role, ip)
			again(http.StatusUnauthorized, "Wrong PIN.")
			return
		}
		s.auth.clearFailures(ip)
		s.auth.setCookie(w, r, role)
		log.Printf("%s login OK from %s", role, ip)
		http.Redirect(w, r, "/"+role, http.StatusSeeOther)
	}
}

func (s *server) handleLogout(role string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		if _, ok := s.auth.roleOf(r, role); !ok || !s.auth.validCSRF(r, role) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// Clear both cookies so signing out really signs out.
		s.auth.clearCookie(w, RoleAdmin)
		s.auth.clearCookie(w, RoleKitchen)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

// ---- admin ----------------------------------------------------------------

// handleAdminMenu lists every menu item, including unavailable ones, with a
// form to add a new one or edit an existing one (?edit=<id>).
func (s *server) handleAdminMenu(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListMenu(r.Context())
	if err != nil {
		log.Printf("admin menu error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	p := page{Title: "Menu", Role: RoleAdmin, Items: items, Flash: r.URL.Query().Get("msg")}
	if id := r.URL.Query().Get("edit"); id != "" {
		for i := range items {
			if items[i].ID == id {
				p.Edit = &items[i]
			}
		}
	}
	s.render(w, r, "admin_menu.html", http.StatusOK, p)
}

// handleAdminSaveItem creates an item (no {id}) or updates one ({id}).
func (s *server) handleAdminSaveItem(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if !s.auth.validCSRF(r, RoleAdmin) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	item, err := itemFromForm(r)
	item.ID = r.PathValue("id")
	if err != nil {
		redirectMsg(w, r, "/admin", "Not saved: "+err.Error(), item.ID)
		return
	}
	if err := s.store.SaveMenuItem(r.Context(), &item); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("admin save item error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	log.Printf("admin save item: OK — %s (%s)", item.Name, money(item.PriceCents))
	redirectMsg(w, r, "/admin", "Saved "+item.Name+".", "")
}

// itemFromForm validates the admin form. The returned item is always usable
// for refilling the form, even when err is set.
func itemFromForm(r *http.Request) (MenuItem, error) {
	it := MenuItem{
		Name:        strings.TrimSpace(r.FormValue("name")),
		Description: strings.TrimSpace(r.FormValue("description")),
		Category:    strings.TrimSpace(r.FormValue("category")),
		Available:   r.FormValue("available") == "on",
	}
	switch {
	case it.Name == "":
		return it, errors.New("name is required")
	case len(it.Name) > 80, len(it.Description) > 200, len(it.Category) > 40:
		return it, errors.New("a field is too long")
	}
	cents, err := parsePrice(r.FormValue("price"))
	if err != nil {
		return it, err
	}
	it.PriceCents = cents
	return it, nil
}

func (s *server) handleAdminDeleteItem(w http.ResponseWriter, r *http.Request) {
	if !s.auth.validCSRF(r, RoleAdmin) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := s.store.DeleteMenuItem(r.Context(), r.PathValue("id")); err != nil && !errors.Is(err, ErrNotFound) {
		log.Printf("admin delete item error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	log.Printf("admin delete item: OK — %s", r.PathValue("id"))
	redirectMsg(w, r, "/admin", "Item deleted.", "")
}

// handleAdminOrders shows recent orders of every status, newest first.
func (s *server) handleAdminOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := s.store.ListOrders(r.Context())
	if err != nil {
		log.Printf("admin orders error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	// ListOrders is oldest first; this page wants newest first, capped at 100.
	for i, j := 0, len(orders)-1; i < j; i, j = i+1, j-1 {
		orders[i], orders[j] = orders[j], orders[i]
	}
	if len(orders) > 100 {
		orders = orders[:100]
	}
	s.render(w, r, "admin_orders.html", http.StatusOK, page{Title: "Orders", Role: RoleAdmin, Orders: orders})
}

// redirectMsg sends the browser back with a one-line message in the query
// string (and the item being edited, if any) after a POST.
func redirectMsg(w http.ResponseWriter, r *http.Request, path, msg, edit string) {
	q := url.Values{"msg": {msg}}
	if edit != "" {
		q.Set("edit", edit)
	}
	http.Redirect(w, r, path+"?"+q.Encode(), http.StatusSeeOther)
}

// ---- kitchen --------------------------------------------------------------

func (s *server) handleKitchen(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "kitchen.html", http.StatusOK, page{Title: "Kitchen", Role: RoleKitchen})
}

// handleKitchenJSON feeds the board. The page polls it every few seconds.
func (s *server) handleKitchenJSON(w http.ResponseWriter, r *http.Request) {
	orders, err := s.store.ListOrders(r.Context(), activeStatuses...)
	if err != nil {
		log.Printf("kitchen orders error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if orders == nil {
		orders = []Order{} // encode as [] not null
	}
	writeJSON(w, orders)
}

func (s *server) handleKitchenSetStatus(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	role, _ := s.auth.roleOf(r, RoleKitchen)
	if !s.auth.validCSRF(r, role) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	status := r.FormValue("status")
	if !validStatuses[status] {
		http.Error(w, "bad status", http.StatusBadRequest)
		return
	}
	err := s.store.UpdateOrderStatus(r.Context(), r.PathValue("id"), status)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("kitchen set status error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	log.Printf("kitchen status: OK — %s -> %s", r.PathValue("id"), status)
	w.WriteHeader(http.StatusNoContent)
}
