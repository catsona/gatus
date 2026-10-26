package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"git.pegasko.art/catsona/gstatus/internal/model"
	bolt "go.etcd.io/bbolt"
)

// Store isolates account snapshots and deliveries in a single transaction database.
type Store struct {
	db *bolt.DB
}

// Open creates a private database, waiting at most one second for another process.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}

	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open state database (is gstatus already running?): %w", err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{"accounts", "deliveries", "herdr_links", "metadata"} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}

		metadata := tx.Bucket([]byte("metadata"))
		version := metadata.Get([]byte("version"))
		if len(version) != 0 && string(version) != "1" {
			return fmt.Errorf("unsupported state database version %q", version)
		}

		return metadata.Put([]byte("version"), []byte("1"))
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// HerdrLink loads the workspace link for a pull request URL.
func (s *Store) HerdrLink(prURL string) (model.HerdrLink, bool, error) {
	var link model.HerdrLink
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte("herdr_links")).Get([]byte(prURL))
		if data == nil {
			return nil
		}

		if err := json.Unmarshal(data, &link); err != nil {
			return err
		}
		return nil
	})
	return link, link.PRURL != "", err
}

// SaveHerdrLink replaces the workspace link for a pull request URL.
func (s *Store) SaveHerdrLink(link model.HerdrLink) error {
	data, err := json.Marshal(link)
	if err != nil {
		return err
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("herdr_links")).Put([]byte(link.PRURL), data)
	})
}

// RemoveHerdrLink deletes the workspace link for a pull request URL.
func (s *Store) RemoveHerdrLink(prURL string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("herdr_links")).Delete([]byte(prURL))
	})
}

// Close flushes outstanding database operations and releases the process lock.
func (s *Store) Close() error {
	return s.db.Close()
}

// Load returns an empty, uninitialized state for an unseen account.
func (s *Store) Load(account model.Account) (model.AccountState, error) {
	result := model.AccountState{Records: map[string]model.Record{}}
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte("accounts")).Get([]byte(account.Key()))
		if data == nil {
			return nil
		}

		return json.Unmarshal(data, &result)
	})
	if result.Records == nil {
		result.Records = map[string]model.Record{}
	}

	return result, err
}

// Save atomically persists a snapshot and inserts previously unseen delivery IDs.
func (s *Store) Save(account model.Account, state model.AccountState, deliveries []model.Delivery) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket([]byte("accounts")).Put([]byte(account.Key()), data); err != nil {
			return err
		}

		queue, err := tx.Bucket([]byte("deliveries")).CreateBucketIfNotExists([]byte(account.Key()))
		if err != nil {
			return err
		}

		for _, delivery := range deliveries {
			if delivery.Account.Key() != account.Key() {
				return fmt.Errorf("delivery belongs to a different account")
			}

			if queue.Get([]byte(delivery.ID)) != nil {
				continue
			}

			encoded, err := json.Marshal(delivery)
			if err != nil {
				return err
			}

			if err := queue.Put([]byte(delivery.ID), encoded); err != nil {
				return err
			}
		}

		return nil
	})
}

// Pending returns this account's deliveries in retry order.
func (s *Store) Pending(account model.Account) ([]model.Delivery, error) {
	var result []model.Delivery
	err := s.db.View(func(tx *bolt.Tx) error {
		queue := tx.Bucket([]byte("deliveries")).Bucket([]byte(account.Key()))
		if queue == nil {
			return nil
		}

		return queue.ForEach(func(_, value []byte) error {
			var delivery model.Delivery
			if err := json.Unmarshal(value, &delivery); err != nil {
				return err
			}

			result = append(result, delivery)
			return nil
		})
	})

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].NextAttempt.Before(result[j].NextAttempt)
	})

	return result, err
}

// PutDelivery persists a delivery's attempt count and next retry time.
func (s *Store) PutDelivery(delivery model.Delivery) error {
	data, err := json.Marshal(delivery)
	if err != nil {
		return err
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		queue, err := tx.Bucket([]byte("deliveries")).CreateBucketIfNotExists([]byte(delivery.Account.Key()))
		if err != nil {
			return err
		}

		return queue.Put([]byte(delivery.ID), data)
	})
}

// RemoveDelivery acknowledges delivery or intentionally cancels a pending event.
func (s *Store) RemoveDelivery(account model.Account, id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		queue := tx.Bucket([]byte("deliveries")).Bucket([]byte(account.Key()))
		if queue == nil {
			return nil
		}

		return queue.Delete([]byte(id))
	})
}

// PruneDeliveries cancels disabled event types in every account's paused queue.
func (s *Store) PruneDeliveries(enabledTypes []string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket([]byte("deliveries"))
		return root.ForEach(func(account, _ []byte) error {
			queue := root.Bucket(account)
			var remove [][]byte
			if err := queue.ForEach(func(id, data []byte) error {
				var delivery model.Delivery
				if err := json.Unmarshal(data, &delivery); err != nil {
					return err
				}

				if !slices.Contains(enabledTypes, delivery.Event.Type) {
					remove = append(remove, slices.Clone(id))
				}
				return nil
			}); err != nil {
				return err
			}

			for _, id := range remove {
				if err := queue.Delete(id); err != nil {
					return err
				}
			}
			return nil
		})
	})
}
