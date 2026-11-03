// Command takeaway is a small ordering app for a takeaway shop: customers
// order from a menu, staff manage the menu in an admin area, and the kitchen
// watches orders arrive on a live board.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"
)

//go:embed templates static
var assets embed.FS

const (
	defaultPort     = "8080"
	defaultDataFile = "data/takeaway.json"
	maxFormBytes    = 64 << 10 // 64 KiB is far more than any form here needs
)

// server holds every dependency; handlers are methods on it so tests can build
// one with a memory store and no network.
type server struct {
	store    Store
	auth     *auth
	shopName string
	pages    map[string]*template.Template
}

func main() {
	ctx := context.Background()

	store, closeStore, err := openStore(ctx)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer closeStore()

	adminPIN := envOrDefault("ADMIN_PIN", "")
	kitchenPIN := envOrDefault("KITCHEN_PIN", "")
	if adminPIN == "" || kitchenPIN == "" {
		log.Printf("WARNING: ADMIN_PIN / KITCHEN_PIN not set, using demo PINs 1234 / 5678. Set real ones before going public.")
		if adminPIN == "" {
			adminPIN = "1234"
		}
		if kitchenPIN == "" {
			kitchenPIN = "5678"
		}
	}
	secret := []byte(os.Getenv("SESSION_SECRET"))
	if len(secret) == 0 {
		log.Printf("WARNING: SESSION_SECRET not set, using a random one. Staff will be logged out on every restart.")
		secret = randomBytes(32)
	}

	s := &server{
		store:    store,
		auth:     newAuth(adminPIN, kitchenPIN, secret),
		shopName: envOrDefault("SHOP_NAME", "Gopher Takeaway"),
	}
	if err := s.parseTemplates(); err != nil {
		log.Fatalf("templates: %v", err)
	}

	port := envOrDefault("PORT", defaultPort)
	log.Printf("%s listening on http://localhost:%s", s.shopName, port)
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

// openStore picks the database. The default is a JSON file so that
// "go run ." works with no setup; STORE=firestore is used on Google Cloud.
func openStore(ctx context.Context) (Store, func(), error) {
	switch envOrDefault("STORE", "file") {
	case "firestore":
		project := os.Getenv("GOOGLE_CLOUD_PROJECT")
		if project == "" {
			return nil, nil, fmt.Errorf("STORE=firestore needs GOOGLE_CLOUD_PROJECT")
		}
		fstore, err := NewFirestoreStore(ctx, project)
		if err != nil {
			return nil, nil, err
		}
		return fstore, func() { fstore.Close() }, nil
	case "file":
		path := envOrDefault("DATA_FILE", defaultDataFile)
		st, err := NewFileStore(path)
		if err != nil {
			return nil, nil, err
		}
		if os.Getenv("SEED_DEMO") != "0" {
			if err := seedDemoMenu(ctx, st); err != nil {
				return nil, nil, err
			}
		}
		log.Printf("using file store %s", path)
		return st, func() {}, nil
	default:
		return nil, nil, fmt.Errorf("unknown STORE %q (use file or firestore)", os.Getenv("STORE"))
	}
}

// seedDemoMenu fills an empty menu so a fresh download shows something.
func seedDemoMenu(ctx context.Context, st Store) error {
	items, err := st.ListMenu(ctx)
	if err != nil || len(items) > 0 {
		return err
	}
	demo := []MenuItem{
		{Name: "Margherita pizza", Description: "Tomato, mozzarella, basil", Category: "Mains", PriceCents: 950, Available: true},
		{Name: "Chicken katsu curry", Description: "Crispy chicken, rice, mild curry sauce", Category: "Mains", PriceCents: 1100, Available: true},
		{Name: "Veggie spring rolls", Description: "Six crispy rolls with sweet chilli", Category: "Starters", PriceCents: 450, Available: true},
		{Name: "Garlic bread", Description: "Toasted with garlic butter", Category: "Starters", PriceCents: 350, Available: true},
		{Name: "Cola", Description: "330ml can", Category: "Drinks", PriceCents: 150, Available: true},
		{Name: "Mango lassi", Description: "Chilled yoghurt drink", Category: "Drinks", PriceCents: 280, Available: true},
	}
	for i := range demo {
		if err := st.SaveMenuItem(ctx, &demo[i]); err != nil {
			return err
		}
	}
	return nil
}

// routes registers every URL. Go 1.22+ patterns let us put the method and path
// wildcards in the pattern itself.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))

	// Customer.
	mux.HandleFunc("GET /{$}", s.handleMenu)
	mux.HandleFunc("POST /order", s.handlePlaceOrder)
	mux.HandleFunc("GET /order/{id}", s.handleOrderStatus)
	mux.HandleFunc("GET /order/{id}/status.json", s.handleOrderStatusJSON)

	// Login and logout for both staff areas.
	for _, role := range []string{RoleAdmin, RoleKitchen} {
		mux.HandleFunc("GET /"+role+"/login", s.handleLoginForm(role))
		mux.HandleFunc("POST /"+role+"/login", s.handleLogin(role))
		mux.HandleFunc("POST /"+role+"/logout", s.handleLogout(role))
	}

	// Admin: menu and order history.
	mux.HandleFunc("GET /admin", s.require(RoleAdmin, s.handleAdminMenu))
	mux.HandleFunc("POST /admin/menu", s.require(RoleAdmin, s.handleAdminSaveItem))
	mux.HandleFunc("POST /admin/menu/{id}", s.require(RoleAdmin, s.handleAdminSaveItem))
	mux.HandleFunc("POST /admin/menu/{id}/delete", s.require(RoleAdmin, s.handleAdminDeleteItem))
	mux.HandleFunc("GET /admin/orders", s.require(RoleAdmin, s.handleAdminOrders))

	// Kitchen: live board and status buttons.
	mux.HandleFunc("GET /kitchen", s.require(RoleKitchen, s.handleKitchen))
	mux.HandleFunc("GET /kitchen/orders.json", s.require(RoleKitchen, s.handleKitchenJSON))
	mux.HandleFunc("POST /kitchen/orders/{id}/status", s.require(RoleKitchen, s.handleKitchenSetStatus))

	return securityHeaders(mux)
}

// securityHeaders adds a few cheap, safe response headers to everything.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// cacheStatic lets browsers keep CSS and JS for an hour. The files are
// embedded, so they only change when the app is redeployed.
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

// page is the data every template receives. Not every field is used by every
// page.
type page struct {
	Title    string
	ShopName string
	Role     string // signed-in role, if any, for nav and CSRF
	CSRF     string
	Flash    string
	Error    string

	Menu   []menuGroup
	Items  []MenuItem
	Edit   *MenuItem
	Order  Order
	Orders []Order
	Form   map[string]string // values to refill a form after an error
}

type menuGroup struct {
	Category string
	Items    []MenuItem
}

func (s *server) parseTemplates() error {
	funcs := template.FuncMap{
		"money": money,
		"clock": func(t time.Time) string { return t.Local().Format("15:04") },
		"label": statusLabel,
		"price": func(cents int) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) },
		"mul":   func(a, b int) int { return a * b },
	}
	names := []string{
		"menu.html", "order.html", "login.html",
		"admin_menu.html", "admin_orders.html", "kitchen.html",
	}
	s.pages = map[string]*template.Template{}
	for _, n := range names {
		t, err := template.New("base.html").Funcs(funcs).ParseFS(assets, "templates/base.html", "templates/"+n)
		if err != nil {
			return fmt.Errorf("parse %s: %w", n, err)
		}
		s.pages[n] = t
	}
	return nil
}

// render executes into a buffer first so a template error becomes a clean 500
// instead of a half-written page.
func (s *server) render(w http.ResponseWriter, r *http.Request, name string, status int, p page) {
	p.ShopName = s.shopName
	// Derive the role from the session, never from the handler, so the CSRF
	// token in the page always matches the cookie that will be sent back.
	// Admin is checked first, matching roleOf.
	p.Role = ""
	for _, role := range []string{RoleAdmin, RoleKitchen} {
		if _, ok := s.auth.roleOf(r, role); ok {
			p.Role = role
			break
		}
	}
	if p.Role != "" {
		p.CSRF = s.auth.csrfToken(p.Role)
	}
	var buf bytes.Buffer
	if err := s.pages[name].ExecuteTemplate(&buf, "base", p); err != nil {
		log.Printf("render %s error: %v", name, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func statusLabel(st string) string {
	switch st {
	case StatusNew:
		return "Received"
	case StatusPreparing:
		return "Being prepared"
	case StatusReady:
		return "Ready to collect"
	case StatusCollected:
		return "Collected"
	case StatusCancelled:
		return "Cancelled"
	}
	return st
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
