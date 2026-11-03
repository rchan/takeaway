package main

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FirestoreStore keeps menu items and orders in Firestore. Collections:
//
//	menu    document ID = generated, fields as tagged on MenuItem
//	orders  document ID = generated, fields as tagged on Order
//	meta    one document, "counter", holding the last order number
type FirestoreStore struct {
	client *firestore.Client
}

// NewFirestoreStore connects using Application Default Credentials.
func NewFirestoreStore(ctx context.Context, projectID string) (*FirestoreStore, error) {
	c, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("firestore client: %w", err)
	}
	return &FirestoreStore{client: c}, nil
}

func (s *FirestoreStore) Close() error { return s.client.Close() }

func (s *FirestoreStore) ListMenu(ctx context.Context) ([]MenuItem, error) {
	iter := s.client.Collection("menu").Documents(ctx)
	defer iter.Stop()
	var out []MenuItem
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read menu: %w", err)
		}
		var it MenuItem
		if err := doc.DataTo(&it); err != nil {
			continue // skip a bad document rather than losing the whole menu
		}
		it.ID = doc.Ref.ID
		out = append(out, it)
	}
	sortMenu(out)
	return out, nil
}

func (s *FirestoreStore) GetMenuItem(ctx context.Context, id string) (MenuItem, error) {
	doc, err := s.client.Collection("menu").Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return MenuItem{}, ErrNotFound
	}
	if err != nil {
		return MenuItem{}, err
	}
	var it MenuItem
	if err := doc.DataTo(&it); err != nil {
		return MenuItem{}, err
	}
	it.ID = doc.Ref.ID
	return it, nil
}

func (s *FirestoreStore) SaveMenuItem(ctx context.Context, item *MenuItem) error {
	if item.ID == "" {
		ref := s.client.Collection("menu").NewDoc()
		item.ID = ref.ID
		_, err := ref.Set(ctx, item)
		return err
	}
	_, err := s.client.Collection("menu").Doc(item.ID).Set(ctx, item)
	return err
}

func (s *FirestoreStore) DeleteMenuItem(ctx context.Context, id string) error {
	_, err := s.client.Collection("menu").Doc(id).Delete(ctx)
	return err
}

// CreateOrder takes the next order number inside a transaction, so two
// customers ordering at the same moment can never get the same number.
func (s *FirestoreStore) CreateOrder(ctx context.Context, o *Order) error {
	counter := s.client.Collection("meta").Doc("counter")
	ref := s.client.Collection("orders").NewDoc()
	now := time.Now().UTC()

	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		last := 0
		snap, err := tx.Get(counter)
		if err == nil {
			if v, ok := snap.Data()["last"].(int64); ok {
				last = int(v)
			}
		} else if status.Code(err) != codes.NotFound {
			return err
		}
		o.Number = last + 1
		o.Status = StatusNew
		o.CreatedAt, o.UpdatedAt = now, now
		if err := tx.Set(counter, map[string]interface{}{"last": o.Number}); err != nil {
			return err
		}
		return tx.Set(ref, o)
	})
	if err != nil {
		return fmt.Errorf("create order: %w", err)
	}
	o.ID = ref.ID
	return nil
}

func (s *FirestoreStore) GetOrder(ctx context.Context, id string) (Order, error) {
	doc, err := s.client.Collection("orders").Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, err
	}
	var o Order
	if err := doc.DataTo(&o); err != nil {
		return Order{}, err
	}
	o.ID = doc.Ref.ID
	return o, nil
}

// ListOrders filters by status in Firestore but sorts in Go. Combining a
// status filter with OrderBy(createdAt) would need a composite index; the
// kitchen only ever has a few dozen active orders, so sorting here is cheaper
// than managing an index.
func (s *FirestoreStore) ListOrders(ctx context.Context, statuses ...string) ([]Order, error) {
	q := s.client.Collection("orders").Query
	if len(statuses) > 0 {
		q = q.Where("status", "in", statuses)
	}
	iter := q.Documents(ctx)
	defer iter.Stop()
	var out []Order
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read orders: %w", err)
		}
		var o Order
		if err := doc.DataTo(&o); err != nil {
			continue
		}
		o.ID = doc.Ref.ID
		out = append(out, o)
	}
	sortOrders(out)
	return out, nil
}

func (s *FirestoreStore) UpdateOrderStatus(ctx context.Context, id, st string) error {
	_, err := s.client.Collection("orders").Doc(id).Update(ctx, []firestore.Update{
		{Path: "status", Value: st},
		{Path: "updatedAt", Value: time.Now().UTC()},
	})
	if status.Code(err) == codes.NotFound {
		return ErrNotFound
	}
	return err
}
