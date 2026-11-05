package store

import (
	"context"
	"encoding/json"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

type Template struct {
	OperationID    string   `json:"operation_id"`
	OperationCode  string   `json:"operation_code"`
	OperationLabel string   `json:"operation_label"`
	Version        string   `json:"template_version"`
	Schema         string   `json:"schema_version"`
	Layout         []string `json:"layout"`
}

func ReadCatalog(ctx context.Context, db *sqlx.DB, version string) (formfields.FieldCatalog, error) {
	var c formfields.FieldCatalog
	var raw string
	e := db.GetContext(ctx, &raw, db.Rebind(`SELECT definition FROM field_schemas WHERE version=?`), version)
	if e != nil {
		return c, e
	}
	e = json.Unmarshal([]byte(raw), &c)
	return c, e
}
func Templates(ctx context.Context, db *sqlx.DB, operationID string) ([]Template, error) {
	// Catalog/template evidence is read in one statement, including the operation label.
	var rows []struct {
		ID      string `db:"id"`
		Code    string `db:"code"`
		Label   string `db:"label"`
		Version string `db:"version"`
		Schema  string `db:"schema_version"`
		Layout  string `db:"layout"`
	}
	q := `SELECT d.id,d.code,d.label,t.version,t.schema_version,t.layout FROM dict_items d CROSS JOIN form_templates t WHERE d.category='inspection_operation' AND t.version='general-1'`
	args := []any{}
	if operationID == "" {
		q += ` AND d.active=1`
	} else {
		q += ` AND d.id=?`
		args = append(args, operationID)
	}
	q += ` ORDER BY d.sort_no,d.code`
	e := db.SelectContext(ctx, &rows, db.Rebind(q), args...)
	if e != nil {
		return nil, e
	}
	out := []Template{}
	for _, r := range rows {
		var layout []string
		if e = json.Unmarshal([]byte(r.Layout), &layout); e != nil {
			return nil, e
		}
		out = append(out, Template{r.ID, r.Code, r.Label, r.Version, r.Schema, layout})
	}
	if operationID != "" && len(out) == 0 {
		return nil, httpx.NotFound("operation")
	}
	return out, nil
}
