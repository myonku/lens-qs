package ledger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/store"
)

// Quantity-change row IDs are server evidence, separate from the immutable
// editable content schema. They never enter Merge or the persisted content JSON.
type changeOutput struct {
	QuantityChange
	ID string `json:"id,omitempty"`
}
type contentOutput struct {
	Content
	Changes []changeOutput `json:"quantity_changes"`
}

func (r Revision) MarshalJSON() ([]byte, error) {
	type value Revision
	changes := make([]changeOutput, len(r.Content.Changes))
	for i, c := range r.Content.Changes {
		id := ""
		if i < len(r.changeIDs) {
			id = r.changeIDs[i]
		}
		changes[i] = changeOutput{c, id}
	}
	return json.Marshal(struct {
		value
		Content contentOutput `json:"content"`
	}{value(r), contentOutput{r.Content, changes}})
}
func (r *Revision) UnmarshalJSON(raw []byte) error {
	type value Revision
	var aux struct {
		*value
		Content contentOutput `json:"content"`
	}
	aux.value = (*value)(r)
	if e := json.Unmarshal(raw, &aux); e != nil {
		return e
	}
	r.Content = aux.Content.Content
	r.Content.Changes = make([]QuantityChange, len(aux.Content.Changes))
	r.changeIDs = make([]string, len(aux.Content.Changes))
	for i, c := range aux.Content.Changes {
		r.Content.Changes[i] = c.QuantityChange
		r.changeIDs[i] = c.ID
	}
	return nil
}
func changeKey(c QuantityChange) string { raw, _ := json.Marshal(c); return string(raw) }
func attachChangeIDs(ctx context.Context, q store.Reader, revisions []Revision) error {
	ids := []string{}
	for _, r := range revisions {
		if len(r.Content.Changes) > 0 {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	query, args, e := sqlx.In(`SELECT id,revision_id,kind,direction,quantity,reason,occurred_date,occurred_at FROM quantity_changes WHERE revision_id IN (?) ORDER BY id`, ids)
	if e != nil {
		return e
	}
	var rows []struct {
		ID         string  `db:"id"`
		RevisionID string  `db:"revision_id"`
		Kind       string  `db:"kind"`
		Direction  string  `db:"direction"`
		Quantity   *int64  `db:"quantity"`
		Reason     string  `db:"reason"`
		Date       *string `db:"occurred_date"`
		At         *string `db:"occurred_at"`
	}
	if e = q.SelectContext(ctx, &rows, q.Rebind(query), args...); e != nil {
		return e
	}
	byContent := map[string][]string{}
	for _, row := range rows {
		key := row.RevisionID + "\x00" + changeKey(QuantityChange{row.Kind, row.Direction, row.Quantity, row.Reason, row.Date, row.At})
		byContent[key] = append(byContent[key], row.ID)
	}
	for i := range revisions {
		r := &revisions[i]
		r.changeIDs = make([]string, len(r.Content.Changes))
		for j, c := range r.Content.Changes {
			key := r.ID + "\x00" + changeKey(c)
			ids := byContent[key]
			if len(ids) == 0 {
				return fmt.Errorf("quantity-change projection differs from saved content")
			}
			r.changeIDs[j] = ids[0]
			byContent[key] = ids[1:]
		}
	}
	return nil
}
