package dictionary

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/myonku/lens-qs-api/internal/store"
)

// SyncOperationsFile validates the entire CSV before applying one atomic sync.
// Codes are stable identities: existing IDs and metadata are preserved. Omitted
// rows are left untouched; disabling an operation requires active=false.
func (s *Store) SyncOperationsFile(ctx context.Context, filename string) (int, error) {
	f, err := os.Open(filename)
	if err != nil {
		return 0, fmt.Errorf("open operation catalog %s: %w", filename, err)
	}
	defer f.Close()
	items, err := readOperations(f)
	if err != nil {
		return 0, fmt.Errorf("operation catalog %s: %w", filename, err)
	}
	w, err := store.BeginWrite(ctx, s.db, "", false, nil)
	if err != nil {
		return 0, err
	}
	defer w.Close()
	changed := 0
	for _, item := range items {
		var old Item
		err = w.GetContext(w.Context, &old, w.Rebind(`SELECT `+itemColumns+` FROM dict_items WHERE category=? AND code=?`), CategoryInspectionOp, item.Code)
		var before any
		if err == nil {
			if old.Label == item.Label && old.SortNo == item.SortNo && old.Active == item.Active {
				continue
			}
			before = old
			item.ID, item.CreatedAt, item.Meta = old.ID, old.CreatedAt, old.Meta
		} else if err != sql.ErrNoRows {
			return 0, err
		}
		if _, err = s.persistItem(w, &item, before); err != nil {
			return 0, err
		}
		changed++
	}
	if err = w.Commit(); err != nil {
		return 0, err
	}
	return changed, nil
}

func readOperations(input io.Reader) ([]Item, error) {
	r := csv.NewReader(input)
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	// Accept UTF-8 BOMs produced by common spreadsheet editors.
	header[0] = strings.TrimPrefix(header[0], "\ufeff")
	if strings.Join(header, ",") != "code,label,sort_no,active" {
		return nil, fmt.Errorf("expected header code,label,sort_no,active")
	}
	items := []Item{}
	seen := map[string]bool{}
	for row := 2; ; row++ {
		values, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", row, err)
		}
		for i := range values {
			values[i] = strings.TrimSpace(values[i])
		}
		if values[0] == "" || values[1] == "" || seen[values[0]] {
			return nil, fmt.Errorf("row %d: code/label required and code must be unique", row)
		}
		sortNo, err := strconv.Atoi(values[2])
		if err != nil {
			return nil, fmt.Errorf("row %d: sort_no must be an integer", row)
		}
		if values[3] != "true" && values[3] != "false" {
			return nil, fmt.Errorf("row %d: active must be true or false", row)
		}
		seen[values[0]] = true
		items = append(items, Item{Category: CategoryInspectionOp, Code: values[0], Label: values[1], SortNo: sortNo, Active: values[3] == "true"})
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("at least one operation required")
	}
	return items, nil
}
