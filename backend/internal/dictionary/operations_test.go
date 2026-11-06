package dictionary

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/myonku/lens-qs-api/internal/store"
)

func TestSyncOperationsPreservesIdentityAndHistory(t *testing.T) {
	db, driver := store.FixtureDB(t, true)
	s := NewStore(db, driver)
	ctx := context.Background()
	meta := `{"aliases":["旧别名"]}`
	old, err := s.UpsertItem(ctx, &Item{Category: CategoryInspectionOp, Code: "一次加硬", Label: "旧名", Active: true, Meta: &meta})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertItem(ctx, &Item{Category: CategoryInspectionOp, Code: "GENERAL", Label: "一般检验", Active: true}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "operations.csv")
	write := func(raw string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("code,label,sort_no,active\n一次加硬,一次加硬,10,true\nNEW,新工序,20,true\n")
	if n, err := s.SyncOperationsFile(ctx, file); err != nil || n != 2 {
		t.Fatalf("sync: %d %v", n, err)
	}
	var got Item
	if err := db.Get(&got, db.Rebind(`SELECT `+itemColumns+` FROM dict_items WHERE id=?`), old.ID); err != nil {
		t.Fatal(err)
	}
	if got.Label != "一次加硬" || got.SortNo != 10 || got.CreatedAt != old.CreatedAt || got.Meta == nil || *got.Meta != meta {
		t.Fatalf("lost identity/metadata: %+v", got)
	}
	var before, after int
	if err := db.Get(&before, `SELECT COUNT(*) FROM business_events`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SyncOperationsFile(ctx, file); err != nil || n != 0 {
		t.Fatalf("repeat sync: %d %v", n, err)
	}
	if err := db.Get(&after, `SELECT COUNT(*) FROM business_events`); err != nil || before != after {
		t.Fatalf("repeat created events: %d %d %v", before, after, err)
	}
	write("code,label,sort_no,active\n一次加硬,停用后的名称,15,false\n")
	if n, err := s.SyncOperationsFile(ctx, file); err != nil || n != 1 {
		t.Fatalf("disable: %d %v", n, err)
	}
	active, err := s.ListItems(ctx, CategoryInspectionOp, false)
	if err != nil || len(active) != 2 {
		t.Fatalf("omitted rows should remain active: %+v %v", active, err)
	}
	write("code,label,sort_no,active\nEXTRA,不能部分保存,30,true\nBAD,错误,40,yes\n")
	if _, err := s.SyncOperationsFile(ctx, file); err == nil {
		t.Fatal("invalid file accepted")
	}
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM dict_items WHERE code='EXTRA'`); err != nil || n != 0 {
		t.Fatal("partial sync", err)
	}
	if _, err := s.SyncOperationsFile(ctx, file+".missing"); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestOperationCSVValidation(t *testing.T) {
	for _, raw := range []string{
		"", "code,label,sort_no,active\n", "wrong,header\n",
		"code,label,sort_no,active\nA,名称,0,true\n A ,重复,1,true\n",
		"code,label,sort_no,active\n,名称,0,true\n",
		"code,label,sort_no,active\nA,,0,true\n",
		"code,label,sort_no,active\nA,名称,no,true\n",
		"code,label,sort_no,active\nA,名称,0\n",
	} {
		if _, err := readOperations(strings.NewReader(raw)); err == nil {
			t.Errorf("accepted invalid CSV %q", raw)
		}
	}
	items, err := readOperations(strings.NewReader("\ufeffcode,label,sort_no,active\r\n A , 名称 ,10,true\r\n"))
	if err != nil || len(items) != 1 || items[0].Code != "A" || items[0].Label != "名称" {
		t.Fatalf("BOM/CRLF: %+v %v", items, err)
	}
}

func TestBundledOperations(t *testing.T) {
	db, driver := store.FixtureDB(t, true)
	s := NewStore(db, driver)
	if n, err := s.SyncOperationsFile(context.Background(), "../../data/inspection_operations.csv"); err != nil || n != 13 {
		t.Fatalf("bundled sync: %d %v", n, err)
	}
	items, err := s.ListItems(context.Background(), CategoryInspectionOp, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"一次加硬", "一次固化", "一次清洗", "PhCh旋涂变色", "紫外线保护", "二次加硬", "二次固化", "ARC镀膜", "DMC打码", "CNC裁型", "Logo打标", "二次清洗", "OQC+包装"}
	for i, label := range want {
		if items[i].Label != label {
			t.Fatalf("position %d: got %q want %q", i, items[i].Label, label)
		}
		templates, err := store.Templates(context.Background(), db, items[i].ID)
		if err != nil || len(templates) != 1 {
			t.Fatalf("missing template: %q %v", label, err)
		}
	}
}

func TestOperationSyncRollsBackDatabaseFailure(t *testing.T) {
	db := store.NewTestDB(t)
	// Fail the second row after the first row and its event have been written.
	if _, err := db.Exec(`CREATE TRIGGER reject_operation BEFORE INSERT ON dict_items WHEN NEW.code='FAIL' BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "operations.csv")
	if err := os.WriteFile(file, []byte("code,label,sort_no,active\nOK,成功,10,true\nFAIL,失败,20,true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(db, "sqlite").SyncOperationsFile(context.Background(), file); err == nil {
		t.Fatal("database failure ignored")
	}
	for _, table := range []string{"dict_items", "business_events", "event_transactions"} {
		var n int
		if err := db.Get(&n, `SELECT COUNT(*) FROM `+table); err != nil || n != 0 {
			t.Fatalf("partial write in %s: %d %v", table, n, err)
		}
	}
}
