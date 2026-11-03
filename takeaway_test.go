package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParsePrice(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"4.50", 450, false},
		{"4.5", 450, false},
		{"4", 400, false},
		{"£12.05", 1205, false},
		{" 0.99 ", 99, false},
		{"", 0, true},
		{"-1", 0, true},
		{"1.234", 0, true},
		{"abc", 0, true},
		{"1.x", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parsePrice(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMoneyAndTotal(t *testing.T) {
	if got := money(1205); got != "£12.05" {
		t.Errorf("money = %q", got)
	}
	items := []OrderItem{{PriceCents: 950, Qty: 2}, {PriceCents: 150, Qty: 3}}
	if got := orderTotal(items); got != 2350 {
		t.Errorf("orderTotal = %d, want 2350", got)
	}
}

func TestBuildOrderItemsIgnoresClientPricesAndBadInput(t *testing.T) {
	menu := []MenuItem{
		{ID: "a", Name: "Pizza", PriceCents: 900, Available: true},
		{ID: "b", Name: "Soup", PriceCents: 400, Available: false},
		{ID: "c", Name: "Cola", PriceCents: 150, Available: true},
	}
	form := map[string][]string{
		"qty_a":     {"2"},
		"qty_b":     {"1"},   // unavailable
		"qty_c":     {"999"}, // clamped
		"qty_ghost": {"5"},   // not on the menu
		"price_a":   {"1"},   // ignored
	}
	items := buildOrderItems(menu, form)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(items), items)
	}
	if items[0].PriceCents != 900 || items[0].Qty != 2 {
		t.Errorf("pizza line wrong: %+v", items[0])
	}
	if items[1].Qty != maxQtyPerItem {
		t.Errorf("cola qty = %d, want clamp to %d", items[1].Qty, maxQtyPerItem)
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	item := &MenuItem{Name: "Tea", PriceCents: 200, Available: true}
	if err := st.SaveMenuItem(ctx, item); err != nil || item.ID == "" {
		t.Fatalf("save item: id=%q err=%v", item.ID, err)
	}
	o1 := &Order{Name: "Ann", Items: []OrderItem{{ItemID: item.ID, Name: "Tea", PriceCents: 200, Qty: 1}}}
	o2 := &Order{Name: "Bob"}
	if err := st.CreateOrder(ctx, o1); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateOrder(ctx, o2); err != nil {
		t.Fatal(err)
	}
	if o1.Number != 1 || o2.Number != 2 {
		t.Errorf("order numbers = %d, %d; want 1, 2", o1.Number, o2.Number)
	}
	if err := st.UpdateOrderStatus(ctx, o1.ID, StatusReady); err != nil {
		t.Fatal(err)
	}
	active, _ := st.ListOrders(ctx, StatusNew)
	if len(active) != 1 || active[0].ID != o2.ID {
		t.Errorf("new orders = %+v", active)
	}

	// Reload from disk: everything must survive a restart.
	st2, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st2.GetOrder(ctx, o1.ID)
	if err != nil || got.Status != StatusReady {
		t.Errorf("reloaded order = %+v, err %v", got, err)
	}
	if err := st2.CreateOrder(ctx, &Order{Name: "Cy"}); err != nil {
		t.Fatal(err)
	}
	all, _ := st2.ListOrders(ctx)
	if all[len(all)-1].Number != 3 {
		t.Errorf("counter did not survive restart: %d", all[len(all)-1].Number)
	}
	if err := st2.UpdateOrderStatus(ctx, "nope", StatusReady); err != ErrNotFound {
		t.Errorf("missing order err = %v", err)
	}
}

func TestTokens(t *testing.T) {
	a := newAuth("1", "2", []byte("secret"))
	good := a.token(RoleAdmin, time.Now().Add(time.Hour))
	if role, ok := a.parseToken(good); !ok || role != RoleAdmin {
		t.Fatalf("good token rejected")
	}
	expired := a.token(RoleAdmin, time.Now().Add(-time.Minute))
	tampered := strings.Replace(a.token(RoleKitchen, time.Now().Add(time.Hour)), "kitchen", "admin", 1)
	other := newAuth("1", "2", []byte("different")).token(RoleAdmin, time.Now().Add(time.Hour))
	for name, tok := range map[string]string{"expired": expired, "tampered": tampered, "wrong secret": other, "junk": "x|y"} {
		if _, ok := a.parseToken(tok); ok {
			t.Errorf("%s token accepted", name)
		}
	}
}

func TestLoginLockout(t *testing.T) {
	a := newAuth("1234", "5678", []byte("s"))
	for i := 0; i < maxLoginAttempts; i++ {
		a.recordFailure("1.2.3.4")
	}
	if _, locked := a.locked("1.2.3.4"); !locked {
		t.Error("expected lockout")
	}
	if _, locked := a.locked("9.9.9.9"); locked {
		t.Error("other client locked out")
	}
}

// testServer builds a server with a memory store and real templates.
func testServer(t *testing.T) *server {
	t.Helper()
	st, _ := NewFileStore("")
	s := &server{store: st, auth: newAuth("1234", "5678", []byte("test-secret")), shopName: "Test Shop"}
	if err := s.parseTemplates(); err != nil {
		t.Fatal(err)
	}
	return s
}

func do(h http.Handler, method, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStaffRoutesNeedLogin(t *testing.T) {
	s := testServer(t)
	h := s.routes()
	for _, path := range []string{"/admin", "/admin/orders", "/kitchen"} {
		rec := do(h, "GET", path, nil)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("GET %s = %d, want redirect to login", path, rec.Code)
		}
	}
	rec := do(h, "GET", "/kitchen/orders.json", nil)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("kitchen json without login = %d", rec.Code)
	}
	if rec := do(h, "POST", "/admin/menu", url.Values{"name": {"X"}, "price": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous POST /admin/menu = %d", rec.Code)
	}
}

func TestAdminRequiresCSRF(t *testing.T) {
	s := testServer(t)
	h := s.routes()
	cookie := &http.Cookie{Name: cookieName(RoleAdmin), Value: s.auth.token(RoleAdmin, time.Now().Add(time.Hour))}

	rec := do(h, "POST", "/admin/menu", url.Values{"name": {"Tea"}, "price": {"2"}, "available": {"on"}}, cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST without CSRF = %d, want 403", rec.Code)
	}
	if items, _ := s.store.ListMenu(context.Background()); len(items) != 0 {
		t.Fatal("item saved despite missing CSRF token")
	}

	form := url.Values{"name": {"Tea"}, "price": {"2"}, "available": {"on"}, "csrf_token": {s.auth.csrfToken(RoleAdmin)}}
	if rec := do(h, "POST", "/admin/menu", form, cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST with CSRF = %d", rec.Code)
	}
	items, _ := s.store.ListMenu(context.Background())
	if len(items) != 1 || items[0].PriceCents != 200 {
		t.Fatalf("items = %+v", items)
	}
}

func TestCustomerOrderFlow(t *testing.T) {
	s := testServer(t)
	h := s.routes()
	ctx := context.Background()
	item := &MenuItem{Name: "Bao", PriceCents: 500, Category: "Mains", Available: true}
	s.store.SaveMenuItem(ctx, item)

	if rec := do(h, "GET", "/", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Bao") {
		t.Fatalf("menu page: %d", rec.Code)
	}

	// No items: rejected, nothing stored.
	rec := do(h, "POST", "/order", url.Values{"name": {"Ann"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty order = %d, want 422", rec.Code)
	}

	rec = do(h, "POST", "/order", url.Values{"name": {"Ann"}, "qty_" + item.ID: {"3"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("order = %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	rec = do(h, "GET", loc, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "£15.00") {
		t.Errorf("order page missing total: %d", rec.Code)
	}
	rec = do(h, "GET", loc+"/status.json", nil)
	if !strings.Contains(rec.Body.String(), `"status":"new"`) {
		t.Errorf("status json = %s", rec.Body)
	}
	if rec := do(h, "GET", "/order/doesnotexist", nil); rec.Code != 404 {
		t.Errorf("unknown order = %d", rec.Code)
	}
}

func TestKitchenFlow(t *testing.T) {
	s := testServer(t)
	h := s.routes()
	ctx := context.Background()
	o := &Order{Name: "Ann", Items: []OrderItem{{Name: "Bao", Qty: 1}}}
	s.store.CreateOrder(ctx, o)

	cookie := &http.Cookie{Name: cookieName(RoleKitchen), Value: s.auth.token(RoleKitchen, time.Now().Add(time.Hour))}
	path := "/kitchen/orders/" + o.ID + "/status"

	if rec := do(h, "POST", path, url.Values{"status": {"ready"}}, cookie); rec.Code != http.StatusForbidden {
		t.Errorf("no CSRF = %d, want 403", rec.Code)
	}
	csrf := s.auth.csrfToken(RoleKitchen)
	if rec := do(h, "POST", path, url.Values{"status": {"bogus"}, "csrf_token": {csrf}}, cookie); rec.Code != http.StatusBadRequest {
		t.Errorf("bad status = %d, want 400", rec.Code)
	}
	if rec := do(h, "POST", path, url.Values{"status": {"ready"}, "csrf_token": {csrf}}, cookie); rec.Code != http.StatusNoContent {
		t.Errorf("good update = %d", rec.Code)
	}
	if got, _ := s.store.GetOrder(ctx, o.ID); got.Status != StatusReady {
		t.Errorf("status = %s", got.Status)
	}
	// A kitchen login must not open the admin area.
	if rec := do(h, "GET", "/admin", nil, cookie); rec.Code != http.StatusSeeOther {
		t.Errorf("kitchen cookie on /admin = %d, want redirect", rec.Code)
	}
}

func TestLoginWrongPIN(t *testing.T) {
	s := testServer(t)
	h := s.routes()
	if rec := do(h, "POST", "/admin/login", url.Values{"pin": {"0000"}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong pin = %d", rec.Code)
	}
	rec := do(h, "POST", "/admin/login", url.Values{"pin": {"1234"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Set-Cookie") == "" {
		t.Errorf("right pin = %d, cookie %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}
}
