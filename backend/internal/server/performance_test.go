package server

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// This opt-in test builds an annual-scale isolated SQL fixture, then measures
// real authenticated HTTP requests. Never point it at an application database.
func TestG2AnnualScalePerformance(t *testing.T) {
	if os.Getenv("LENS_G2_PERF") != "1" {
		t.Skip("explicit annual-scale performance run")
	}
	oldWriter := gin.DefaultWriter
	gin.DefaultWriter = io.Discard
	defer func() { gin.DefaultWriter = oldWriter }()
	f := newAPI(t)
	lot := f.lot()
	op := f.op("PERF")
	g := f.group(lot.ID, op, f.token)
	source := f.save(g.ID, "source", record)
	ctx := context.Background()
	tx, e := f.db.BeginTxx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := tx.ExecContext(ctx, tx.Rebind(q), args...); e != nil {
			t.Fatal(q, e)
		}
	}
	exec(`CREATE TABLE g2_load(n INTEGER PRIMARY KEY)`)
	if f.db.DriverName() == "pgx" {
		exec(`INSERT INTO g2_load SELECT generate_series(0,99999)`)
	} else {
		exec(`INSERT INTO g2_load WITH RECURSIVE seq(n) AS (SELECT 0 UNION ALL SELECT n+1 FROM seq WHERE n<99999) SELECT n FROM seq`)
	}
	// Every 100 submissions share a lot/group. One percent use machine target,
	// producing a 1000-record query scope across a 100000-record database.
	exec(`INSERT INTO product_lots(id,shared_form_no,sample_identity,sample_identity_key,version,created_by,created_at) SELECT 'perf-lot-'||CAST(n AS TEXT),'PERF-'||CAST(n AS TEXT),'X','X',1,?,? FROM g2_load WHERE n<1000`, f.uid, source.Submission.CreatedAt)
	exec(`INSERT INTO operation_groups(id,product_lot_id,operation_id,operation_code,operation_label,inspection_kind,template_version,reporting_state,batch_mode,version,created_by,created_at) SELECT 'perf-group-'||CAST(n AS TEXT),'perf-lot-'||CAST(n AS TEXT),?,'PERF','性能工序','NORMAL','general-1','OPEN','SPLIT',101,?,? FROM g2_load WHERE n<1000`, op, f.uid, source.Submission.CreatedAt)
	exec(`INSERT INTO inspection_submissions(id,operation_group_id,batch_no,scope,inspection_kind,state,version,current_revision_id,created_by,created_at,last_saved_by,last_saved_at) SELECT 'perf-sub-'||CAST(n AS TEXT),'perf-group-'||CAST(n/100 AS TEXT),CAST(n AS TEXT),'SPLIT','NORMAL','ACTIVE',CASE WHEN n%10=0 THEN 2 ELSE 1 END,'perf-rev-'||CAST(n AS TEXT)||CASE WHEN n%10=0 THEN '-next' ELSE '' END,?,?,?,? FROM g2_load`, f.uid, source.Submission.CreatedAt, f.uid, source.Submission.LastSavedAt)
	rs, e := tx.Queryx(`SELECT * FROM submission_revisions LIMIT 0`)
	if e != nil {
		t.Fatal(e)
	}
	cols, e := rs.Columns()
	rs.Close()
	if e != nil {
		t.Fatal(e)
	}
	for _, next := range []bool{false, true} {
		expr := []string{}
		for _, col := range cols {
			value := "r." + col
			switch col {
			case "id":
				value = "'perf-rev-'||CAST(n AS TEXT)"
				if next {
					value += "||'-next'"
				}
			case "submission_id":
				value = "'perf-sub-'||CAST(n AS TEXT)"
			case "revision_no":
				value = "1"
				if next {
					value = "2"
				}
			case "base_revision_id":
				value = "NULL"
				if next {
					value = "'perf-rev-'||CAST(n AS TEXT)"
				}
			case "machine_no":
				value = "CASE WHEN n%100=0 THEN 'target' ELSE 'other' END"
			case "content_json":
				value = `REPLACE(r.content_json,'"machine_no":"M01"',CASE WHEN n%100=0 THEN '"machine_no":"target"' ELSE '"machine_no":"other"' END)`
			}
			expr = append(expr, value)
		}
		where := ""
		if next {
			where = " AND n%10=0"
		}
		exec(`INSERT INTO submission_revisions(`+strings.Join(cols, ",")+`) SELECT `+strings.Join(expr, ",")+` FROM g2_load CROSS JOIN submission_revisions r WHERE r.id=?`+where, source.Revision.ID)
	}
	rs, e = tx.Queryx(`SELECT * FROM revision_lines LIMIT 0`)
	if e != nil {
		t.Fatal(e)
	}
	lineCols, e := rs.Columns()
	rs.Close()
	if e != nil {
		t.Fatal(e)
	}
	for _, next := range []bool{false, true} {
		expr := []string{}
		for _, col := range lineCols {
			value := "l." + col
			if col == "revision_id" {
				value = "'perf-rev-'||CAST(n AS TEXT)"
				if next {
					value += "||'-next'"
				}
			}
			expr = append(expr, value)
		}
		where := ""
		if next {
			where = " AND n%10=0"
		}
		exec(`INSERT INTO revision_lines(`+strings.Join(lineCols, ",")+`) SELECT `+strings.Join(expr, ",")+` FROM g2_load CROSS JOIN revision_lines l WHERE l.revision_id=?`+where, source.Revision.ID)
	}
	exec(`INSERT INTO submission_slots(operation_group_id,batch_key,submission_id) SELECT operation_group_id,batch_no,id FROM inspection_submissions WHERE id LIKE 'perf-sub-%'`)
	exec(`INSERT INTO quantity_changes(id,revision_id,kind,direction,quantity,reason,occurred_date,occurred_at) SELECT 'perf-change-'||r.id,r.id,'LOSS','DECREASE',NULL,'待核对',NULL,NULL FROM submission_revisions r WHERE r.id LIKE 'perf-rev-%'`)
	// The load is one complete fixture transaction. History snapshots remain
	// internally verifiable; production HTTP batch limits are tested separately.
	exec(`INSERT INTO event_transactions(id,domain,created_at) VALUES('perf-load',1,?)`, source.Submission.CreatedAt)
	object := func(pairs string) string {
		fn := "json_object"
		if f.db.DriverName() == "pgx" {
			fn = "json_build_object"
		}
		return "CAST(" + fn + "(" + pairs + ") AS TEXT)"
	}
	lotJSON := object(`'id',p.id,'shared_form_no',p.shared_form_no,'sample_identity',p.sample_identity,'sample_identity_key',p.sample_identity_key,'version',p.version,'created_by',p.created_by,'created_at',p.created_at`)
	groupJSON := object(`'id',g.id,'product_lot_id',g.product_lot_id,'operation_id',g.operation_id,'operation_code',g.operation_code,'operation_label',g.operation_label,'inspection_kind',g.inspection_kind,'template_version',g.template_version,'reporting_state',g.reporting_state,'batch_mode',g.batch_mode,'version',g.version,'created_by',g.created_by,'created_at',g.created_at`)
	subJSON := object(`'id',s.id,'operation_group_id',s.operation_group_id,'batch_no',s.batch_no,'scope',s.scope,'inspection_kind',s.inspection_kind,'state',s.state,'version',s.version,'current_revision_id',s.current_revision_id,'created_by',s.created_by,'created_at',s.created_at,'last_saved_by',s.last_saved_by,'last_saved_at',s.last_saved_at`)
	for _, entity := range []struct{ table, alias, pattern, action, json string }{{"product_lots", "p", "perf-lot-%", "create", lotJSON}, {"operation_groups", "g", "perf-group-%", "create", groupJSON}, {"inspection_submissions", "s", "perf-sub-%", "content_saved", subJSON}} {
		name := map[string]string{"product_lots": "product_lot", "operation_groups": "operation_group", "inspection_submissions": "submission"}[entity.table]
		exec(`INSERT INTO business_events(transaction_id,actor_id,actor_name,action,entity,entity_id,after_json,created_at) SELECT 'perf-load',?,'性能操作者',?,?,`+entity.alias+`.id,`+entity.json+`,? FROM `+entity.table+` `+entity.alias+` WHERE `+entity.alias+`.id LIKE ?`, f.uid, entity.action, name, source.Submission.CreatedAt, entity.pattern)
	}
	exec(`UPDATE event_transactions SET last_event_id=(SELECT MAX(event_id) FROM business_events) WHERE id='perf-load'`)
	exec(`DROP TABLE g2_load`)
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`ANALYZE`); e != nil {
		t.Fatal(e)
	}
	planQuery := `SELECT s.id FROM inspection_submissions s JOIN submission_revisions r ON r.id=s.current_revision_id AND r.submission_id=s.id WHERE r.machine_no='target' ORDER BY s.created_at DESC,s.id DESC LIMIT 100`
	prefix := "EXPLAIN QUERY PLAN "
	if f.db.DriverName() == "pgx" {
		prefix = "EXPLAIN "
	}
	plan, e := f.db.Queryx(prefix + planQuery)
	if e != nil {
		t.Fatal(e)
	}
	planParts := []string{}
	for plan.Next() {
		values, e := plan.SliceScan()
		if e != nil {
			t.Fatal(e)
		}
		planParts = append(planParts, fmt.Sprint(values))
	}
	plan.Close()
	t.Logf("PLAN %s", strings.Join(planParts, " | "))
	duration := func(method, path, body, key string) (time.Duration, int) {
		req := httptest.NewRequest(method, "/api/v2"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("X-Idempotency-Key", key)
		w := httptest.NewRecorder()
		start := time.Now()
		f.r.ServeHTTP(w, req)
		return time.Since(start), w.Code
	}
	measure := func(name, path string, count int) time.Duration {
		times := []time.Duration{}
		for i := 0; i < count; i++ {
			d, status := duration("GET", path, "", "")
			if status != 200 {
				t.Fatalf("%s status %d", name, status)
			}
			times = append(times, d)
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		p95 := times[(len(times)*95+99)/100-1]
		t.Logf("PERF %s samples=%d P95=%s", name, count, p95)
		return p95
	}
	queryP95 := measure("1000-scope/page100", "/submissions?machine_no=target&page_size=100", 20)
	measure("summary-100000", "/stats/summary?group_by=machine_no", 5)
	measure("defects-1000", "/stats/defects?machine_no=target", 5)
	measure("history-lot100", "/reports/product-lots/perf-lot-0", 5)
	var wg sync.WaitGroup
	latencies := make(chan time.Duration, 96)
	failures := make(chan int, 96)
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 6; j++ {
				key := fmt.Sprintf("perf-save-%d-%d", worker, j)
				body := fmt.Sprintf(`{"operation_group_id":%q,"batch_no":%q,"content":%s}`, g.ID, key, record)
				d, status := duration("POST", "/submissions", body, key)
				if status != 201 {
					failures <- status
				}
				latencies <- d
			}
		}(worker)
	}
	wg.Wait()
	close(latencies)
	close(failures)
	for status := range failures {
		t.Errorf("save status %d", status)
	}
	saves := []time.Duration{}
	for d := range latencies {
		saves = append(saves, d)
	}
	sort.Slice(saves, func(i, j int) bool { return saves[i] < saves[j] })
	saveP95 := saves[(len(saves)*95+99)/100-1]
	t.Logf("PERF database=%s OS=%s Go=%s CPU=%d submissions=100001 revisions=110001 lines=110001 concurrency=16 save_samples=96 save_P95=%s query_P95=%s", f.db.DriverName(), runtime.GOOS, runtime.Version(), runtime.NumCPU(), saveP95, queryP95)
	if saveP95 >= 500*time.Millisecond || queryP95 >= 2*time.Second {
		t.Fatal("performance target missed")
	}
	var total int
	f.db.Get(&total, `SELECT COUNT(*) FROM submission_revisions`)
	if total < 110000 {
		t.Fatal("invalid fixture")
	}
}
