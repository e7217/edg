package core

import (
	"path/filepath"
	"sync"
	"testing"
)

// Foreign keys must be on for EVERY pooled connection, not just the one that
// happened to serve the pragma at open time.
//
// `PRAGMA foreign_keys = ON` against the *sql.DB set it on one connection. For a
// file-backed database the pool is unbounded, so a cascade fired or did not
// depending on which connection the delete landed on -- measured at two of eight
// reads seeing it off. The pragma now travels in the DSN.
func TestForeignKeysOnEveryPooledConnection(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Force several connections to exist simultaneously, then read the pragma
	// on each. database/sql hands out whichever is free.
	var wg sync.WaitGroup
	results := make([]int, 8)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			var on int
			if err := store.db.QueryRow("PRAGMA foreign_keys").Scan(&on); err != nil {
				t.Error(err)
				return
			}
			results[i] = on
		}(i)
	}
	close(start)
	wg.Wait()
	t.Logf("PRAGMA foreign_keys per goroutine: %v", results)

	off := 0
	for _, v := range results {
		if v == 0 {
			off++
		}
	}
	if off > 0 {
		t.Errorf("%d of 8 reads saw foreign_keys OFF; cascades are not reliable", off)
	}
}

// The cascade must hold on a file-backed store, which is the only kind a real
// deployment has. An in-memory store pins the pool to one connection and so
// cannot see the defect above.
func TestPointListCascadeOnFileBackedStore(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.CreateAsset(&Asset{ID: "a", Name: "A", TemplateName: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertPointList(&PointList{
		AssetID: "a", Protocol: "modbus-tcp",
		Points: []Point{{Name: "p", ValueType: ValueTypeNumber, Address: "0", Enabled: true}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteAsset("a"); err != nil {
		t.Fatal(err)
	}

	pl, err := store.GetPointList("a")
	if err != nil {
		t.Fatal(err)
	}
	if pl != nil {
		t.Errorf("point list survived the asset delete: FK cascade did not fire")
	}
	n, err := store.CountPoints()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d orphan points remain", n)
	}
}
