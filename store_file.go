package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileStore keeps everything in memory and writes it to one JSON file after
// every change. It is meant for local running and demos: one process, small
// data. It is not safe to share between several server instances.
type FileStore struct {
	mu   sync.Mutex
	path string // empty means memory only (used by tests)
	data fileData
}

type fileData struct {
	Menu    []MenuItem `json:"menu"`
	Orders  []Order    `json:"orders"`
	LastNum int        `json:"lastNumber"`
}

// NewFileStore loads path if it exists. A missing file is fine; it is created
// on the first write. Pass "" for a memory-only store.
func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

// saveLocked persists the data. Callers must hold s.mu. It writes to a
// temporary file and renames it, so a crash mid-write cannot corrupt the
// existing file.
func (s *FileStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the OS random source failing is not recoverable
	}
	return hex.EncodeToString(b)
}

func (s *FileStore) ListMenu(ctx context.Context) ([]MenuItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]MenuItem(nil), s.data.Menu...)
	sortMenu(out)
	return out, nil
}

func (s *FileStore) GetMenuItem(ctx context.Context, id string) (MenuItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.data.Menu {
		if it.ID == id {
			return it, nil
		}
	}
	return MenuItem{}, ErrNotFound
}

func (s *FileStore) SaveMenuItem(ctx context.Context, item *MenuItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if item.ID == "" {
		item.ID = newID()
		s.data.Menu = append(s.data.Menu, *item)
		return s.saveLocked()
	}
	for i := range s.data.Menu {
		if s.data.Menu[i].ID == item.ID {
			s.data.Menu[i] = *item
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

func (s *FileStore) DeleteMenuItem(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Menu {
		if s.data.Menu[i].ID == id {
			s.data.Menu = append(s.data.Menu[:i], s.data.Menu[i+1:]...)
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

func (s *FileStore) CreateOrder(ctx context.Context, o *Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.LastNum++
	now := time.Now().UTC()
	o.ID = newID()
	o.Number = s.data.LastNum
	o.Status = StatusNew
	o.CreatedAt, o.UpdatedAt = now, now
	s.data.Orders = append(s.data.Orders, *o)
	return s.saveLocked()
}

func (s *FileStore) GetOrder(ctx context.Context, id string) (Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.data.Orders {
		if o.ID == id {
			return o, nil
		}
	}
	return Order{}, ErrNotFound
}

func (s *FileStore) ListOrders(ctx context.Context, statuses ...string) ([]Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[string]bool{}
	for _, st := range statuses {
		want[st] = true
	}
	var out []Order
	for _, o := range s.data.Orders {
		if len(want) == 0 || want[o.Status] {
			out = append(out, o)
		}
	}
	sortOrders(out)
	return out, nil
}

func (s *FileStore) UpdateOrderStatus(ctx context.Context, id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Orders {
		if s.data.Orders[i].ID == id {
			s.data.Orders[i].Status = status
			s.data.Orders[i].UpdatedAt = time.Now().UTC()
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

// sortMenu orders by category then name, so the menu is stable on screen.
func sortMenu(items []MenuItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Category != items[j].Category {
			return items[i].Category < items[j].Category
		}
		return items[i].Name < items[j].Name
	})
}
